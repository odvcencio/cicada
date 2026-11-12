package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/recording"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/render"
	"m31labs.dev/cicada/smf"
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

// TestApplyWritesASendAutomationPointInDB lets an agent write the values a
// score now takes: send automation points in dB and off for silence, on the
// path that names the reverb by its declared name.
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
		{"kind":"setautomationpoint","entity":"param:a.send.hall","at":"@3.1.1","value":"off"}]}`
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
	for _, want := range []string{"automate a.send.hall {", "@1.1.1 -6dB linear", "@2.1.1 0dB smooth", "@3.1.1 off linear"} {
		if !strings.Contains(string(after), want) {
			t.Fatalf("score lacks %q:\n%s", want, after)
		}
	}
	inspection, err := inspectScore(path)
	if err != nil || hasDiagnosticErrors(inspection.diagnostics) {
		t.Fatalf("the written score does not check: %v %+v", err, inspection.diagnostics)
	}
}

// TestEveryOutputCommandCreatesItsFolder is banyan's, baobab's and ebony's
// finding: render -o into a folder that did not exist failed with "open
// .../.cicada-render-566377017: no such file or directory", a file the author
// never named, and midi, convert, bundle and import-midi failed the same way.
// Every command that writes to -o now creates the missing folders, as stems,
// audition and Studio's export do; a folder that cannot be created is named.
func TestEveryOutputCommandCreatesItsFolder(t *testing.T) {
	score := filepath.Join("..", "..", "examples", "arrangement-tempo.cicada")
	inspection, err := inspectScore(score)
	if err != nil {
		t.Fatal(err)
	}
	semantic, err := project.CanonicalJSON(inspection.semantic)
	if err != nil {
		t.Fatal(err)
	}
	inputs := t.TempDir()
	projectJSON := filepath.Join(inputs, "score.json")
	if err := os.WriteFile(projectJSON, semantic, 0o600); err != nil {
		t.Fatal(err)
	}
	midiInput := filepath.Join(inputs, "idea.mid")
	var encoded bytes.Buffer
	if err := smf.Encode(smf.File{PPQ: 960, TempoMicros: 500000, Tracks: []smf.TrackChunk{{Notes: []smf.Note{{Tick: 0, Dur: 176, Note: 60, Vel: 64}, {Tick: 320, Dur: 176, Note: 64, Vel: 100}}}}}, &encoded); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(midiInput, encoded.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	var rate = 16000
	pcm := make([]float32, rate)
	for i := rate / 10; i < rate*8/10; i++ {
		pcm[i] = float32(.2 * math.Sin(2*math.Pi*220*float64(i)/float64(rate)))
	}
	voice := filepath.Join(inputs, "voice.wav")
	if err := os.WriteFile(voice, recording.EncodeWAV(pcm, rate), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		command, file string
		run           func(output string) error
	}{
		{"render", "mix.wav", func(output string) error {
			return renderFile(inspection.score, output, render.Options{SampleRate: 48000, Bits: 16, Bars: 1}, nil)
		}},
		{"render --json", "mix.wav", func(output string) error {
			var stdout bytes.Buffer
			if status := renderJSONMain("render", []string{score, "-o", output, "--bars", "1", "--json"}, &stdout, io.Discard); status != 0 {
				return fmt.Errorf("status %d: %s", status, stdout.String())
			}
			return nil
		}},
		{"render --loudness", "mix.wav", func(output string) error {
			return renderFile(inspection.score, output, render.Options{SampleRate: 48000, Bits: 24, Bars: 1}, &renderTargetOptions{LoudnessTarget: -45, TruePeakMaxDBTP: -1, Tolerance: 0.5, Loudness: true})
		}},
		{"midi", "song.mid", func(output string) error {
			return midiFile(inspection.semantic, midiOptions{output: output})
		}},
		{"convert", "song.json", func(output string) error { return writeNewAtomic(output, semantic) }},
		{"view", "view.html", func(output string) error { return viewCommand([]string{projectJSON, "-o", output}) }},
		{"bundle", "song.zip", func(output string) error { return bundleCommand([]string{score, output}) }},
		{"import-midi", "idea.cicada", func(output string) error { return importMIDICommand([]string{midiInput, "-o", output}) }},
		{"transcribe", "melody.cicada", func(output string) error {
			return transcribeCommand([]string{voice, "--tempo", "120", "--key", "a minor", "-o", output}, io.Discard, io.Discard)
		}},
		{"gen", "bass.cicada", func(output string) error { return generatorCommand([]string{"--seed", "7", "-o", output}) }},
	} {
		output := filepath.Join(t.TempDir(), "remote-out", "banyan", c.file)
		if err := c.run(output); err != nil {
			t.Fatalf("%s -o %s: %v", c.command, output, err)
		}
		if info, err := os.Stat(output); err != nil || info.Size() == 0 {
			t.Fatalf("%s wrote no %s: %v", c.command, output, err)
		}
	}
	// A folder that cannot be created is named, with its own code.
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	status := renderJSONMain("render", []string{score, "-o", filepath.Join(blocked, "out", "mix.wav"), "--bars", "1", "--json"}, &stdout, io.Discard)
	var result renderJSONResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if status != 1 || result.Error == nil || result.Error.Code != "output_not_writable" || !strings.Contains(result.Error.Message, "output folder "+filepath.Join(blocked, "out")+" cannot be created") {
		t.Fatalf("status %d, error %+v", status, result.Error)
	}
	// An agent reads the folder from its own field, not from the message.
	if want := jsonPath(filepath.Join(blocked, "out")); result.Error.Path != want {
		t.Fatalf("error path %q, want %q", result.Error.Path, want)
	}
}

// TestExportProfileTailUpTo60Seconds is the review's blocker: an export
// profile with tail = 45s failed check ("export tail must be 0 to 30s") and
// render --export ("export settings need a compiled project"), because one of
// the two checks still stopped at 30 s. Both now take the render maximum, and
// the rendered file holds the 45 s tail and reports how loud its end is.
func TestExportProfileTailUpTo60Seconds(t *testing.T) {
	dir := t.TempDir()
	source, err := os.ReadFile(filepath.Join("..", "..", "examples", "arrangement-tempo.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	score := filepath.Join(dir, "long.cicada")
	if err := os.WriteFile(score, append(source, []byte("export long { tail = 45s }\nexport over { tail = 61s }\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	inspection, err := inspectScore(score)
	if err != nil {
		t.Fatal(err)
	}
	var refused []string
	for _, d := range inspection.diagnostics {
		if d.Severity == "error" {
			refused = append(refused, d.Code+" "+d.Message)
		}
	}
	if len(refused) != 1 || refused[0] != "CICADA-PARAM export over tail 61s is outside 0 to 60s" {
		t.Fatalf("check: %v", refused)
	}
	if err := os.WriteFile(score, append(source, []byte("export long { tail = 45s }\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "long.wav")
	var stdout, stderr bytes.Buffer
	status := renderJSONMain("render", []string{score, "-o", output, "--bars", "1", "--bits", "16", "--export", "long", "--json"}, &stdout, &stderr)
	var result renderJSONResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if status != 0 || !result.OK || len(result.Outputs) != 1 || result.Outputs[0].DurationSeconds != 47 {
		t.Fatalf("render --export long: status %d %+v; %s", status, result, stderr.String())
	}
	// One bar of a quiet bass, then 45 s: the last 100 ms hold only the
	// 16-bit dither, about one step (-90 dBFS).
	if peak := result.Outputs[0].TailEndPeakDBFS; peak == nil || *peak > -85 {
		t.Fatalf("tail end peak %v, want the dither floor", peak)
	}
}

// TestTailEndPeakShowsATailStillSounding renders with no tail, so the file
// ends while the notes sound: tail_end_peak_dbfs reports their level.
func TestTailEndPeakShowsATailStillSounding(t *testing.T) {
	output := filepath.Join(t.TempDir(), "cut.wav")
	var stdout bytes.Buffer
	renderJSONMain("render", []string{filepath.Join("..", "..", "examples", "first-acid.cicada"), "-o", output, "--bars", "1", "--tail", "0s", "--json"}, &stdout, io.Discard)
	var result renderJSONResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Outputs) != 1 || result.Outputs[0].TailEndPeakDBFS == nil || *result.Outputs[0].TailEndPeakDBFS < -60 {
		t.Fatalf("outputs %+v", result.Outputs)
	}
}

// TestVerifyWAVTailOutOfRangeNamesTheFlag: verify-wav refused --tail 61s
// with other words than render and printed usage for --tail -1s. It now gives
// render's refusal, exit status 2, and with --report the same structured
// error. The command exits, so it runs in a child process.
func TestVerifyWAVTailOutOfRangeNamesTheFlag(t *testing.T) {
	if args := os.Getenv("CICADA_VERIFY_TAIL_ARGS"); args != "" {
		verifyWAVCommand(strings.Fields(args))
		return
	}
	for tail, message := range map[string]string{
		"61s": "--tail 61s exceeds the 60s maximum; --tail takes 0s to 60s",
		"-1s": "--tail -1s is below the 0s minimum; --tail takes 0s to 60s",
	} {
		child := exec.Command(os.Args[0], "-test.run=^TestVerifyWAVTailOutOfRangeNamesTheFlag$")
		child.Env = append(os.Environ(), "CICADA_VERIFY_TAIL_ARGS=missing.wav --bars 1 --tail "+tail+" --report")
		var stderr bytes.Buffer
		child.Stderr = &stderr
		err := child.Run()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 2 || !strings.Contains(stderr.String(), message) || !strings.Contains(stderr.String(), `"code": "flag_out_of_range"`) {
			t.Fatalf("--tail %s: %v\n%s", tail, err, stderr.String())
		}
	}
}
