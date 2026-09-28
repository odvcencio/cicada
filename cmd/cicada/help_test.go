package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHelpTextGolden(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{name: "global", args: []string{"--help"}},
		{name: "fix", args: []string{"help", "fix"}},
		{name: "play", args: []string{"help", "play"}},
		{name: "studio", args: []string{"studio", "--help"}},
		{name: "render", args: []string{"render", "--help"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			handled, exitCode := handleCLIHelp(test.args, &stdout, &stderr)
			if !handled || exitCode != 0 || stderr.Len() != 0 {
				t.Fatalf("help dispatch: handled=%t exit=%d stderr=%q", handled, exitCode, stderr.String())
			}
			goldenPath := filepath.Join("testdata", "help", test.name+".golden")
			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(stdout.Bytes(), want) {
				t.Fatalf("help output differs from %s\nwant:\n%s\ngot:\n%s", goldenPath, want, stdout.String())
			}
		})
	}
	var byCommand, byHelp bytes.Buffer
	if handled, code := handleCLIHelp([]string{"play", "--help"}, &byCommand, &bytes.Buffer{}); !handled || code != 0 {
		t.Fatalf("command help dispatch: handled=%t code=%d", handled, code)
	}
	if handled, code := handleCLIHelp([]string{"help", "play"}, &byHelp, &bytes.Buffer{}); !handled || code != 0 {
		t.Fatalf("help command dispatch: handled=%t code=%d", handled, code)
	}
	if !bytes.Equal(byCommand.Bytes(), byHelp.Bytes()) {
		t.Fatalf("play --help differs from help play:\n%s\n%s", byCommand.String(), byHelp.String())
	}
}

func TestEveryCommandHasPerCommandHelp(t *testing.T) {
	for _, entry := range commandHelpEntries {
		entry := entry
		t.Run(entry.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			handled, exitCode := handleCLIHelp([]string{entry.name, "--help"}, &stdout, &stderr)
			if !handled || exitCode != 0 || stderr.Len() != 0 {
				t.Fatalf("help dispatch: handled=%t exit=%d stderr=%q", handled, exitCode, stderr.String())
			}
			if !strings.Contains(stdout.String(), entry.usage) || !strings.Contains(stdout.String(), "Flags:") || !strings.Contains(stdout.String(), "--help") {
				t.Fatalf("incomplete command help:\n%s", stdout.String())
			}
		})
	}
}

func TestAllCommandHelpTextGolden(t *testing.T) {
	blocks := make([]string, 0, len(commandHelpEntries))
	for _, entry := range commandHelpEntries {
		blocks = append(blocks, fmt.Sprintf("### %s\n%s", entry.name, renderCommandHelp(entry)))
	}
	output := strings.Join(blocks, "\n\n") + "\n"
	path := filepath.Join("testdata", "help", "all-commands.golden")
	if os.Getenv("CICADA_UPDATE_HELP_GOLDEN") == "1" {
		if err := os.WriteFile(path, []byte(output), 0644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal([]byte(output), want) {
		t.Fatalf("all command help differs from %s", path)
	}
}

func TestUnknownCommandHelpIsConcise(t *testing.T) {
	var stdout, stderr bytes.Buffer
	handled, exitCode := handleCLIHelp([]string{"bogus"}, &stdout, &stderr)
	if !handled || exitCode != 2 || stdout.Len() != 0 {
		t.Fatalf("unknown dispatch: handled=%t exit=%d stdout=%q", handled, exitCode, stdout.String())
	}
	if got, want := stderr.String(), "cicada: unknown command \"bogus\"\nrun cicada help\n"; got != want {
		t.Fatalf("unknown command output:\n%q\nwant:\n%q", got, want)
	}
}
