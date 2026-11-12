package main

import (
	"strings"
	"testing"

	"m31labs.dev/cicada/internal/agentguide"
)

// frontDoorFiles builds the committed files of the agent front door.
// internal/agentguide.TestAgentFrontDoorIsCurrent keeps them current.
func frontDoorFiles(t *testing.T) map[string][]byte {
	t.Helper()
	files, err := agentguide.Build(agentguide.Inputs{Root: repositoryRoot()})
	if err != nil {
		t.Fatal(err)
	}
	return files
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
