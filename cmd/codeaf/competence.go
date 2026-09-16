package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Agent-Field/codeaf/internal/config"
	"github.com/Agent-Field/codeaf/internal/env"
	"github.com/Agent-Field/codeaf/internal/profile"
	"github.com/Agent-Field/codeaf/internal/store"
)

func runCompetence(args []string) error {
	return runCompetenceTo(args, os.Stdout, time.Now().UTC())
}

func runCompetenceTo(args []string, output io.Writer, now time.Time) error {
	flags := commandFlags("competence")
	database := flags.String("db", defaultChatDB(), storeFlagHelp)
	modelFlag := flags.String("model", "", "working model whose profile buckets to include")
	htmlFlag := flags.String("html", "", "write an interactive skill atlas to this path instead of the text report")
	if err := parseCommandFlags(flags, reorder(flags, args)); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("usage: codeaf competence [--db path] [--model slug] [--html path]")
	}
	path, err := expandHome(strings.TrimSpace(*database))
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("open competence map: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("open competence map: %s is not a regular database file", path)
	}
	graph, err := store.Open(path)
	if err != nil {
		return err
	}
	defer graph.Close()

	prefs := loadChatPrefs(filepath.Dir(path))
	model := firstNonEmptyString(*modelFlag, prefs.TaskModel, env.Get("CODEAF_MODEL"), config.DefaultModel)
	competence, err := measureCompetence(graph, env.Get("CODEAF_PROFILE_DIR"), model, now)
	if err != nil {
		return err
	}
	if atlasPath := strings.TrimSpace(*htmlFlag); atlasPath != "" {
		return writeCompetenceAtlas(output, competence, now, atlasPath)
	}
	return writeCompetence(output, competence, now)
}

// writeCompetenceAtlas is --html's whole job: render the same CompetenceMap
// the text report reads, to disk, and say where. It never touches the store
// again and never falls back to the text report — an explicit destination
// asked for a file, not a second thing on the terminal.
func writeCompetenceAtlas(output io.Writer, competence store.CompetenceMap, now time.Time, path string) error {
	path, err := expandHome(path)
	if err != nil {
		return err
	}
	page := renderCompetenceAtlas(competence, now)
	if err := os.WriteFile(path, []byte(page), 0o644); err != nil {
		return fmt.Errorf("write skill atlas: %w", err)
	}
	_, err = fmt.Fprintf(output, "skill atlas written to %s\n", path)
	return err
}

func measureCompetence(graph *store.Store, profileDir, model string, now time.Time) (store.CompetenceMap, error) {
	measured, err := profile.Load(profileDir, model, "linear")
	if err != nil {
		return store.CompetenceMap{}, fmt.Errorf("load competence profile: %w", err)
	}
	return graph.CompetenceMap(store.CompetenceOptions{Profile: measured, Now: now})
}

func writeCompetence(output io.Writer, competence store.CompetenceMap, now time.Time) error {
	if len(competence.Scopes) == 0 {
		_, err := fmt.Fprintln(output, "No competence evidence yet.")
		return err
	}
	groups := []store.CompetenceClass{
		store.CompetenceStrong,
		store.CompetenceFrontier,
		store.CompetenceWeak,
		store.CompetenceStale,
	}
	wroteGroup := false
	for _, class := range groups {
		var rows []store.ScopeCompetence
		for _, scope := range competence.Scopes {
			if scope.Class == class {
				rows = append(rows, scope)
			}
		}
		if len(rows) == 0 {
			continue
		}
		if wroteGroup {
			if _, err := fmt.Fprintln(output); err != nil {
				return err
			}
		}
		wroteGroup = true
		if _, err := fmt.Fprintln(output, class); err != nil {
			return err
		}
		for _, scope := range rows {
			if _, err := fmt.Fprintf(output, "  %s — %s\n", competenceLabel(scope), competenceEvidence(scope, now)); err != nil {
				return err
			}
		}
	}
	return nil
}

func competenceLabel(scope store.ScopeCompetence) string {
	if scope.Kind == store.CompetenceProfile {
		return strings.TrimPrefix(scope.Scope, "profile:") + " work"
	}
	return scope.Scope
}

func competenceEvidence(scope store.ScopeCompetence, now time.Time) string {
	parts := make([]string, 0, 4)
	if scope.Class == store.CompetenceStale {
		parts = append(parts, "last touched "+store.AgeLabel(scope.LastTouched, now))
	} else if scope.Samples == 0 {
		parts = append(parts, "installed, not yet exercised")
	} else {
		failed := int(scope.FailureRate*100 + 0.5)
		parts = append(parts, fmt.Sprintf("%d runs · %d%% failed", scope.Samples, failed))
		if scope.SurpriseTrend != store.SurpriseUnknown {
			parts = append(parts, "surprise "+string(scope.SurpriseTrend))
		}
	}
	if count := len(scope.InstalledSkills); count > 0 {
		parts = append(parts, fmt.Sprintf("%d installed %s", count, pluralWord(count, "skill")))
	}
	return strings.Join(parts, " · ")
}

func pluralWord(count int, word string) string {
	if count == 1 {
		return word
	}
	return word + "s"
}
