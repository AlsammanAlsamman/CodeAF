package resident

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Agent-Field/codeaf/internal/env"
	"github.com/Agent-Field/codeaf/internal/store"
)

const (
	skillCandidateScanLimit = 10_000
	skillTrialTimeout       = 10 * time.Second
	skillFailureBytes       = 400
)

// Both halves of the shelf are gated on the journal (memo.go), and for the same
// reason: each of them exists to make the disk agree with the fact shelf, the
// fact shelf only moves when something is journaled, and neither of them was
// cheap. Promotion walks every candidate's parent chain back to its top-level
// job — one node read per generation, per candidate. The bin sync stats and
// readlinks the whole shelf directory. A tick that runs for a reason unrelated
// to either — a clock deadline, the standing ceiling — used to pay for both
// anyway, twice a second, forever, which is what an idle laptop heard as a disk
// that never spun down.
//
// The gates are separate because the two passes do not run back to back and a
// shared one would let whichever ran first suppress the other. They are in
// memory rather than durable, unlike the consolidation lane's: the consolidator
// spends a model call, so a restart buying another one is expensive, whereas a
// restart here costs one extra read of a shelf that is almost always empty.

type skillRecurrence struct {
	facts []store.Fact
	jobs  map[string]bool
}

// promoteRecurringSkills is the retrospective's mechanical half. The model
// may propose a procedure after one job, but only two independent top-level
// jobs and a green executable check can make it active.
func (r *Reconciler) promoteRecurringSkills(ctx context.Context) {
	if !r.skillPromotionGate.due(r.store) {
		return
	}
	candidates, err := r.store.SkillFacts(store.FactCandidate, skillCandidateScanLimit)
	if err != nil || len(candidates) == 0 {
		return
	}
	root, err := store.SkillsRoot()
	if err != nil || os.MkdirAll(root, 0o755) != nil {
		return
	}

	groups := make(map[string]*skillRecurrence)
	for _, candidate := range candidates {
		jobID, ok := r.topLevelJobID(candidate.NodeID)
		if !ok {
			continue
		}
		key := skillMatchKey(candidate)
		group := groups[key]
		if group == nil {
			group = &skillRecurrence{jobs: make(map[string]bool)}
			groups[key] = group
		}
		group.facts = append(group.facts, candidate)
		group.jobs[jobID] = true
	}
	keys := make([]string, 0, len(groups))
	promotionOccurrences := int(r.store.Parameter(store.ParameterSkillPromotionOccurrences))
	for key, group := range groups {
		if len(group.jobs) >= promotionOccurrences {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	for _, key := range keys {
		if ctx.Err() != nil {
			return
		}
		group := groups[key]
		selected := group.facts[0] // SkillFacts is newest first.
		jobs := make([]string, 0, len(group.jobs))
		for jobID := range group.jobs {
			jobs = append(jobs, jobID)
		}
		sort.Strings(jobs)

		installed, err := installSkillTrial(ctx, root, selected, jobs)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			reason := skillFailureReason(err)
			for _, candidate := range group.facts {
				_ = r.store.SupersedeFactWithReason(candidate.Seq, 0, reason)
			}
			continue
		}
		if err := r.store.ActivateSkill(selected.Seq, installed); err != nil {
			continue
		}
		r.queueLearningMoment(selected.NodeID, forgedSkillMoment(filepath.Base(installed)))
		reason := fmt.Sprintf("matched independent jobs and promoted as skill #%d", selected.Seq)
		for _, candidate := range group.facts {
			if candidate.Seq != selected.Seq {
				_ = r.store.SupersedeFactWithReason(candidate.Seq, selected.Seq, reason)
			}
		}
	}
}

func (r *Reconciler) topLevelJobID(nodeID string) (string, bool) {
	if nodeID == "" || nodeID == store.RootID {
		return "", false
	}
	node, ok, err := r.store.Node(nodeID)
	if err != nil || !ok {
		return "", false
	}
	for node.Parent != store.RootID {
		if node.Parent == "" {
			return "", false
		}
		node, ok, err = r.store.Node(node.Parent)
		if err != nil || !ok {
			return "", false
		}
	}
	return node.ID, true
}

func skillMatchKey(fact store.Fact) string {
	scope := strings.ToLower(strings.TrimSpace(fact.Scope))
	doc := strings.Join(strings.Fields(strings.ToLower(fact.Body)), " ")
	return scope + "\x00" + doc
}

func installSkillTrial(ctx context.Context, root string, candidate store.Fact, jobs []string) (string, error) {
	rawSource := strings.TrimSpace(candidate.Artifact)
	if !filepath.IsAbs(rawSource) {
		return "", fmt.Errorf("candidate artifact %q is not absolute", rawSource)
	}
	source, err := filepath.Abs(rawSource)
	if err != nil {
		return "", fmt.Errorf("resolve candidate artifact: %w", err)
	}
	if pathsOverlap(source, root) {
		return "", fmt.Errorf("candidate artifact %q overlaps the skill shelf", source)
	}
	staging, err := os.MkdirTemp(root, ".candidate-")
	if err != nil {
		return "", fmt.Errorf("create skill staging directory: %w", err)
	}
	defer os.RemoveAll(staging)

	if err := copySkillDirectory(source, staging); err != nil {
		return "", fmt.Errorf("prepare skill trial: %w", err)
	}
	provenance := strings.Join(jobs, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(staging, "PROVENANCE"), []byte(provenance), 0o644); err != nil {
		return "", fmt.Errorf("write skill provenance: %w", err)
	}
	if err := runSkillCheck(ctx, staging); err != nil {
		return "", err
	}
	if err := runSkillStones(ctx, staging); err != nil {
		return "", err
	}
	if _, err := skillExecutable(staging); err != nil {
		return "", fmt.Errorf("check.sh removed the skill executable: %w", err)
	}
	if err := os.WriteFile(filepath.Join(staging, "PROVENANCE"), []byte(provenance), 0o644); err != nil {
		return "", fmt.Errorf("rewrite skill provenance: %w", err)
	}

	slug := skillSlug(filepath.Base(source))
	target := filepath.Join(root, slug)
	if _, err := os.Lstat(target); err == nil {
		// The artifact's own name is the command workers were taught. Preserve
		// it normally; only a real shelf collision earns a durable sequence
		// suffix, so installation never overwrites another learned capability.
		slug += "-" + strconv.FormatInt(candidate.Seq, 10)
		target = filepath.Join(root, slug)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("install skill: inspect target: %w", err)
	}
	if _, err := os.Lstat(target); err == nil {
		return "", fmt.Errorf("install skill: target %q already exists", target)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("install skill: inspect target: %w", err)
	}
	if err := os.Rename(staging, target); err != nil {
		return "", fmt.Errorf("install skill: %w", err)
	}
	return target, nil
}

func copySkillDirectory(source, target string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return fmt.Errorf("inspect artifact: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("artifact %q is not a real directory", source)
	}
	err = filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("artifact contains symlink %q", relative)
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o755)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("artifact contains non-regular file %q", relative)
		}
		return copySkillFile(path, destination, info.Mode().Perm())
	})
	if err != nil {
		return err
	}
	check := filepath.Join(target, "check.sh")
	if !regularExecutable(check) {
		return fmt.Errorf("artifact needs executable check.sh")
	}
	if _, err := skillExecutable(target); err != nil {
		return err
	}
	return nil
}

func copySkillFile(source, target string, mode fs.FileMode) error {
	reader, err := os.Open(source)
	if err != nil {
		return err
	}
	defer reader.Close()
	writer, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(writer, reader)
	closeErr := writer.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func runSkillCheck(ctx context.Context, skillDir string) error {
	clean, err := os.MkdirTemp("", "codeaf-skill-check-")
	if err != nil {
		return fmt.Errorf("create clean check directory: %w", err)
	}
	defer os.RemoveAll(clean)
	return runSkillCheckIn(ctx, skillDir, clean, nil)
}

func runSkillCheckIn(ctx context.Context, skillDir, cwd string, extraEnv []string) error {
	trialCtx, cancel := context.WithTimeout(ctx, skillTrialTimeout)
	defer cancel()
	cmd := exec.CommandContext(trialCtx, filepath.Join(skillDir, "check.sh"))
	cmd.Dir = cwd
	const skillDirEnv = "CODEAF_SKILL_DIR"
	cmd.Env = append(os.Environ(), skillDirEnv+"="+skillDir, env.Legacy(skillDirEnv)+"="+skillDir)
	cmd.Env = append(cmd.Env, extraEnv...)
	cmd.WaitDelay = time.Second
	output, runErr := cmd.CombinedOutput()
	if trialCtx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("check.sh timed out after %s: %s", skillTrialTimeout, boundedSkillOutput(output))
	}
	if runErr != nil {
		return fmt.Errorf("check.sh failed: %v: %s", runErr, boundedSkillOutput(output))
	}
	return nil
}

// runSkillStones is the trial's QC round, run only once the base check has
// already gone green. A candidate's own check.sh is written by whatever
// taught it the skill — it proves the happy path, nothing more. These stones
// are external and adversarial: each re-runs the same check.sh under a
// condition its author never wrote it for, on the same principle SkillJab
// (github.com/AlsammanAlsamman/skilljab) applies to data pipelines — plant
// nothing new, just stop trusting a check that has only ever run once, in
// one pristine directory it will never see again in the wild. A green base
// check that goes red under a stone is a silent failure the author's own
// check could never have caught; it supersedes the candidate with the stone
// that caught it as evidence, the same way any other red check does.
func runSkillStones(ctx context.Context, skillDir string) error {
	stones := []struct {
		name string
		run  func(context.Context, string) error
	}{
		{"reused directory", skillStoneReusedDir},
		{"pre-existing files", skillStoneDirtyWorkspace},
		{"unrelated environment noise", skillStoneEnvNoise},
	}
	for _, stone := range stones {
		if err := stone.run(ctx, skillDir); err != nil {
			return fmt.Errorf("stone %q: %w", stone.name, err)
		}
	}
	return nil
}

// skillStoneReusedDir catches a check that only works once — a lockfile it
// never cleans up, a directory it assumes doesn't exist yet, output it
// appends to without checking. The resident's real ticks never hand a skill
// a directory it hasn't already touched; a check that requires one is
// already broken on the second run nobody will notice failed.
func skillStoneReusedDir(ctx context.Context, skillDir string) error {
	clean, err := os.MkdirTemp("", "codeaf-skill-stone-")
	if err != nil {
		return fmt.Errorf("create stone directory: %w", err)
	}
	defer os.RemoveAll(clean)
	if err := runSkillCheckIn(ctx, skillDir, clean, nil); err != nil {
		return err
	}
	return runSkillCheckIn(ctx, skillDir, clean, nil)
}

// skillStoneDirtyWorkspace catches a check that silently assumes an empty
// directory — counting entries, globbing everything present, or writing a
// name that happens not to collide only because nothing else was there.
func skillStoneDirtyWorkspace(ctx context.Context, skillDir string) error {
	clean, err := os.MkdirTemp("", "codeaf-skill-stone-")
	if err != nil {
		return fmt.Errorf("create stone directory: %w", err)
	}
	defer os.RemoveAll(clean)
	decoys := []string{"notes.txt", ".leftover", "output"}
	for _, name := range decoys {
		if err := os.WriteFile(filepath.Join(clean, name), []byte("decoy"), 0o644); err != nil {
			return fmt.Errorf("seed decoy file %q: %w", name, err)
		}
	}
	return runSkillCheckIn(ctx, skillDir, clean, nil)
}

// skillStoneEnvNoise catches a check that only passes in the resident's own
// process environment — reading a variable a real shell session sets and
// codeaf's own trial happens not to, or breaking when one it doesn't expect
// is present.
func skillStoneEnvNoise(ctx context.Context, skillDir string) error {
	clean, err := os.MkdirTemp("", "codeaf-skill-stone-")
	if err != nil {
		return fmt.Errorf("create stone directory: %w", err)
	}
	defer os.RemoveAll(clean)
	noise := []string{"CI=true", "LC_ALL=C", "TZ=UTC"}
	return runSkillCheckIn(ctx, skillDir, clean, noise)
}

func boundedSkillOutput(output []byte) string {
	text := strings.TrimSpace(string(output))
	if len(text) <= skillFailureBytes {
		return text
	}
	return clipBlock(text, skillFailureBytes)
}

func skillFailureReason(err error) string {
	reason := "skill trial failed: " + strings.TrimSpace(err.Error())
	return clipFactBody(reason)
}

func skillSlug(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var slug strings.Builder
	dash := false
	for _, char := range name {
		switch {
		case char >= 'a' && char <= 'z', char >= '0' && char <= '9':
			slug.WriteRune(char)
			dash = false
		default:
			if slug.Len() > 0 && !dash {
				slug.WriteByte('-')
				dash = true
			}
		}
		if slug.Len() >= 48 {
			break
		}
	}
	result := strings.Trim(slug.String(), "-")
	if result == "" {
		return "skill"
	}
	return result
}

func regularExecutable(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}

func skillExecutable(skillDir string) (string, error) {
	run := filepath.Join(skillDir, "run.sh")
	if regularExecutable(run) {
		return run, nil
	}
	var executable string
	err := filepath.WalkDir(skillDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || executable != "" {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, relErr := filepath.Rel(skillDir, path)
		if relErr != nil {
			return relErr
		}
		if relative == "check.sh" || relative == "PROVENANCE" {
			return nil
		}
		if regularExecutable(path) {
			executable = path
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if executable == "" {
		return "", fmt.Errorf("artifact needs run.sh or another executable")
	}
	return executable, nil
}

func pathsOverlap(first, second string) bool {
	within := func(parent, child string) bool {
		relative, err := filepath.Rel(parent, child)
		return err == nil && relative != ".." &&
			!strings.HasPrefix(relative, ".."+string(os.PathSeparator))
	}
	return within(first, second) || within(second, first)
}

// syncSkillBins makes the active fact view true on disk and retires only bin
// symlinks this forge owns. Installed directories remain as provenance-bearing
// evidence after retirement; they simply stop being offered on PATH.
func (r *Reconciler) syncSkillBins() {
	if !r.skillBinGate.due(r.store) {
		return
	}
	facts, err := r.store.SkillFacts("", skillCandidateScanLimit)
	if err != nil {
		return
	}
	root, err := store.SkillsRoot()
	if err != nil {
		return
	}
	bin := filepath.Join(root, "bin")
	active := make(map[string]bool)
	installed := make(map[string]bool)
	for _, fact := range facts {
		if _, ok := installedSkillSlug(root, fact.Artifact); !ok {
			continue
		}
		installed[fact.Artifact] = true
		if fact.Status == store.FactActive {
			active[fact.Artifact] = true
		}
	}
	if len(active) > 0 {
		if err := os.MkdirAll(bin, 0o755); err != nil {
			return
		}
	}
	for artifact := range active {
		ensureSkillBinLink(root, bin, artifact)
	}
	for artifact := range installed {
		if !active[artifact] {
			removeSkillBinLink(root, bin, artifact)
		}
	}
}

func installedSkillSlug(root, artifact string) (string, bool) {
	if artifact == "" {
		return "", false
	}
	relative, err := filepath.Rel(root, artifact)
	if err != nil || relative == "." || relative == ".." || relative == "bin" ||
		filepath.IsAbs(relative) ||
		strings.HasPrefix(relative, ".."+string(os.PathSeparator)) ||
		strings.Contains(relative, string(os.PathSeparator)) {
		return "", false
	}
	return relative, true
}

func ensureSkillBinLink(root, bin, artifact string) {
	slug, ok := installedSkillSlug(root, artifact)
	if !ok {
		return
	}
	executable, err := skillExecutable(artifact)
	if err != nil {
		return
	}
	link := filepath.Join(bin, slug)
	if target, err := os.Readlink(link); err == nil {
		if !filepath.IsAbs(target) {
			target = filepath.Join(bin, target)
		}
		if filepath.Clean(target) == filepath.Clean(executable) {
			return
		}
		return
	} else if !errors.Is(err, fs.ErrNotExist) {
		return
	}
	_ = os.Symlink(executable, link)
}

func removeSkillBinLink(root, bin, artifact string) {
	slug, ok := installedSkillSlug(root, artifact)
	if !ok {
		return
	}
	link := filepath.Join(bin, slug)
	target, err := os.Readlink(link)
	if err != nil {
		return
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(bin, target)
	}
	relative, err := filepath.Rel(artifact, filepath.Clean(target))
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return
	}
	_ = os.Remove(link)
}
