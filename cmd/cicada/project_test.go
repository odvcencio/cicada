package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/render"
)

func TestProjectCLI(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "cicada")
	build := exec.Command("go", "build", "-o", bin, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	first := filepath.Join("..", "..", "examples", "first-acid.cicada")
	other := filepath.Join("..", "..", "examples", "glassbass.cicada")
	jsonPath := filepath.Join(t.TempDir(), "score.json")
	textPath := filepath.Join(t.TempDir(), "score.cicada")
	run := func(wantCode int, args ...string) string {
		t.Helper()
		command := exec.Command(bin, args...)
		output, err := command.CombinedOutput()
		if wantCode == 0 && err != nil {
			t.Fatalf("cicada %v: %v\n%s", args, err, output)
		}
		if wantCode != 0 {
			exit, ok := err.(*exec.ExitError)
			if !ok || exit.ExitCode() != wantCode {
				t.Fatalf("cicada %v: want exit %d, got %v\n%s", args, wantCode, err, output)
			}
		}
		return string(output)
	}
	if output := run(0, "convert", first, "-o", jsonPath); output != jsonPath+"\n" {
		t.Fatalf("convert output: %q", output)
	}
	run(0, "fmt", jsonPath, "--check")
	run(0, "convert", jsonPath, "-o", textPath)
	if output := run(0, "compare", "--semantic", first, textPath); output != "equal\n" {
		t.Fatalf("compare output: %q", output)
	}
	if output := run(1, "compare", "--semantic", first, other); !strings.Contains(output, "different") {
		t.Fatalf("difference output: %q", output)
	}
	if output := run(2, "convert", first, jsonPath); !strings.Contains(output, "usage:") {
		t.Fatalf("usage output: %q", output)
	}
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("empty conversion output")
	}
	wavPath := filepath.Join(t.TempDir(), "first-acid.wav")
	if output := run(0, "render", first, "-o", wavPath, "--rate", "48000", "--bits", "24", "--bars", "16", "--tail", "3s"); !strings.Contains(output, "clipped samples") {
		t.Fatalf("render report omitted clipping count: %q", output)
	}
	if output := run(0, "verify-wav", wavPath, "--rate", "48000", "--bits", "24", "--bars", "16", "--tail", "3s", "--peak-max-db", "-0.3", "--dc-max-db", "-60"); !strings.Contains(output, "1479653 frames") {
		t.Fatalf("unexpected WAV verification: %q", output)
	}
	if output := run(1, "verify-wav", wavPath, "--rate", "48000", "--bits", "24", "--bars", "16", "--tail", "3s", "--peak-max-db", "-80", "--dc-max-db", "-60"); !strings.Contains(output, "exceeds") {
		t.Fatalf("strict peak ceiling was not enforced: %q", output)
	}
	run(2, "render", first, "-o", wavPath, "--bits", "16")
}

func TestFailedRenderPreservesOutput(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "examples", "first-acid.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	score, diagnostics := notation.Parse(source)
	if len(diagnostics) != 0 {
		t.Fatalf("parse: %+v", diagnostics)
	}
	score.Song = nil // exercise an error after the temporary output file is opened
	path := filepath.Join(t.TempDir(), "existing.wav")
	if err := os.WriteFile(path, []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := renderFile(score, path, render.Options{SampleRate: 48_000, TailSec: 3}); err == nil {
		t.Fatal("expected missing arrangement error")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "keep" {
		t.Fatalf("render replaced existing output: %q, %v", data, err)
	}
}
