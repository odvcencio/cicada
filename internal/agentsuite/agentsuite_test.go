// Package agentsuite holds the agent task suite: scripted tasks that drive the
// built cicada binary the way an agent does, reading only machine-readable
// output. Run it with make test-agent-surface, which builds build/cicada and
// sets CICADA_BIN.
package agentsuite

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// suiteBudget is the longest the suite may take (plan row E8).
const suiteBudget = 2 * time.Minute

// cicada runs the binary in dir and returns its standard output and its exit
// status. Standard error is kept for failure messages only; the suite never
// reads prose to decide anything.
func cicada(t *testing.T, bin, dir string, args ...string) ([]byte, int) {
	t.Helper()
	command := exec.Command(bin, args...)
	command.Dir = dir
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	code := 0
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("cicada %v: %v", args, err)
	}
	if code != 0 {
		t.Logf("cicada %v exited %d; stderr:\n%s", args, code, stderr.String())
	}
	for _, arg := range args {
		if arg == "--json" {
			checkJSONConventions(t, args, stdout.Bytes())
		}
	}
	return stdout.Bytes(), code
}

var snakeCase = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)

// checkJSONConventions fails on a --json document without a schema or with a
// key that is not snake_case. Two exceptions are deliberate: symbols --json
// is an older format (a list of symbols whose positions use the keys Line and
// Column), left as it is; and an intent payload, such as an example in the
// intents catalog or a fix in check --json, uses the names its intent decodes
// (vocal comps keep camelCase metadata names).
func checkJSONConventions(t *testing.T, args []string, stdout []byte) {
	t.Helper()
	if args[0] == "symbols" {
		return
	}
	var document any
	if err := json.Unmarshal(stdout, &document); err != nil {
		t.Errorf("cicada %v: not JSON: %v", args, err)
		return
	}
	object, ok := document.(map[string]any)
	if schema, _ := object["schema"].(string); !ok || !strings.HasPrefix(schema, "cicada.") {
		t.Errorf("cicada %v: the document has no schema", args)
	}
	var walk func(value any, path string)
	walk = func(value any, path string) {
		switch value := value.(type) {
		case map[string]any:
			for key, inner := range value {
				if !snakeCase.MatchString(key) {
					t.Errorf("cicada %v: key %s.%s is not snake_case", args, path, key)
				}
				if (key == "example" || key == "examples") && strings.HasPrefix(path, "$.intents") || key == "intents" && strings.HasSuffix(path, ".fix") {
					continue
				}
				walk(inner, path+"."+key)
			}
		case []any:
			for _, inner := range value {
				walk(inner, path+"[]")
			}
		}
	}
	walk(document, "$")
}

// decode parses one JSON document into v and fails the test if stdout holds
// anything else.
func decode(t *testing.T, what string, stdout []byte, v any) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(stdout))
	if err := decoder.Decode(v); err != nil {
		t.Fatalf("%s: standard output is not JSON: %v\n%s", what, err, stdout)
	}
	if decoder.More() {
		t.Fatalf("%s: standard output holds more than one JSON document", what)
	}
}

func TestAgentTaskSuite(t *testing.T) {
	bin := os.Getenv("CICADA_BIN")
	if bin == "" {
		if os.Getenv("CICADA_AGENT_SUITE") == "1" {
			t.Fatal("CICADA_AGENT_SUITE=1 needs CICADA_BIN, the built cicada binary")
		}
		t.Skip("set CICADA_BIN to the built binary; make test-agent-surface does")
	}
	bin, err := filepath.Abs(bin)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	// 0. Start as a new agent does: read the quick start from the binary and
	// follow it (quickstart_test.go).
	followQuickStart(t, bin)
	// The MCP server, through the protocol (mcp_test.go).
	driveMCPServer(t, bin)
	dir := t.TempDir()
	source, err := os.ReadFile(filepath.Join("..", "..", "examples", "first-acid.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "song.cicada"), source, 0o644); err != nil {
		t.Fatal(err)
	}

	// 1. Discover the tool from itself.
	var help struct {
		Schema   string `json:"schema"`
		Commands []struct {
			Name  string `json:"name"`
			Flags []struct {
				Name string `json:"name"`
			} `json:"flags"`
		} `json:"commands"`
	}
	stdout, code := cicada(t, bin, dir, "help", "--json")
	decode(t, "help --json", stdout, &help)
	if code != 0 || help.Schema != "cicada.help.v1" {
		t.Fatalf("help --json: exit %d, schema %q", code, help.Schema)
	}
	flags := map[string]map[string]bool{}
	for _, command := range help.Commands {
		flags[command.Name] = map[string]bool{}
		for _, flag := range command.Flags {
			flags[command.Name][flag.Name] = true
		}
	}
	for command, want := range map[string][]string{"check": {"--json"}, "intents": {"--json"}, "apply": {"--json", "--write"}, "render": {"--json", "--loudness", "-o", "--bars"}, "audition": {"--json", "--bars"}, "symbols": {"--json"}} {
		for _, flag := range want {
			if !flags[command][flag] {
				t.Fatalf("help --json: %s lacks %s", command, flag)
			}
		}
	}

	// 2. Learn two intents from the catalog.
	var catalog struct {
		Schema  string `json:"schema"`
		Intents []struct {
			Kind    string          `json:"kind"`
			Meaning string          `json:"meaning"`
			Example json.RawMessage `json:"example"`
			Fields  []struct {
				Name     string `json:"name"`
				Required bool   `json:"required"`
			} `json:"fields"`
		} `json:"intents"`
	}
	stdout, code = cicada(t, bin, dir, "intents", "--json")
	decode(t, "intents --json", stdout, &catalog)
	if code != 0 || catalog.Schema != "cicada.intents.v1" {
		t.Fatalf("intents --json: exit %d, schema %q", code, catalog.Schema)
	}
	examples := map[string]map[string]any{}
	for _, intent := range catalog.Intents {
		var example map[string]any
		if err := json.Unmarshal(intent.Example, &example); err != nil || intent.Meaning == "" {
			t.Fatalf("intents --json: %s has no usable example or meaning", intent.Kind)
		}
		for _, field := range intent.Fields {
			if _, ok := example[field.Name]; field.Required && !ok {
				t.Fatalf("intents --json: the %s example lacks the required field %s", intent.Kind, field.Name)
			}
		}
		examples[intent.Kind] = example
	}

	// 3. Find the names to edit from the score itself.
	var symbols []struct {
		Role string `json:"role"`
		Kind string `json:"kind"`
		Name string `json:"name"`
	}
	stdout, code = cicada(t, bin, dir, "symbols", "--json", "song.cicada")
	decode(t, "symbols --json", stdout, &symbols)
	defined := map[string]bool{}
	for _, symbol := range symbols {
		if symbol.Role == "definition" {
			defined[symbol.Kind+":"+symbol.Name] = true
		}
	}
	if code != 0 || !defined["track:bass"] || !defined["pattern:bass-a"] {
		t.Fatalf("symbols --json: exit %d, definitions %v", code, defined)
	}

	// 4. The score checks clean.
	type checkResult struct {
		Schema string `json:"schema"`
		Valid  bool   `json:"valid"`
		Errors int    `json:"errors"`
	}
	var check checkResult
	stdout, code = cicada(t, bin, dir, "check", "--json", "song.cicada")
	decode(t, "check --json", stdout, &check)
	if code != 0 || check.Schema != "cicada.check.v1" || !check.Valid || check.Errors != 0 {
		t.Fatalf("check --json before the edit: exit %d %+v", code, check)
	}

	// 5. Apply two intents built from the catalog examples.
	setparam, settings := examples["setparam"], examples["setpatternsettings"]
	if setparam == nil || settings == nil {
		t.Fatal("intents --json lacks setparam or setpatternsettings")
	}
	setparam["entity"], setparam["value"] = "param:bass.cutoff", "900Hz"
	settings["entity"] = "pattern:bass-a"
	envelope, err := json.Marshal(map[string]any{"version": 1, "intents": []any{setparam, settings}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "edit.json"), envelope, 0o644); err != nil {
		t.Fatal(err)
	}
	var apply struct {
		Schema         string `json:"schema"`
		OK             bool   `json:"ok"`
		Written        bool   `json:"written"`
		RevisionBefore string `json:"revision_before"`
		RevisionAfter  string `json:"revision_after"`
		Intents        []struct {
			Kind  string `json:"kind"`
			Label string `json:"label"`
		} `json:"intents"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	stdout, code = cicada(t, bin, dir, "apply", "song.cicada", "edit.json", "--write", "--json")
	decode(t, "apply --json", stdout, &apply)
	if code != 0 || apply.Schema != "cicada.apply.v1" || !apply.OK || !apply.Written || apply.RevisionAfter == "" || apply.RevisionAfter == apply.RevisionBefore || len(apply.Intents) != 2 {
		t.Fatalf("apply --json: exit %d %+v", code, apply)
	}
	check = checkResult{}
	stdout, code = cicada(t, bin, dir, "check", "--json", "song.cicada")
	decode(t, "check --json", stdout, &check)
	if code != 0 || !check.Valid {
		t.Fatalf("check --json after the edit: exit %d %+v", code, check)
	}

	// 5b. Change what a track plays, on a copy of the score: the first try is
	// refused with the reason and what it would change, and the resend with
	// the flags the refusal names is written as one step and checks clean.
	changeVoice(t, bin, dir, examples["settrackvoice"])

	// 6. Render to a loudness target and read what was delivered.
	var rendered struct {
		Schema  string `json:"schema"`
		OK      bool   `json:"ok"`
		Bars    int    `json:"bars"`
		Outputs []struct {
			Path       string `json:"path"`
			SampleRate int    `json:"sample_rate"`
			Channels   int    `json:"channels"`
			Frames     int64  `json:"frames"`
		} `json:"outputs"`
		Loudness *struct {
			TargetLUFS      float64  `json:"target_lufs"`
			ToleranceLU     float64  `json:"tolerance_lu"`
			TruePeakMaxDBTP float64  `json:"true_peak_max_dbtp"`
			IntegratedLUFS  *float64 `json:"integrated_lufs"`
			TruePeakDBTP    *float64 `json:"true_peak_dbtp"`
			AppliedGainDB   *float64 `json:"applied_gain_db"`
			TargetMet       bool     `json:"target_met"`
			Passes          int      `json:"passes"`
			Syntheses       int      `json:"syntheses"`
			Replays         int      `json:"replays"`
			Repeats         int      `json:"repeats"`
			PassLog         []struct {
				GainDB         float64  `json:"gain_db"`
				IntegratedLUFS *float64 `json:"integrated_lufs"`
				TruePeakDBTP   *float64 `json:"true_peak_dbtp"`
				Render         string   `json:"render"`
			} `json:"pass_log"`
		} `json:"loudness"`
		Error *struct {
			Reason string `json:"reason"`
		} `json:"error"`
	}
	stdout, code = cicada(t, bin, dir, "render", "song.cicada", "-o", "song.wav", "--bars", "4", "--loudness", "-14", "--json")
	decode(t, "render --json", stdout, &rendered)
	loudness := rendered.Loudness
	if code != 0 || rendered.Schema != "cicada.render.v1" || !rendered.OK || rendered.Bars != 4 || len(rendered.Outputs) != 1 || loudness == nil || !loudness.TargetMet || loudness.IntegratedLUFS == nil || loudness.TruePeakDBTP == nil || loudness.AppliedGainDB == nil {
		t.Fatalf("render --json: exit %d %+v %+v", code, rendered, loudness)
	}
	if math.Abs(*loudness.IntegratedLUFS-loudness.TargetLUFS) > loudness.ToleranceLU || *loudness.TruePeakDBTP > loudness.TruePeakMaxDBTP {
		t.Fatalf("render --json: %.2f LUFS and %.2f dBTP do not meet %.2f LUFS and %.2f dBTP", *loudness.IntegratedLUFS, *loudness.TruePeakDBTP, loudness.TargetLUFS, loudness.TruePeakMaxDBTP)
	}
	if output := rendered.Outputs[0]; output.Path != "song.wav" || output.SampleRate != 48000 || output.Channels != 2 || output.Frames <= 0 {
		t.Fatalf("render --json output: %+v", output)
	}
	// The search synthesizes the song once; later passes replay its mix.
	if loudness.Syntheses != 1 || loudness.Syntheses+loudness.Replays+loudness.Repeats != loudness.Passes || len(loudness.PassLog) != loudness.Passes || loudness.PassLog[0].Render != "synthesized" || loudness.PassLog[0].GainDB != 0 {
		t.Fatalf("render --json passes: %d passes, %d syntheses, %d replays, %d repeats, log %+v", loudness.Passes, loudness.Syntheses, loudness.Replays, loudness.Repeats, loudness.PassLog)
	}

	// 6b. The medium reference song (95 seconds, four tracks) at -14 LUFS:
	// one full synthesis whatever the number of passes (plan row F1). It
	// misses the target at the true-peak ceiling, which is a result: the best
	// pass is still written.
	for _, name := range []string{"lanterns-after-rain.cicada", "cicada.mod"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "examples", "songs", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(dir, "medium"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "medium", name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rendered.Loudness = nil
	stdout, _ = cicada(t, bin, dir, "render", "medium/lanterns-after-rain.cicada", "-o", "medium/song.wav", "--loudness", "-14", "--json")
	decode(t, "render --json of the medium song", stdout, &rendered)
	if loudness = rendered.Loudness; loudness == nil || loudness.Syntheses != 1 || loudness.Passes < 1 || len(rendered.Outputs) != 1 {
		t.Fatalf("render --json of the medium song: %+v %+v", rendered, loudness)
	}

	// 7. Listen to the edited score.
	var audition struct {
		Format string `json:"format"`
		Score  struct {
			Bars int `json:"bars"`
		} `json:"score"`
		Bars   []json.RawMessage `json:"bars"`
		Tracks []struct {
			ID     string `json:"id"`
			Silent bool   `json:"silent"`
		} `json:"tracks"`
	}
	stdout, code = cicada(t, bin, dir, "audition", "song.cicada", "--bars", "4", "--json")
	decode(t, "audition --json", stdout, &audition)
	if code != 0 || audition.Format != "cicada.audition/1" || audition.Score.Bars != 4 || len(audition.Bars) != 4 || len(audition.Tracks) == 0 {
		t.Fatalf("audition --json: exit %d, format %q, %d bars, %d tracks", code, audition.Format, len(audition.Bars), len(audition.Tracks))
	}

	elapsed := time.Since(started)
	t.Logf("agent task suite: 9 commands in %s", elapsed.Round(time.Millisecond))
	if elapsed > suiteBudget {
		t.Fatalf("the suite took %s; its budget is %s", elapsed, suiteBudget)
	}
}

// changeVoice moves the acid bass of a copy of the score to the score's own
// glassbass instrument with settrackvoice, built from the catalog example.
// The acid settings glassbass lacks are refused (voice_params_unsupported)
// and the marks it plays differently, if any, too (voice_changes_sound); each
// refusal names its flag. Sending the flags writes the change, and the score
// checks clean.
func changeVoice(t *testing.T, bin, dir string, example map[string]any) {
	t.Helper()
	if example == nil {
		t.Fatal("intents --json lacks settrackvoice")
	}
	source, err := os.ReadFile(filepath.Join(dir, "song.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "voice.cicada"), source, 0o644); err != nil {
		t.Fatal(err)
	}
	intent := map[string]any{}
	for key, value := range example {
		intent[key] = value
	}
	delete(intent, "dropparams")
	delete(intent, "acceptchanges")
	intent["track"], intent["voice"] = "bass", "glassbass"
	type result struct {
		OK      bool `json:"ok"`
		Written bool `json:"written"`
		Intents []struct {
			Label    string   `json:"label"`
			Warnings []string `json:"warnings"`
		} `json:"intents"`
		Error *struct {
			Code   string   `json:"code"`
			Reason string   `json:"reason"`
			Field  string   `json:"field"`
			Uses   []string `json:"uses"`
		} `json:"error"`
	}
	apply := func() (result, int) {
		envelope, err := json.Marshal(map[string]any{"version": 1, "intents": []any{intent}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "voice.json"), envelope, 0o644); err != nil {
			t.Fatal(err)
		}
		var got result
		stdout, code := cicada(t, bin, dir, "apply", "voice.cicada", "voice.json", "--write", "--json")
		decode(t, "apply --json settrackvoice", stdout, &got)
		return got, code
	}
	flags := map[string]string{"voice_params_unsupported": "dropparams", "voice_changes_sound": "acceptchanges"}
	refusals := 0
	for {
		got, code := apply()
		if code == 0 {
			if !got.OK || !got.Written || len(got.Intents) != 1 || !strings.Contains(got.Intents[0].Label, "glassbass") {
				t.Fatalf("settrackvoice: %+v", got)
			}
			break
		}
		flag := ""
		if got.Error != nil {
			flag = flags[got.Error.Reason]
		}
		if flag == "" || got.Error.Code != "intent_refused" || got.Error.Field != "voice" || len(got.Error.Uses) == 0 || intent[flag] == true {
			t.Fatalf("settrackvoice refusal: exit %d %+v", code, got.Error)
		}
		intent[flag] = true
		refusals++
	}
	if refusals == 0 {
		t.Fatal("settrackvoice moved acid settings to glassbass without asking")
	}
	var check struct {
		Valid bool `json:"valid"`
	}
	stdout, code := cicada(t, bin, dir, "check", "--json", "voice.cicada")
	decode(t, "check --json after settrackvoice", stdout, &check)
	if code != 0 || !check.Valid {
		t.Fatalf("check --json after settrackvoice: exit %d", code)
	}
}
