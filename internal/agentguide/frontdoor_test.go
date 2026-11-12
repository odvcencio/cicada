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

// countOfCatalog matches a count of catalog entries, such as "68 edit intents"
// or "76 diagnostic codes": a number that changes whenever a catalog does.
var countOfCatalog = regexp.MustCompile(`\b\d[\d,]* (commands|edit intents|intents|intent kinds|diagnostic codes|diagnostics|codes|effect routes|effect kinds|routes)\b`)

// TestFrontDoorReadsOnlyStableInputs proves the rule for what the front door
// commits: a committed generated file has only inputs that change rarely, so
// a pull request that changes a manual page, the intent catalog, the
// diagnostics table or the effect routes never has to regenerate one.
//
//  1. A folder that holds only frontDoorInputs builds the same bytes as the
//     repository, so Build reads no other page.
//  2. No committed file holds a diagnostic code, an intent kind, an effect
//     kind or a count of catalog entries, unless the quick start (an input)
//     holds it too, so no catalog of the binary reaches them. Those live in
//     cicada agents reference, which the binary builds when asked.
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

	var tokens []string
	for _, code := range diagnostics.Catalog() {
		tokens = append(tokens, code.Code)
	}
	for _, info := range edits.Catalog() {
		tokens = append(tokens, "`"+info.Kind+"`")
	}
	for _, route := range edits.EffectRoutes() {
		tokens = append(tokens, "`"+route.Kind+"`")
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
			if strings.Contains(text, token) && !strings.Contains(string(quickstart), token) {
				t.Errorf("%s holds %s from a catalog of the binary; leave it to cicada agents reference", path, token)
			}
		}
		for _, count := range countOfCatalog.FindAllString(text, -1) {
			if !strings.Contains(string(quickstart), count) {
				t.Errorf("%s holds the count %q, which changes with a catalog; leave it to cicada agents reference", path, count)
			}
		}
	}
}
