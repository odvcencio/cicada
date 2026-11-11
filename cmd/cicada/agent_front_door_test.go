package main

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"m31labs.dev/cicada/internal/agentguide"
)

// frontDoorFiles builds the committed files of the agent front door.
func frontDoorFiles(t *testing.T) map[string][]byte {
	t.Helper()
	files, err := agentguide.Build(agentguide.Inputs{Root: repositoryRoot()})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// TestAgentFrontDoorIsCurrent rebuilds the committed files of the agent front
// door (llms.txt, the generated region of AGENTS.md, the quick start the
// binary embeds and the AGENTS.md of the first-song example), whose only
// inputs are the quick start and stable text, and fails while one on disk
// differs. CICADA_UPDATE_AGENT_FRONT_DOOR=1 (make agent-front-door) writes
// them instead.
func TestAgentFrontDoorIsCurrent(t *testing.T) {
	files := frontDoorFiles(t)
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	update := os.Getenv("CICADA_UPDATE_AGENT_FRONT_DOOR") == "1"
	var stale []string
	for _, path := range paths {
		full := filepath.Join(repositoryRoot(), filepath.FromSlash(path))
		current, err := os.ReadFile(full)
		if err == nil && bytes.Equal(current, files[path]) {
			continue
		}
		if update {
			if err := os.WriteFile(full, files[path], 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		stale = append(stale, path)
	}
	if len(stale) > 0 {
		t.Fatalf("stale: %s; run %s", strings.Join(stale, ", "), agentguide.FrontDoorCommand)
	}
}

// TestAgentGuideBudgets keeps each page an agent reads within its byte
// budget (about four bytes a token): the committed files, and the core
// reference as this binary builds it.
func TestAgentGuideBudgets(t *testing.T) {
	files := frontDoorFiles(t)
	reference := agentsReference()
	for _, budget := range []struct {
		name  string
		size  int
		bytes int
	}{
		{"internal/agentguide/quickstart.md", len(files["internal/agentguide/quickstart.md"]), agentguide.QuickStartBudget},
		{"cicada agents reference", len(reference), agentguide.ReferenceBudget},
		{"llms.txt", len(files["llms.txt"]), agentguide.LLMsBudget},
		{"AGENTS.md", len(files["AGENTS.md"]), agentguide.AgentsMDBudget},
	} {
		t.Logf("%s: %d bytes of %d", budget.name, budget.size, budget.bytes)
		if budget.size == 0 || budget.size > budget.bytes {
			t.Errorf("%s is %d bytes; its budget is %d", budget.name, budget.size, budget.bytes)
		}
	}
	for _, part := range []string{"\n## Commands\n", "\n## Edit intents\n", "\n## Diagnostic codes\n", "\n## Effect routes\n", "`cicada agents`"} {
		if !strings.Contains(reference, part) {
			t.Errorf("the reference lacks %q", part)
		}
	}
	if agentguide.QuickStart != string(files["internal/agentguide/quickstart.md"]) {
		t.Errorf("the embedded quick start differs from the page; run %s", agentguide.FrontDoorCommand)
	}
	t.Logf("the AGENTS.md cicada new writes: %d bytes", len(agentguide.ProjectAgents()))
}
