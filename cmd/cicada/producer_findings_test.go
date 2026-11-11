package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRenderTailOutOfRangeNamesTheFlag is banyan's finding: --tail 10.5s
// failed with "invalid scheduled render options". Tails to 60 s now render;
// a tail past the maximum or below zero fails before rendering with the code
// flag_out_of_range and the flag, the value, the bound and the limit as
// fields, on render and on stems.
func TestRenderTailOutOfRangeNamesTheFlag(t *testing.T) {
	score := filepath.Join("..", "..", "examples", "arrangement-tempo.cicada")
	for _, tc := range []struct {
		command, tail, bound, limit, message string
	}{
		{"render", "61s", "maximum", "60s", "--tail 61s exceeds the 60s maximum; --tail takes 0s to 60s"},
		{"render", "1m0.5s", "maximum", "60s", "--tail 1m0.5s exceeds the 60s maximum; --tail takes 0s to 60s"},
		{"render", "-1s", "minimum", "0s", "--tail -1s is below the 0s minimum; --tail takes 0s to 60s"},
		{"stems", "90s", "maximum", "60s", "--tail 90s exceeds the 60s maximum; --tail takes 0s to 60s"},
	} {
		output := filepath.Join(t.TempDir(), "out.wav")
		var stdout, stderr bytes.Buffer
		status := renderJSONMain(tc.command, []string{score, "-o", output, "--tail", tc.tail, "--json"}, &stdout, &stderr)
		var result renderJSONResult
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			t.Fatalf("%s --tail %s: %v\n%s", tc.command, tc.tail, err, stdout.String())
		}
		failure := result.Error
		if status != 2 || result.OK || failure == nil || failure.Code != "flag_out_of_range" || failure.Flag != "--tail" || failure.Value != tc.tail || failure.Bound != tc.bound || failure.Limit != tc.limit || failure.Message != tc.message {
			t.Fatalf("%s --tail %s: status %d, error %+v", tc.command, tc.tail, status, failure)
		}
		if !strings.Contains(stderr.String(), tc.message) {
			t.Fatalf("%s --tail %s: stderr %q", tc.command, tc.tail, stderr.String())
		}
	}
}

// TestRenderTailAboveTenSecondsRenders renders an arranged score, which takes
// the scheduled renderer, with banyan's 10.5 s tail and the 60 s maximum.
func TestRenderTailAboveTenSecondsRenders(t *testing.T) {
	score := filepath.Join("..", "..", "examples", "arrangement-tempo.cicada")
	for _, tail := range []string{"10.5s", "60s"} {
		output := filepath.Join(t.TempDir(), "out.wav")
		var stdout, stderr bytes.Buffer
		status := renderJSONMain("render", []string{score, "-o", output, "--bars", "1", "--bits", "16", "--tail", tail, "--json"}, &stdout, &stderr)
		var result renderJSONResult
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if status != 0 || !result.OK || len(result.Outputs) != 1 {
			t.Fatalf("--tail %s: status %d %+v; stderr %s", tail, status, result, stderr.String())
		}
		// One bar at 120 BPM is 2 s.
		want := map[string]float64{"10.5s": 12.5, "60s": 62}[tail]
		if got := result.Outputs[0].DurationSeconds; got != want {
			t.Fatalf("--tail %s: %v s, want %v s", tail, got, want)
		}
	}
}

// TestApplyWritesASendAutomationPointInDB lets an agent write the value a
// score now takes: a send automation point in dB, on the path that names the
// reverb by its declared name. A linear send point is written as before.
func TestApplyWritesASendAutomationPointInDB(t *testing.T) {
	isolateState(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "main.cicada")
	source := "cicada 2\ntempo 120\nfx hall reverb {}\ntrack a acid { send hall = -6dB }\npattern p acid { 1 . 5 . }\nscene main { a = p }\nsong { main*4 }\n"
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	intents := filepath.Join(dir, "intents.json")
	envelope := `{"version":1,"intents":[
		{"kind":"setautomationpoint","entity":"param:a.send.hall","at":"@1.1.1","value":"-6dB"},
		{"kind":"setautomationpoint","entity":"param:a.send.hall","at":"@2.1.1","value":"0dB","shape":"smooth"},
		{"kind":"setautomationpoint","entity":"param:a.send.hall","at":"@3.1.1","value":"0.25"}]}`
	if err := os.WriteFile(intents, []byte(envelope), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := captureApply(t, path, intents, "--write"); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"automate a.send.hall {", "@1.1.1 -6dB linear", "@2.1.1 0dB smooth", "@3.1.1 0.25 linear"} {
		if !strings.Contains(string(after), want) {
			t.Fatalf("score lacks %q:\n%s", want, after)
		}
	}
	inspection, err := inspectScore(path)
	if err != nil || hasDiagnosticErrors(inspection.diagnostics) {
		t.Fatalf("the written score does not check: %v %+v", err, inspection.diagnostics)
	}
}

// TestRenderIntoAMissingFolderNamesTheFolder is banyan's, baobab's and
// ebony's finding: render -o into a folder that did not exist failed with
// "open .../.cicada-render-566377017: no such file or directory", a file the
// author never named. It now fails before rendering, naming the folder, with
// the code output_folder_missing, and leaves nothing behind.
func TestRenderIntoAMissingFolderNamesTheFolder(t *testing.T) {
	score := filepath.Join("..", "..", "examples", "arrangement-tempo.cicada")
	missing := filepath.Join(t.TempDir(), "remote-out", "banyan")
	for _, args := range [][]string{
		{score, "-o", filepath.Join(missing, "s2.wav"), "--bars", "1", "--json"},
		{score, "-o", filepath.Join(missing, "s2.wav"), "--bars", "1", "--loudness", "-14", "--json"},
	} {
		var stdout, stderr bytes.Buffer
		status := renderJSONMain("render", args, &stdout, &stderr)
		var result renderJSONResult
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		want := "output folder " + missing + " does not exist; create it (mkdir -p " + missing + "), then render again"
		if status != 1 || result.Error == nil || result.Error.Code != "output_folder_missing" || result.Error.Message != want {
			t.Fatalf("%v: status %d, error %+v", args, status, result.Error)
		}
		if _, err := os.Stat(missing); !os.IsNotExist(err) {
			t.Fatalf("the render created %s", missing)
		}
	}
}
