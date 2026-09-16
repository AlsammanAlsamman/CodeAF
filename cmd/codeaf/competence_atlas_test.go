package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Agent-Field/codeaf/internal/store"
)

func sampleAtlasCompetence() store.CompetenceMap {
	return store.CompetenceMap{Scopes: []store.ScopeCompetence{
		{Scope: "tool:go", Kind: store.CompetenceTerritory, Class: store.CompetenceStrong,
			Samples: 12, SuccessRate: .9, InstalledSkills: []string{"go-parser-fix"}},
		{Scope: "repo:/workspace/app", Kind: store.CompetenceTerritory, Class: store.CompetenceFrontier,
			Samples: 5, SuccessRate: .5, InstalledSkills: []string{"repo-audit"}},
		{Scope: "file:/workspace/app/main.go", Kind: store.CompetenceTerritory, Class: store.CompetenceWeak,
			Samples: 4, SuccessRate: .1},
		{Scope: "profile:atomic", Kind: store.CompetenceProfile, Class: store.CompetenceStrong,
			Samples: 8, SuccessRate: .95},
	}}
}

func TestRenderCompetenceAtlasEmbedsNodesEdgesAndClassColors(t *testing.T) {
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	page := renderCompetenceAtlas(sampleAtlasCompetence(), now)

	if !strings.HasPrefix(strings.TrimSpace(page), "<!doctype html>") {
		t.Fatalf("atlas page missing doctype: %q", page[:40])
	}
	for _, want := range []string{
		`"id":"tool:go"`, `"id":"repo:/workspace/app"`,
		`"id":"file:/workspace/app/main.go"`, `"id":"profile:atomic"`,
		"go-parser-fix", "repo-audit",
		"#A3BE8C", "#EBCB8B", "#C67173", "#6B7280", // strong, frontier, weak, stale
	} {
		if !strings.Contains(page, want) {
			t.Errorf("atlas page missing %q", want)
		}
	}

	start := strings.Index(page, "var data = ")
	if start == -1 {
		t.Fatal("atlas page has no embedded data assignment")
	}
	start += len("var data = ")
	end := strings.Index(page[start:], ";\n")
	if end == -1 {
		t.Fatal("atlas page's embedded data assignment has no terminator")
	}
	var data struct {
		Nodes []struct {
			ID string `json:"id"`
		} `json:"nodes"`
		Edges []struct {
			Source string `json:"source"`
			Target string `json:"target"`
		} `json:"edges"`
	}
	if err := json.Unmarshal([]byte(page[start:start+end]), &data); err != nil {
		t.Fatalf("embedded atlas data is not valid JSON: %v", err)
	}
	if len(data.Nodes) != 4 {
		t.Fatalf("atlas nodes = %d, want 4", len(data.Nodes))
	}
	// repo:/workspace/app touches its own file, and both touch nothing else
	// (tool:go and profile:atomic share no path/tool relation with them).
	foundRepoFileEdge := false
	for _, edge := range data.Edges {
		if (edge.Source == "repo:/workspace/app" && edge.Target == "file:/workspace/app/main.go") ||
			(edge.Target == "repo:/workspace/app" && edge.Source == "file:/workspace/app/main.go") {
			foundRepoFileEdge = true
		}
		if edge.Source == "profile:atomic" || edge.Target == "profile:atomic" {
			t.Fatalf("profile scope must never be edged: %+v", edge)
		}
	}
	if !foundRepoFileEdge {
		t.Fatalf("expected an edge between a repo scope and its own file scope, edges = %+v", data.Edges)
	}
}

func TestRenderCompetenceAtlasEscapesUntrustedText(t *testing.T) {
	competence := store.CompetenceMap{Scopes: []store.ScopeCompetence{
		{Scope: `repo:</script><script>alert(1)</script>`, Kind: store.CompetenceTerritory,
			Class: store.CompetenceStrong, Samples: 1, InstalledSkills: []string{"</script>evil"}},
	}}
	page := renderCompetenceAtlas(competence, time.Now())
	if strings.Contains(page, "<script>alert(1)</script>") {
		t.Fatal("atlas page embeds an unescaped </script> break-out from scope/skill text")
	}
}

func TestWriteCompetenceAtlasWritesFileAndReportsPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "atlas.html")
	var output bytes.Buffer
	if err := writeCompetenceAtlas(&output, sampleAtlasCompetence(), time.Now(), path); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), path) {
		t.Fatalf("writeCompetenceAtlas output = %q, want it to name %q", output.String(), path)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), "skill atlas") {
		t.Fatalf("written atlas file missing expected content")
	}
}
