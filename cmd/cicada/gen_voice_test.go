package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func generateToFile(t *testing.T, args ...string) (string, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "generated.cicada")
	stdout := os.Stdout
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = devnull
	defer func() {
		os.Stdout = stdout
		devnull.Close()
	}()
	if err := generatorCommand(append(args, "-o", path)); err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data), nil
}

// With no --voice, gen writes an FM bass. The checked-in fixtures in examples/gen
// are the acid reference set: --voice acid writes them byte for byte, and the
// default differs from them in the track line only.
func TestGenVoiceDefaultsToFMBassAndAcidReproducesTheFixtures(t *testing.T) {
	for _, seed := range []int{0, 7, 23} {
		fixturePath := filepath.Join(repositoryRoot(), "examples", "gen", fmt.Sprintf("seed-%04d.cicada", seed))
		fixture, err := os.ReadFile(fixturePath)
		if err != nil {
			t.Fatal(err)
		}
		args := []string{"--seed", fmt.Sprint(seed), "--key", "a", "--scale", "minor"}
		acid, err := generateToFile(t, append(args, "--voice", "acid")...)
		if err != nil || acid != string(fixture) {
			t.Fatalf("seed %d: --voice acid differs from %s: %v", seed, fixturePath, err)
		}
		byDefault, err := generateToFile(t, args...)
		if err != nil {
			t.Fatal(err)
		}
		want := strings.Replace(string(fixture), "track bass acid {}\n", "track bass fm_bass {}\n", 1)
		if byDefault != want {
			t.Fatalf("seed %d: the default voice changed more than the track line:\n%s", seed, byDefault)
		}
		if strings.Contains(byDefault, "acid") {
			t.Fatalf("seed %d: default output names the acid voice:\n%s", seed, byDefault)
		}
	}
}

func TestGenVoiceAcceptsNoteVoicesAndRefusesTheRest(t *testing.T) {
	for _, voice := range []string{"piano", "tine_ep", "poly_keys", "soft_pad"} {
		source, err := generateToFile(t, "--seed", "3", "--voice", voice)
		if err != nil || !strings.Contains(source, "track bass "+voice+" {}\n") {
			t.Fatalf("--voice %s: %v\n%s", voice, err, source)
		}
		// Every offered voice writes a score that passes check.
		path := filepath.Join(t.TempDir(), "voice.cicada")
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		if err := checkPaths([]string{path}, &stdout, &stderr); err != nil {
			t.Fatalf("--voice %s output fails check: %v\n%s", voice, err, stderr.String())
		}
	}
	for _, voice := range []string{"drums", "model_bd", "guitar", "nope"} {
		_, err := generateToFile(t, "--voice", voice)
		if err == nil || !strings.Contains(err.Error(), "CICADA-PARAM") || !strings.Contains(err.Error(), "unknown voice") {
			t.Fatalf("--voice %s: error = %v", voice, err)
		}
	}
}

// The default output is a portable score: one keyboard bass track.
func TestGenDefaultOutputFitsTheBrowserProfile(t *testing.T) {
	source, err := generateToFile(t, "--seed", "4242", "--key", "a", "--scale", "minor")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "generated.cicada")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := checkCommand([]string{"--profile", "browser", path}, &stdout, &stderr); err != nil {
		t.Fatalf("check --profile browser: %v\n%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "1 of 16 tracks") {
		t.Fatalf("browser profile report: %s", stdout.String())
	}
}
