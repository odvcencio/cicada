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

// frontDoorFiles builds the agent front door from the repository and this
// binary's command table.
func frontDoorFiles(t *testing.T) map[string][]byte {
	t.Helper()
	var commands []agentguide.Command
	for _, entries := range [][]commandHelpEntry{commandHelpEntries, extraCommandHelpEntries} {
		for _, entry := range entries {
			commands = append(commands, agentguide.Command{Name: entry.name, Summary: entry.summary})
		}
	}
	files, err := agentguide.Build(agentguide.Inputs{Root: repositoryRoot(), Commands: commands})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// TestAgentFrontDoorIsCurrent rebuilds every generated file of the agent
// front door (the core reference, llms.txt, llms-full.txt, the generated
// region of AGENTS.md, the pages the binary embeds and the AGENTS.md of the
// first-song example) and fails while one on disk differs.
// CICADA_UPDATE_AGENT_FRONT_DOOR=1 writes them instead.
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
// budget (about four bytes a token).
func TestAgentGuideBudgets(t *testing.T) {
	files := frontDoorFiles(t)
	for _, budget := range []struct {
		path  string
		bytes int
	}{
		{"internal/agentguide/quickstart.md", agentguide.QuickStartBudget},
		{"docs/manual/agent-reference.md", agentguide.ReferenceBudget},
		{"llms.txt", agentguide.LLMsBudget},
		{"llms-full.txt", agentguide.LLMsFullBudget},
		{"AGENTS.md", agentguide.AgentsMDBudget},
	} {
		size := len(files[budget.path])
		t.Logf("%s: %d bytes of %d", budget.path, size, budget.bytes)
		if size == 0 || size > budget.bytes {
			t.Errorf("%s is %d bytes; its budget is %d", budget.path, size, budget.bytes)
		}
	}
	// The embedded pages are the generated ones, and cicada new writes the
	// AGENTS.md the example project holds.
	if agentguide.Reference != string(files["docs/manual/agent-reference.md"]) || agentguide.QuickStart != string(files["internal/agentguide/quickstart.md"]) {
		t.Errorf("the embedded pages differ from the generated ones; run %s", agentguide.FrontDoorCommand)
	}
	if agentguide.ProjectAgents() != string(files["examples/first-song/AGENTS.md"]) {
		t.Errorf("cicada new writes another AGENTS.md than examples/first-song holds")
	}
}
