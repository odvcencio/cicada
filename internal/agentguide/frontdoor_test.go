package agentguide

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/internal/diagnostics"
)

const repositoryRoot = "../.."

func sortedPaths(files map[string][]byte) []string {
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

// TestAgentFrontDoorIsCurrent rebuilds the committed files of the agent front
// door (llms.txt, the generated region of AGENTS.md, the quick start the
// binary embeds and the AGENTS.md of the first-song example), whose only
// inputs are the quick start and stable text, and fails while one on disk
// differs. CICADA_UPDATE_AGENT_FRONT_DOOR=1 (make agent-front-door) writes
// them instead.
func TestAgentFrontDoorIsCurrent(t *testing.T) {
	files, err := Build(Inputs{Root: repositoryRoot})
	if err != nil {
		t.Fatal(err)
	}
	update := os.Getenv("CICADA_UPDATE_AGENT_FRONT_DOOR") == "1"
	var stale []string
	for _, path := range sortedPaths(files) {
		full := filepath.Join(repositoryRoot, filepath.FromSlash(path))
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
		t.Fatalf("stale: %s; run %s", strings.Join(stale, ", "), FrontDoorCommand)
	}
}

// countOfCatalog matches a count of catalog entries, such as "68 edit
// intents", "76 diagnostic codes" or "intents: 75": a number that changes
// whenever a catalog does.
var countOfCatalog = regexp.MustCompile(`\b\d[\d,]* (commands|edit intents|intents|intent kinds|kinds|diagnostic codes|diagnostics|codes|error codes|effect routes|effect kinds|routes|schemas|formats|tools)\b|\b(commands|intents|kinds|codes|diagnostics|routes|tools)\s*[:=(]\s*\d`)

// schemaName matches a JSON format name such as cicada.check.v1.
var schemaName = regexp.MustCompile(`\bcicada\.[a-z_]+\.v\d+\b`)

// TestFrontDoorReadsOnlyStableInputs proves the rule for what the front door
// commits: a committed generated file has only inputs that change rarely, so
// a pull request that changes a manual page, the intent catalog, the
// diagnostics table or the effect routes never has to regenerate one.
//
//  1. A folder that holds only frontDoorInputs builds the same bytes as the
//     repository, so Build reads no other page.
//  2. No committed file holds a diagnostic code, an intent or effect kind (as
//     a whole word), a format name or a count of catalog entries, unless the
//     quick start (an input) holds it too, so no catalog of the binary reaches
//     them. Those live in cicada agents reference, which the binary builds
//     when asked. Command names and the apply and render error codes are in
//     package main; cmd/cicada.TestFrontDoorNamesNoCommandTable checks them.
func TestFrontDoorReadsOnlyStableInputs(t *testing.T) {
	want, err := Build(Inputs{Root: repositoryRoot})
	if err != nil {
		t.Fatal(err)
	}

	isolated := t.TempDir()
	for _, input := range frontDoorInputs {
		data, err := os.ReadFile(filepath.Join(repositoryRoot, filepath.FromSlash(input)))
		if err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(isolated, filepath.FromSlash(input))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Build(Inputs{Root: isolated})
	if err != nil {
		t.Fatalf("Build from frontDoorInputs alone: %v; a new input belongs in frontDoorInputs only if it changes as rarely as the quick start", err)
	}
	if strings.Join(sortedPaths(got), " ") != strings.Join(sortedPaths(want), " ") {
		t.Fatalf("Build from frontDoorInputs alone gives %v, from the repository %v", sortedPaths(got), sortedPaths(want))
	}
	for _, path := range sortedPaths(want) {
		if !bytes.Equal(got[path], want[path]) {
			t.Errorf("%s differs when built from frontDoorInputs alone, so Build reads another file", path)
		}
	}

	// Codes as written; intent and effect kinds as whole words, with or
	// without backticks.
	var tokens []*regexp.Regexp
	for _, code := range diagnostics.Catalog() {
		tokens = append(tokens, regexp.MustCompile(regexp.QuoteMeta(code.Code)+`\b`))
	}
	for _, info := range edits.Catalog() {
		tokens = append(tokens, regexp.MustCompile(`\b`+regexp.QuoteMeta(info.Kind)+`\b`))
	}
	for _, route := range edits.EffectRoutes() {
		tokens = append(tokens, regexp.MustCompile(`\b`+regexp.QuoteMeta(route.Kind)+`\b`))
	}
	if len(tokens) < 100 {
		t.Fatalf("only %d catalog names to look for; the catalogs did not load", len(tokens))
	}
	quickstart, err := os.ReadFile(filepath.Join(repositoryRoot, "docs", "manual", "agent-quickstart.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range sortedPaths(want) {
		if path == "internal/agentguide/quickstart.md" {
			continue // the quick start itself, which may name anything
		}
		text := string(want[path])
		for _, token := range tokens {
			if found := token.FindString(text); found != "" && !token.MatchString(string(quickstart)) {
				t.Errorf("%s holds %s from a catalog of the binary; leave it to cicada agents reference", path, found)
			}
		}
		for _, count := range countOfCatalog.FindAllString(text, -1) {
			if !strings.Contains(string(quickstart), count) {
				t.Errorf("%s holds the count %q, which changes with a catalog; leave it to cicada agents reference", path, count)
			}
		}
		for _, schema := range schemaName.FindAllString(text, -1) {
			if !strings.Contains(string(quickstart), schema) {
				t.Errorf("%s names the format %s, which the quick start does not; leave it to cicada agents reference", path, schema)
			}
		}
	}
}
