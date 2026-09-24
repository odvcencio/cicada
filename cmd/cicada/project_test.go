package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
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
	path := filepath.Join(t.TempDir(), "existing.wav")
	if err := os.WriteFile(path, []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := renderFile(score, path); err == nil {
		t.Fatal("expected unsupported acid renderer error")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "keep" {
		t.Fatalf("render replaced existing output: %q, %v", data, err)
	}
}
