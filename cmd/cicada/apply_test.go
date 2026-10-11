package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/edit/editlog"
)

func applyFixture(t *testing.T) (string, string, []byte) {
	t.Helper()
	t.Chdir(t.TempDir())
	if err := newProject("score", os.WriteFile); err != nil {
		t.Fatal(err)
	}
	path, err := filepath.Abs("score/main.cicada")
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	intents := filepath.Join(filepath.Dir(path), "intents.json")
	writeApplyEnvelope(t, intents, edits.Envelope{Version: 1, Intents: []edits.Intent{&edits.SetPatternSettings{Entity: "pattern:pulse", Swing100: 5500, Gate: 55}}})
	return path, intents, before
}

func writeApplyEnvelope(t *testing.T, path string, env edits.Envelope) {
	t.Helper()
	data, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func captureApply(t *testing.T, args ...string) (string, error) {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	previous := os.Stdout
	os.Stdout = file
	defer func() { os.Stdout = previous }()
	err = applyCommand(args)
	if _, seekErr := file.Seek(0, 0); seekErr != nil {
		t.Fatal(seekErr)
	}
	data, readErr := os.ReadFile(file.Name())
	if readErr != nil {
		t.Fatal(readErr)
	}
	return string(data), err
}

func TestApplyDiffWriteAndStudioParity(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, intents, before := applyFixture(t)
	files := noopFiles(t, filepath.Dir(path))
	diff, err := captureApply(t, path, intents)
	if err != nil || !strings.Contains(diff, "--- a/score.cicada\n+++ b/score.cicada\n@@") || !strings.Contains(diff, "swing = 55%") || !reflect.DeepEqual(files, noopFiles(t, filepath.Dir(path))) {
		t.Fatalf("diff-only apply: %q %v", diff, err)
	}
	s, err := newStudio(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.shutdown() })
	envData, _ := os.ReadFile(intents)
	var env edits.Envelope
	if err := json.Unmarshal(envData, &env); err != nil {
		t.Fatal(err)
	}
	env.Revision = edits.Revision(before)
	r := studioCall(t, s.domainRoutes(), "/api/intents", env)
	want := assertProposalResponseMatchesDisk(t, path, r)
	if err := os.WriteFile(path, before, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(editlog.Path(path)); err != nil {
		t.Fatal(err)
	}
	gotDiff, err := captureApply(t, path, intents, "--write", "--author", "tester", "--session", "cli-test")
	got, _ := os.ReadFile(path)
	if err != nil || gotDiff != diff || !bytes.Equal(got, want) {
		t.Fatalf("write differs from Studio: %v %q", err, gotDiff)
	}
	records, skipped, err := editlog.ReadLog(editlog.Path(path))
	if err != nil || skipped != 0 || len(records) != 1 || records[0].Author != "tester" || records[0].Session != "cli-test" || len(records[0].Intents) != 1 || records[0].Parent != edits.Revision(before) || records[0].Revision != edits.Revision(want) {
		t.Fatalf("CLI log: %+v %v", records, err)
	}
	files = noopFiles(t, filepath.Dir(path))
	if diff, err := captureApply(t, path, intents, "--write"); err != nil || diff != "" || !reflect.DeepEqual(files, noopFiles(t, filepath.Dir(path))) {
		t.Fatalf("no-op apply: %q %v", diff, err)
	}
	env.Revision = edits.Revision(before)
	writeApplyEnvelope(t, intents, env)
	files = noopFiles(t, filepath.Dir(path))
	_, err = captureApply(t, path, intents, "--write")
	wantError := "score changed; intents target revision " + env.Revision + ", file is at " + edits.Revision(want)
	if err == nil || err.Error() != wantError || !reflect.DeepEqual(files, noopFiles(t, filepath.Dir(path))) {
		t.Fatalf("stale revision: %v", err)
	}
}

func TestApplyDefaultAuthorAndInvalidIntents(t *testing.T) {
	path, intents, before := applyFixture(t)
	if _, err := captureApply(t, "--write", path, intents); err != nil {
		t.Fatal(err)
	}
	records, _, err := editlog.ReadLog(editlog.Path(path))
	if err != nil || len(records) != 1 || records[0].Author != "cli" {
		t.Fatalf("default author: %+v %v", records, err)
	}
	writeApplyEnvelope(t, intents, edits.Envelope{Version: 1, Intents: []edits.Intent{&edits.ReplaceText{Source: "???"}}})
	files := noopFiles(t, filepath.Dir(path))
	_, err = captureApply(t, path, intents, "--write")
	if err == nil || !strings.Contains(err.Error(), "CICADA-SYNTAX") || !reflect.DeepEqual(files, noopFiles(t, filepath.Dir(path))) {
		t.Fatalf("invalid compiler text: %v", err)
	}
	for _, args := range [][]string{{}, {path}, {path, intents, "--unknown"}, {path, intents, "--author"}, {path, intents, "--session"}, {path, intents, "extra"}} {
		if _, err := captureApply(t, args...); err == nil {
			t.Fatalf("invalid args accepted: %v", args)
		}
	}
	writeApplyEnvelope(t, intents, edits.Envelope{Version: 1, DryRun: true, Intents: []edits.Intent{&edits.ReplaceText{Source: string(before)}}})
	files = noopFiles(t, filepath.Dir(path))
	if diff, err := captureApply(t, path, intents, "--write"); err != nil || diff == "" || !reflect.DeepEqual(files, noopFiles(t, filepath.Dir(path))) {
		t.Fatalf("envelope dry run wrote: %q %v", diff, err)
	}
}

func TestApplyRefusesConcurrentSaveAtExchange(t *testing.T) {
	path, intents, before := applyFixture(t)
	external := append(bytes.Clone(before), []byte("// concurrent save\n")...)
	var diff bytes.Buffer
	err := applyCommandWithHooks([]string{path, intents, "--write"}, &diff, func() {
		if err := os.WriteFile(path, external, 0644); err != nil {
			t.Fatal(err)
		}
	})
	got, _ := os.ReadFile(path)
	if err == nil || !bytes.Equal(got, external) {
		t.Fatalf("concurrent save overwritten: %v", err)
	}
	if records, _, err := editlog.ReadLog(editlog.Path(path)); err != nil || len(records) != 0 {
		t.Fatalf("failed write logged: %+v %v", records, err)
	}
}

func TestApplyAuxiliaryUpgradeDryRunAndWrite(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path := studioTestPath(t, studioScore)
	manifest := filepath.Join(filepath.Dir(path), "cicada.mod")
	if err := os.WriteFile(manifest, []byte("project cli\ncicada 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	intents := filepath.Join(filepath.Dir(path), "intents.json")
	writeApplyEnvelope(t, intents, edits.Envelope{Version: 1, Intents: []edits.Intent{&edits.SetParam{Entity: "param:bass.level", Value: json.RawMessage(`-3`), ConfirmUpgrade: true}, &edits.SetParam{Entity: "param:bass.pan", Value: json.RawMessage(`0.25`)}}})
	files := noopFiles(t, filepath.Dir(path))
	if diff, err := captureApply(t, path, intents); err != nil || !strings.Contains(diff, "cicada.mod") || !reflect.DeepEqual(files, noopFiles(t, filepath.Dir(path))) {
		t.Fatalf("upgrade dry run: %q %v", diff, err)
	}
	if _, err := captureApply(t, path, intents, "--write"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(manifest); string(got) != "project cli\ncicada 2\n" {
		t.Fatalf("manifest: %q", got)
	}
	if info, _ := os.Stat(manifest); info.Mode().Perm() != 0600 {
		t.Fatal("manifest mode changed")
	}
}
