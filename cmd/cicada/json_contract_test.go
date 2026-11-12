package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
)

const contractScore = "cicada 2\ntempo 120\ntrack bass acid {}\npattern riff { a1 . c2 . }\nscene main { bass = riff }\nsong { main*2 }\n"

// oneJSONDocument requires stdout to hold exactly one JSON object with a
// schema, and returns it.
func oneJSONDocument(t *testing.T, what string, stdout []byte) map[string]any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(stdout))
	var document map[string]any
	if err := decoder.Decode(&document); err != nil {
		t.Fatalf("%s: standard output is not one JSON object: %v\n%q", what, err, stdout)
	}
	if decoder.More() {
		t.Fatalf("%s: standard output holds more than one document", what)
	}
	if schema, _ := document["schema"].(string); !strings.HasPrefix(schema, "cicada.") {
		t.Fatalf("%s: no schema in %v", what, document)
	}
	return document
}

func errorCode(document map[string]any) string {
	failure, _ := document["error"].(map[string]any)
	code, _ := failure["code"].(string)
	if message, _ := failure["message"].(string); message == "" {
		return ""
	}
	return code
}

// resolvedTempDir is a temporary folder named without symbolic links, so the
// working folder and the paths in it compare as written.
func resolvedTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeContractFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestJSONFailuresPrintOneDocument runs every --json command into a failure,
// usage errors included, and requires one document with an error code and
// the text command's exit status.
func TestJSONFailuresPrintOneDocument(t *testing.T) {
	dir := resolvedTempDir(t)
	t.Chdir(dir)
	writeContractFile(t, filepath.Join(dir, "song.cicada"), contractScore)
	writeContractFile(t, filepath.Join(dir, "bad.cicada"), "cicada 2\ntempo 120\ntrack bass acid { cutoff = 9 }\npattern riff { a1 . }\nscene main { bass = riff }\nsong { main }\n")
	if err := os.Mkdir(filepath.Join(dir, "stems"), 0o755); err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		stdout []byte
		status int
	}
	exit := func(err error) int {
		if err != nil {
			return 1
		}
		return 0
	}
	run := map[string]func() outcome{
		"check usage": func() outcome {
			var stdout bytes.Buffer
			status := exit(checkCommand([]string{"--json", "--bogus"}, &stdout, &bytes.Buffer{}))
			return outcome{stdout.Bytes(), status}
		},
		"intents unknown kind": func() outcome {
			var stdout bytes.Buffer
			status := exit(intentsCommand([]string{"--json", "nosuch"}, &stdout))
			return outcome{stdout.Bytes(), status}
		},
		"intents usage": func() outcome {
			var stdout bytes.Buffer
			status := exit(intentsCommand([]string{"--json", "--bogus"}, &stdout))
			return outcome{stdout.Bytes(), status}
		},
		"diagnostics unknown code": func() outcome {
			var stdout bytes.Buffer
			status := exit(diagnosticsCommand([]string{"--json", "NOPE"}, &stdout))
			return outcome{stdout.Bytes(), status}
		},
		"help unknown command": func() outcome {
			var stdout bytes.Buffer
			_, code := handleCLIHelp([]string{"help", "--json", "bogus"}, &stdout, &bytes.Buffer{})
			status := code
			return outcome{stdout.Bytes(), status}
		},
		"render without a score": func() outcome {
			var stdout bytes.Buffer
			status := renderJSONMain("render", []string{"--json"}, &stdout, &bytes.Buffer{})
			return outcome{stdout.Bytes(), status}
		},
		"render without -o": func() outcome {
			var stdout bytes.Buffer
			status := renderJSONMain("render", []string{"song.cicada", "--json"}, &stdout, &bytes.Buffer{})
			return outcome{stdout.Bytes(), status}
		},
		"stems with a loudness target": func() outcome {
			var stdout bytes.Buffer
			status := renderJSONMain("stems", []string{"song.cicada", "-o", "new-stems", "--loudness", "-14", "--json"}, &stdout, &bytes.Buffer{})
			return outcome{stdout.Bytes(), status}
		},
		"stems into an existing folder": func() outcome {
			var stdout bytes.Buffer
			status := renderJSONMain("stems", []string{"song.cicada", "-o", "stems", "--json"}, &stdout, &bytes.Buffer{})
			return outcome{stdout.Bytes(), status}
		},
		"audition of an invalid score": func() outcome {
			var stdout bytes.Buffer
			status := exit(auditionCommand([]string{"bad.cicada", "--json"}, &stdout, &bytes.Buffer{}))
			return outcome{stdout.Bytes(), status}
		},
		"audition usage": func() outcome {
			var stdout bytes.Buffer
			status := exit(auditionCommand([]string{"--json"}, &stdout, &bytes.Buffer{}))
			return outcome{stdout.Bytes(), status}
		},
		"apply usage": func() outcome {
			var stdout bytes.Buffer
			status := exit(applyCommandWithHooks([]string{"--json"}, &stdout, nil))
			return outcome{stdout.Bytes(), status}
		},
		"params with no match": func() outcome {
			var stdout bytes.Buffer
			status := exit(paramsCommand([]string{"--json", "nosuch"}, &stdout))
			return outcome{stdout.Bytes(), status}
		},
		"params usage": func() outcome {
			var stdout bytes.Buffer
			status := exit(paramsCommand([]string{"--json", "--bogus"}, &stdout))
			return outcome{stdout.Bytes(), status}
		},
		"params with an unknown scope": func() outcome {
			var stdout bytes.Buffer
			status := exit(paramsCommand([]string{"--json", "--scope", "nosuch"}, &stdout))
			return outcome{stdout.Bytes(), status}
		},
		"params with a voice that is an effect": func() outcome {
			var stdout bytes.Buffer
			status := exit(paramsCommand([]string{"--json", "--voice", "delay"}, &stdout))
			return outcome{stdout.Bytes(), status}
		},
		"explain an unknown name": func() outcome {
			var stdout bytes.Buffer
			status := exit(explainCommand([]string{"nosuch", "--json"}, &stdout))
			return outcome{stdout.Bytes(), status}
		},
		"explain an unknown track": func() outcome {
			var stdout bytes.Buffer
			status := exit(explainCommand([]string{"track:nosuch", "song.cicada", "--json"}, &stdout))
			return outcome{stdout.Bytes(), status}
		},
		"explain a track of a missing score": func() outcome {
			var stdout bytes.Buffer
			status := exit(explainCommand([]string{"track:bass", "missing.cicada", "--json"}, &stdout))
			return outcome{stdout.Bytes(), status}
		},
		"explain usage": func() outcome {
			var stdout bytes.Buffer
			status := exit(explainCommand([]string{"--json"}, &stdout))
			return outcome{stdout.Bytes(), status}
		},
		"explain probe of a mixer control": func() outcome {
			var stdout bytes.Buffer
			status := exit(explainCommand([]string{"mix.pan", "--probe", "--json"}, &stdout))
			return outcome{stdout.Bytes(), status}
		},
		"agents usage": func() outcome {
			var stdout bytes.Buffer
			status := agentsCommand([]string{"--json", "--bogus"}, &stdout, &bytes.Buffer{})
			return outcome{stdout.Bytes(), status}
		},
		"intents index with a kind": func() outcome {
			var stdout bytes.Buffer
			status := exit(intentsCommand([]string{"--json", "--index", "setstep"}, &stdout))
			return outcome{stdout.Bytes(), status}
		},
	}
	want := map[string]struct {
		code   string
		status int
	}{
		"check usage":                           {"usage", 1},
		"intents unknown kind":                  {"unknown_kind", 1},
		"intents usage":                         {"usage", 1},
		"diagnostics unknown code":              {"unknown_code", 1},
		"help unknown command":                  {"unknown_command", 2},
		"render without a score":                {"usage", 2},
		"render without -o":                     {"usage", 2},
		"stems with a loudness target":          {"usage", 2},
		"stems into an existing folder":         {"output_exists", 1},
		"audition of an invalid score":          {"invalid_score", 1},
		"audition usage":                        {"usage", 1},
		"apply usage":                           {"usage", 1},
		"params with no match":                  {"no_match", 1},
		"params usage":                          {"usage", 1},
		"params with an unknown scope":          {"usage", 1},
		"params with a voice that is an effect": {"no_match", 1},
		"explain an unknown name":               {"unknown_thing", 1},
		"explain an unknown track":              {"unknown_track", 1},
		"explain a track of a missing score":    {"unreadable_score", 1},
		"explain usage":                         {"usage", 1},
		"explain probe of a mixer control":      {"not_probeable", 1},
		"agents usage":                          {"usage", 2},
		"intents index with a kind":             {"usage", 1},
	}
	for name, command := range run {
		got := command()
		document := oneJSONDocument(t, name, got.stdout)
		if code := errorCode(document); code != want[name].code || got.status != want[name].status {
			t.Errorf("%s: error code %q and exit status %d, want %q and %d: %v", name, code, got.status, want[name].code, want[name].status, document)
		}
	}
}

// TestCheckJSONProjectFailures checks a project whose cicada.mod does not
// parse: without a score the document names the manifest error, as checking
// the score itself does.
func TestCheckJSONProjectFailures(t *testing.T) {
	dir := resolvedTempDir(t)
	t.Chdir(dir)
	writeContractFile(t, filepath.Join(dir, "cicada.mod"), "bogus directive\n")
	writeContractFile(t, filepath.Join(dir, "song.cicada"), contractScore)
	for _, args := range [][]string{{"--json"}, {"--json", "song.cicada"}} {
		var stdout bytes.Buffer
		err := checkCommand(args, &stdout, &bytes.Buffer{})
		document := oneJSONDocument(t, strings.Join(args, " "), stdout.Bytes())
		data, _ := json.Marshal(document)
		if err == nil || document["valid"] != false || !strings.Contains(string(data), `"code":"CICADA-MANIFEST"`) {
			t.Errorf("check %v: %v\n%s", args, err, data)
		}
	}
	empty := resolvedTempDir(t)
	t.Chdir(empty)
	var stdout bytes.Buffer
	if err := checkCommand([]string{"--json"}, &stdout, &bytes.Buffer{}); err == nil || errorCode(oneJSONDocument(t, "empty folder", stdout.Bytes())) != "no_scores" {
		t.Fatalf("an empty folder: %v %s", err, stdout.String())
	}
}

// TestJSONPathsFollowOneRule passes absolute paths to each command: a path
// inside the working folder comes back relative with forward slashes, a path
// outside comes back absolute.
func TestJSONPathsFollowOneRule(t *testing.T) {
	dir, other := resolvedTempDir(t), resolvedTempDir(t)
	t.Chdir(dir)
	inside := filepath.Join(dir, "sub", "song.cicada")
	outside := filepath.Join(other, "song.cicada")
	writeContractFile(t, inside, contractScore)
	writeContractFile(t, outside, contractScore)
	envelope := filepath.Join(other, "edit.json")
	writeContractFile(t, envelope, `{"version":1,"intents":[{"kind":"setparam","entity":"param:bass.cutoff","value":"900Hz"}]}`)
	for _, tc := range []struct {
		score, output, want, wantOutput string
	}{
		{inside, filepath.Join(dir, "sub", "out.wav"), "sub/song.cicada", "sub/out.wav"},
		{outside, filepath.Join(other, "out.wav"), outside, filepath.Join(other, "out.wav")},
	} {
		var stdout bytes.Buffer
		checkCommand([]string{"--json", tc.score}, &stdout, &bytes.Buffer{})
		scores, _ := oneJSONDocument(t, "check", stdout.Bytes())["scores"].([]any)
		if len(scores) != 1 || scores[0].(map[string]any)["file"] != tc.want {
			t.Errorf("check %s: %v", tc.score, scores)
		}
		stdout.Reset()
		renderJSONMain("render", []string{tc.score, "-o", tc.output, "--bars", "1", "--tail", "0s", "--json"}, &stdout, &bytes.Buffer{})
		rendered := oneJSONDocument(t, "render", stdout.Bytes())
		outputs, _ := rendered["outputs"].([]any)
		if rendered["score"] != tc.want || len(outputs) != 1 || outputs[0].(map[string]any)["path"] != tc.wantOutput {
			t.Errorf("render %s: %v", tc.score, rendered)
		}
		stdout.Reset()
		applyCommandWithHooks([]string{tc.score, envelope, "--json"}, &stdout, nil)
		if applied := oneJSONDocument(t, "apply", stdout.Bytes()); applied["score"] != tc.want {
			t.Errorf("apply %s: %v", tc.score, applied)
		}
	}
	var stdout bytes.Buffer
	renderJSONMain("stems", []string{inside, "-o", filepath.Join(dir, "sub", "stems"), "--bars", "1", "--tail", "0s", "--json"}, &stdout, &bytes.Buffer{})
	outputs, _ := oneJSONDocument(t, "stems", stdout.Bytes())["outputs"].([]any)
	if len(outputs) == 0 || !strings.HasPrefix(outputs[0].(map[string]any)["path"].(string), "sub/stems/") {
		t.Errorf("stems: %v", outputs)
	}
}

// TestCheckJSONProvesEachFixAlone checks a score with two off-step
// placements: each gets its own fix, for the score's revision, although the
// score keeps the other error after either fix.
func TestCheckJSONProvesEachFixAlone(t *testing.T) {
	dir := resolvedTempDir(t)
	path := filepath.Join(dir, "snap.cicada")
	source := "cicada 2\ntempo 120\ntrack bass acid {}\ntrack lead acid {}\npattern pulse { a1 . . . e2 . . . }\narrange {\n  place one bass pulse { at = 100ticks length = 1bars }\n  place two lead pulse { at = 130ticks length = 1bars }\n}\n"
	writeContractFile(t, path, source)
	document, err := runCheckJSON(t, path)
	if err == nil || len(document.Scores[0].Diagnostics) != 2 {
		t.Fatalf("want two errors: %v %+v", err, document)
	}
	for _, diagnostic := range document.Scores[0].Diagnostics {
		if diagnostic.Fix == nil || len(diagnostic.Fix.Intents) != 1 || diagnostic.Fix.Revision != edits.Revision([]byte(source)) {
			t.Fatalf("a fix is missing or lacks the revision: %+v", diagnostic)
		}
	}
	// cicada apply refuses a batch that leaves the score invalid, so the fixes
	// for one revision go in one batch.
	first, second := document.Scores[0].Diagnostics[0].Fix, document.Scores[0].Diagnostics[1].Fix
	envelope := filepath.Join(dir, "fix.json")
	writeContractFile(t, envelope, `{"version":1,"revision":"`+first.Revision+`","intents":[`+string(first.Intents[0])+`,`+string(second.Intents[0])+`]}`)
	if err := applyCommandWithHooks([]string{path, envelope, "--write"}, &bytes.Buffer{}, nil); err != nil {
		t.Fatalf("the fixes do not apply at their revision: %v", err)
	}
	if document, err := runCheckJSON(t, path); err != nil || !document.Valid {
		t.Fatalf("after both fixes: %v %+v", err, document.Scores[0].Diagnostics)
	}
}

// TestCheckJSONOffersCicadaFixOnlyWhenItRepairs offers cicada fix for a
// loose score with edition-1 spellings, where running it repairs the score,
// and not where the fix command would refuse: two such scores share a folder.
func TestCheckJSONOffersCicadaFixOnlyWhenItRepairs(t *testing.T) {
	legacy := "cicada 2\ntempo 120\nfx delay {}\ntrack bass acid { send_a = 0.5 }\npattern p { a1 . }\nscene main { bass = p }\nsong { main }\n"
	dir := resolvedTempDir(t)
	path := filepath.Join(dir, "legacy.cicada")
	writeContractFile(t, path, legacy)
	document, _ := runCheckJSON(t, path)
	fixes := 0
	for _, diagnostic := range document.Scores[0].Diagnostics {
		if diagnostic.Code == "CICADA-VERSION" && diagnostic.Fix != nil && len(diagnostic.Fix.Command) == 3 && diagnostic.Fix.Command[1] == "fix" {
			fixes++
		}
	}
	if fixes != 2 {
		t.Fatalf("want cicada fix offered for both legacy spellings: %+v", document.Scores[0].Diagnostics)
	}
	if err := fixCommand([]string{path}); err != nil {
		t.Fatal(err)
	}
	if document, err := runCheckJSON(t, path); err != nil || !document.Valid {
		t.Fatalf("cicada fix did not repair the score: %v %+v", err, document)
	}

	shared := resolvedTempDir(t)
	writeContractFile(t, filepath.Join(shared, "one.cicada"), legacy)
	writeContractFile(t, filepath.Join(shared, "two.cicada"), legacy)
	document, _ = runCheckJSON(t, filepath.Join(shared, "one.cicada"))
	for _, diagnostic := range document.Scores[0].Diagnostics {
		if diagnostic.Fix != nil {
			t.Fatalf("cicada fix one.cicada refuses while two.cicada is also edition 1, yet it is offered: %+v", diagnostic)
		}
	}
}

// documentedCodes reads the codes listed between <!-- NAME --> and
// <!-- /NAME --> in docs/manual/agents.md, one `code` per list item.
func documentedCodes(t *testing.T, name string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repositoryRoot(), "docs", "manual", "agents.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	start, end := strings.Index(text, "<!-- "+name+" -->"), strings.Index(text, "<!-- /"+name+" -->")
	if start < 0 || end < start {
		t.Fatalf("agents.md lacks the %s list", name)
	}
	var codes []string
	for _, match := range regexp.MustCompile("(?m)^- `([a-z_]+)`").FindAllStringSubmatch(text[start:end], -1) {
		codes = append(codes, match[1])
	}
	return codes
}

// TestQuickStartListsTheApplyErrorCodes keeps the apply error codes the
// agent quick start names, between its apply-error-codes markers, equal to the
// codes apply sets.
func TestQuickStartListsTheApplyErrorCodes(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repositoryRoot(), "docs", "manual", "agent-quickstart.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, list, found := strings.Cut(string(data), "<!-- apply-error-codes -->")
	list, _, closed := strings.Cut(list, "<!-- /apply-error-codes -->")
	if !found || !closed {
		t.Fatal("agent-quickstart.md lacks the apply-error-codes list")
	}
	var named []string
	for _, match := range regexp.MustCompile("`([a-z_]+)`").FindAllStringSubmatch(list, -1) {
		named = append(named, match[1])
	}
	want := append([]string(nil), applyErrorCodes...)
	sort.Strings(named)
	sort.Strings(want)
	if strings.Join(named, " ") != strings.Join(want, " ") {
		t.Fatalf("the quick start names the apply codes %v; apply sets %v", named, want)
	}
}

// TestErrorCodesAreDocumented keeps the render and apply error codes equal
// to the lists in docs/manual/agents.md, and every code the source sets in
// those lists.
func TestErrorCodesAreDocumented(t *testing.T) {
	for _, tc := range []struct {
		list    string
		codes   []string
		files   []string
		pattern string
	}{
		{"render-error-codes", renderErrorCodes, []string{"render_json.go", "main.go"}, `(?:reason: |reason = |Code: )"([a-z_]+)"`},
		{"apply-error-codes", applyErrorCodes, []string{"apply.go"}, `(?:fail\(|stage: |Code: )"([a-z_]+)"`},
	} {
		documented := documentedCodes(t, tc.list)
		sorted := append([]string(nil), tc.codes...)
		sort.Strings(sorted)
		sort.Strings(documented)
		if strings.Join(sorted, " ") != strings.Join(documented, " ") {
			t.Errorf("%s: the code has %v, agents.md lists %v", tc.list, sorted, documented)
		}
		for _, file := range tc.files {
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			for _, match := range regexp.MustCompile(tc.pattern).FindAllStringSubmatch(string(data), -1) {
				if !containsName(tc.codes, match[1]) {
					t.Errorf("%s sets the code %q, which %s does not list", file, match[1], tc.list)
				}
			}
		}
	}
}

// TestDocumentedExamplesDecode decodes every output example in
// docs/manual/agents.md into the Go type of its format, with a decoder that
// ignores unknown fields as a client should, and requires a known schema.
// A shown value of the wrong type, or an unknown schema, fails.
func TestDocumentedExamplesDecode(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repositoryRoot(), "docs", "manual", "agents.md"))
	if err != nil {
		t.Fatal(err)
	}
	formats := map[string]func() any{
		agentsSchema:       func() any { return &agentsDocument{} },
		songSchema:         func() any { return &songDocument{} },
		intentsIndexSchema: func() any { return &intentsIndexDocument{} },
		helpSchema:         func() any { return &helpDocument{} },
		intentsSchema:      func() any { return &intentsDocument{} },
		diagnosticsSchema:  func() any { return &diagnosticsDocument{} },
		checkSchema:        func() any { return &checkDocument{} },
		applySchema:        func() any { return &applyDocument{} },
		renderSchema:       func() any { return &renderDocument{} },
		paramsSchema:       func() any { return &paramsDocument{} },
		paramsIndexSchema:  func() any { return &paramsIndexDocument{} },
	}
	// cicada.explain.v1 has one shape for each kind of thing explained.
	explainKinds := map[string]func() any{
		"control": func() any { return &explainControlDocument{} },
		"owner":   func() any { return &explainOwnerDocument{} },
		"track":   func() any { return &explainTrackDocument{} },
	}
	fence := strings.Repeat("`", 3)
	lines := strings.Split(string(data), "\n")
	decoded := 0
	for i := 0; i < len(lines); i++ {
		if !strings.HasPrefix(strings.TrimSpace(lines[i]), "<!-- agent-example: ") {
			continue
		}
		start := i + 1
		for start < len(lines) && strings.TrimSpace(lines[start]) != fence+"json" {
			start++
		}
		end := start + 1
		for end < len(lines) && strings.TrimSpace(lines[end]) != fence {
			end++
		}
		body := []byte(strings.Join(lines[start+1:end], "\n"))
		var header struct {
			Schema string `json:"schema"`
			Kind   string `json:"kind"`
		}
		if err := json.Unmarshal(body, &header); err != nil {
			t.Fatalf("agents.md:%d: %v", i+1, err)
		}
		make, ok := formats[header.Schema]
		if header.Schema == explainSchema {
			make, ok = explainKinds[header.Kind]
		}
		if !ok {
			t.Errorf("agents.md:%d: unknown schema %q", i+1, header.Schema)
			continue
		}
		document := make()
		strict := json.NewDecoder(bytes.NewReader(body))
		strict.DisallowUnknownFields()
		if err := strict.Decode(document); err != nil {
			t.Errorf("agents.md:%d: the %s example does not decode as that format: %v", i+1, header.Schema, err)
		}
		var shown map[string]json.RawMessage
		if err := json.Unmarshal(body, &shown); err != nil {
			t.Fatal(err)
		}
		for _, key := range requiredJSONKeys(document) {
			if _, ok := shown[key]; !ok {
				t.Errorf("agents.md:%d: the %s example leaves out %q, which every %s document has", i+1, header.Schema, key, header.Schema)
			}
		}
		decoded++
		i = end
	}
	if decoded < 10 {
		t.Fatalf("decoded only %d examples", decoded)
	}
}

// requiredJSONKeys lists the top-level keys every document of v's type
// carries: the JSON names of its fields without omitempty. An example may
// shorten what is inside them, but not leave one out, so a document that
// lost a key under an unchanged schema cannot match its example.
func requiredJSONKeys(v any) []string {
	typ := reflect.TypeOf(v)
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	var keys []string
	for i := 0; i < typ.NumField(); i++ {
		name, options, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		if name == "" || name == "-" || strings.Contains(","+options+",", ",omitempty,") {
			continue
		}
		keys = append(keys, name)
	}
	return keys
}
