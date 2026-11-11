package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/internal/agentguide"
)

func TestAgentsCommandPrintsTheQuickStart(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := agentsCommand(nil, &stdout, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("agents: exit %d, stderr %q", code, stderr.String())
	}
	if stdout.String() != agentguide.QuickStart || !strings.HasPrefix(stdout.String(), "# Cicada quick start for agents\n") {
		t.Fatalf("agents does not print the quick start:\n%.200s", stdout.String())
	}
}

func TestAgentsCommandJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := agentsCommand([]string{"--json"}, &stdout, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("agents --json: exit %d, stderr %q", code, stderr.String())
	}
	var document struct {
		Schema     string `json:"schema"`
		Guide      string `json:"guide"`
		GuideBytes int    `json:"guide_bytes"`
		Commands   []struct {
			Command string `json:"command"`
			Summary string `json:"summary"`
		} `json:"commands"`
	}
	decoder := json.NewDecoder(&stdout)
	if err := decoder.Decode(&document); err != nil || decoder.More() {
		t.Fatalf("agents --json is not one document: %v", err)
	}
	if document.Schema != agentsSchema || document.Guide != agentguide.QuickStart || document.GuideBytes != len(agentguide.QuickStart) {
		t.Fatalf("agents --json: schema %q, %d bytes", document.Schema, document.GuideBytes)
	}
	if document.GuideBytes > agentguide.QuickStartBudget {
		t.Fatalf("the quick start is %d bytes; its budget is %d", document.GuideBytes, agentguide.QuickStartBudget)
	}
	if len(document.Commands) != len(agentguide.Lookups) {
		t.Fatalf("agents --json lists %d commands, the guide %d", len(document.Commands), len(agentguide.Lookups))
	}
	for _, command := range document.Commands {
		if !strings.HasPrefix(command.Command, "cicada ") || command.Summary == "" {
			t.Errorf("incomplete command %+v", command)
		}
		// help answers itself; every other lookup is a command with an entry.
		name := strings.Fields(command.Command)[1]
		if _, ok := findCommandHelp(name); !ok && name != "help" {
			t.Errorf("%q names no command", command.Command)
		}
	}
	t.Logf("quick start: %d bytes", document.GuideBytes)
}

// TestAgentsCommandRefusesArguments covers a usage error: exit status 2, and
// with --json one error document.
func TestAgentsCommandRefusesArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := agentsCommand([]string{"reference"}, &stdout, &stderr); code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), agentsUsage) {
		t.Fatalf("agents reference: exit %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := agentsCommand([]string{"--json", "--list"}, &stdout, &stderr); code != 2 {
		t.Fatalf("agents --json --list: exit %d", code)
	}
	var document jsonErrorDocument
	if err := json.Unmarshal(stdout.Bytes(), &document); err != nil || document.Schema != agentsSchema || document.Error.Code != "usage" {
		t.Fatalf("agents --json --list: %v %+v", err, document)
	}
}

func TestIntentsCommandIndex(t *testing.T) {
	var stdout bytes.Buffer
	if err := intentsCommand([]string{"--json", "--index"}, &stdout); err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(stdout.Bytes(), &document); err != nil {
		t.Fatalf("intents --json --index is not JSON: %v", err)
	}
	for _, key := range []string{"intents", "envelope"} {
		if _, ok := document[key]; ok {
			t.Errorf("the index carries %q", key)
		}
	}
	var index intentsIndexDocument
	if err := json.Unmarshal(stdout.Bytes(), &index); err != nil {
		t.Fatal(err)
	}
	kinds := edits.Kinds()
	if index.Schema != intentsSchema || index.EnvelopeVersion != edits.EnvelopeVersion || len(index.Index) != len(kinds) {
		t.Fatalf("index: schema %q, %d kinds; the registry has %d", index.Schema, len(index.Index), len(kinds))
	}
	for i, entry := range index.Index {
		info, _ := edits.DescribeIntent(entry.Kind)
		if entry.Kind != kinds[i] || entry.Meaning == "" || entry.Meaning != info.Meaning {
			t.Errorf("index entry %d: %+v", i, entry)
		}
	}
	var catalog bytes.Buffer
	if err := intentsCommand([]string{"--json"}, &catalog); err != nil {
		t.Fatal(err)
	}
	t.Logf("intents --json --index: %d bytes for %d kinds; the whole catalog is %d bytes", stdout.Len(), len(index.Index), catalog.Len())
	if stdout.Len()*10 > catalog.Len() {
		t.Fatalf("the index is %d bytes, more than a tenth of the catalog's %d", stdout.Len(), catalog.Len())
	}
	stdout.Reset()
	if err := intentsCommand([]string{"--json", "--index", "setstep"}, &stdout); err == nil || !strings.Contains(stdout.String(), `"code": "usage"`) {
		t.Fatalf("--index with a kind: %v\n%s", err, stdout.String())
	}
}
