package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLanguageCLI(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "cicada")
	build := exec.Command("go", "build", "-o", bin, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	score := filepath.Join("..", "..", "examples", "first-acid.cicada")
	run := func(wantCode int, env []string, args ...string) (string, string) {
		t.Helper()
		command := exec.Command(bin, args...)
		command.Env = append(os.Environ(), env...)
		var stdout, stderr strings.Builder
		command.Stdout, command.Stderr = &stdout, &stderr
		err := command.Run()
		code := 0
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		if code != wantCode {
			t.Fatalf("cicada %v: exit %d, want %d\n%s%s", args, code, wantCode, stdout.String(), stderr.String())
		}
		return stdout.String(), stderr.String()
	}

	ansi, _ := run(0, []string{"NO_COLOR="}, "highlight", score)
	if !strings.Contains(ansi, "\x1b[1;38;2;155;225;93mcicada\x1b[0m") {
		t.Fatalf("highlight did not color the cicada directive: %q", ansi[:min(len(ansi), 200)])
	}
	plain, _ := run(0, []string{"NO_COLOR=1"}, "highlight", score)
	source, err := os.ReadFile(score)
	if err != nil {
		t.Fatal(err)
	}
	if plain != string(source) {
		t.Fatal("NO_COLOR output should be the source unchanged")
	}
	page, _ := run(0, nil, "highlight", score, "--html")
	if !strings.HasPrefix(page, "<!doctype html>") || !strings.Contains(page, "<title>first-acid.cicada</title>") {
		t.Fatalf("html output: %q", page[:min(len(page), 300)])
	}
	spans, _ := run(0, nil, "highlight", "--spans", score)
	if !strings.Contains(spans, "62:3     tag.builtin                  \"sd:\"") {
		t.Fatalf("span listing lacks the snare lane:\n%s", spans)
	}

	outline, _ := run(0, nil, "symbols", score)
	if !strings.Contains(outline, "27:12    definition instrument glassbass\n") || strings.Contains(outline, "reference") {
		t.Fatalf("symbols output:\n%s", outline)
	}
	var symbols []struct {
		Role, Kind, Name string
	}
	encoded, _ := run(0, nil, "symbols", "--refs", "--json", score)
	if err := json.Unmarshal([]byte(encoded), &symbols); err != nil {
		t.Fatalf("symbols json: %v\n%s", err, encoded)
	}
	references := 0
	for _, symbol := range symbols {
		if symbol.Role == "reference" {
			references++
		}
	}
	if references == 0 || len(symbols) <= references {
		t.Fatalf("want definitions and references, got %+v", symbols)
	}

	broken := filepath.Join(t.TempDir(), "broken.cicada")
	if err := os.WriteFile(broken, []byte("cicada 1\ntrack bass acid {\n  cutoff = 620hz\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	listing, stderr := run(1, nil, "highlight", "--spans", broken)
	if !strings.Contains(listing, "error") || !strings.Contains(stderr, broken+":") || !strings.Contains(stderr, "CICADA-SYNTAX") {
		t.Fatalf("broken score: stdout %q stderr %q", listing, stderr)
	}
	run(2, nil, "highlight", "--html", "--spans", score)
	run(2, nil, "symbols", "--bogus", score)
}
