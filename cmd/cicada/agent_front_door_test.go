package main

import (
	"regexp"
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

// frontDoorCommandsNamedOnPurpose are the commands a committed front-door
// file may name although the quick start does not: agents, the entry point
// that prints the guide, and song, which the project AGENTS.md names as a
// source of the revision an edit must send.
var frontDoorCommandsNamedOnPurpose = map[string]bool{"agents": true, "song": true}

// TestFrontDoorNamesNoCommandTable extends the agentguide stable-inputs test
// to what only package main holds: no committed front-door file names a
// command, or an error code of apply or render, that the quick start does not
// (apart from frontDoorCommandsNamedOnPurpose), so adding a command or an
// error code never requires regenerating one.
func TestFrontDoorNamesNoCommandTable(t *testing.T) {
	files := frontDoorFiles(t)
	quickstart := string(files["internal/agentguide/quickstart.md"])
	type token struct {
		label   string
		pattern *regexp.Regexp
	}
	var tokens []token
	for _, entries := range [][]commandHelpEntry{commandHelpEntries, extraCommandHelpEntries} {
		for _, entry := range entries {
			if !frontDoorCommandsNamedOnPurpose[entry.name] {
				tokens = append(tokens, token{"the command " + entry.name, regexp.MustCompile(`\bcicada ` + regexp.QuoteMeta(entry.name) + `\b`)})
			}
		}
	}
	for _, code := range append(append([]string{}, applyErrorCodes...), renderErrorCodes...) {
		tokens = append(tokens, token{"the error code " + code, regexp.MustCompile("`" + regexp.QuoteMeta(code) + "`")})
	}
	if len(tokens) < 50 {
		t.Fatalf("only %d command names and error codes to look for", len(tokens))
	}
	for path, data := range files {
		if path == "internal/agentguide/quickstart.md" {
			continue // the quick start itself, which may name anything
		}
		for _, token := range tokens {
			if token.pattern.Match(data) && !token.pattern.MatchString(quickstart) {
				t.Errorf("%s names %s, which the quick start does not; leave it to cicada agents reference", path, token.label)
			}
		}
	}
}
