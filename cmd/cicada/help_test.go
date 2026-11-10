package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
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
		{name: "audition", args: []string{"audition", "--help"}},
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

// dispatchedCommands reads the command names main.go dispatches on: every
// string compared with os.Args[1] or with the command variable.
func dispatchedCommands(t *testing.T) map[string]bool {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	commands := map[string]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		binary, ok := node.(*ast.BinaryExpr)
		if !ok || binary.Op != token.EQL && binary.Op != token.NEQ {
			return true
		}
		literal, ok := binary.Y.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		var operand bytes.Buffer
		if err := printer.Fprint(&operand, token.NewFileSet(), binary.X); err != nil {
			t.Fatal(err)
		}
		if operand.String() == "os.Args[1]" || operand.String() == "command" {
			name, err := strconv.Unquote(literal.Value)
			if err != nil {
				t.Fatal(err)
			}
			commands[name] = true
		}
		return true
	})
	if len(commands) < 30 {
		t.Fatalf("found only %d dispatched commands in main.go; has the dispatch changed shape?", len(commands))
	}
	return commands
}

// TestOverviewListsEveryCommand fails when main.go dispatches a command that
// the overview (cicada help) does not list or that has no help entry.
func TestOverviewListsEveryCommand(t *testing.T) {
	listed := map[string]int{}
	for _, group := range overviewGroups {
		for _, name := range group {
			listed[name]++
		}
	}
	for name := range dispatchedCommands(t) {
		if listed[name] != 1 {
			t.Errorf("command %q is dispatched in main.go but listed %d times in overviewGroups", name, listed[name])
		}
	}
	for name := range listed {
		if _, ok := findCommandHelp(name); !ok {
			t.Errorf("overview lists %q, which has no help entry", name)
		}
		if listed[name] > 1 {
			t.Errorf("overview lists %q %d times", name, listed[name])
		}
	}
	for _, entries := range [][]commandHelpEntry{commandHelpEntries, extraCommandHelpEntries} {
		for _, entry := range entries {
			if !strings.Contains(entry.name, " ") && listed[entry.name] == 0 {
				t.Errorf("help entry %q is missing from the overview", entry.name)
			}
		}
	}
}

func TestHelpJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	handled, code := handleCLIHelp([]string{"help", "--json"}, &stdout, &stderr)
	if !handled || code != 0 || stderr.Len() != 0 {
		t.Fatalf("help --json: handled=%t code=%d stderr=%q", handled, code, stderr.String())
	}
	var document struct {
		Schema   string `json:"schema"`
		Commands []struct {
			Name    string   `json:"name"`
			Summary string   `json:"summary"`
			Usage   []string `json:"usage"`
			Flags   []struct {
				Name        string `json:"name"`
				Argument    string `json:"argument"`
				Description string `json:"description"`
			} `json:"flags"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &document); err != nil {
		t.Fatalf("help --json is not JSON: %v", err)
	}
	if document.Schema != helpSchema || len(document.Commands) != len(commandHelpEntries)+len(extraCommandHelpEntries) {
		t.Fatalf("schema %q, %d commands", document.Schema, len(document.Commands))
	}
	found := map[string]bool{}
	for i, command := range document.Commands {
		if i > 0 && document.Commands[i-1].Name >= command.Name {
			t.Fatalf("commands are not sorted at %s", command.Name)
		}
		found[command.Name] = true
		if command.Summary == "" || len(command.Usage) == 0 || len(command.Flags) == 0 {
			t.Errorf("%s: incomplete %+v", command.Name, command)
		}
		for _, flag := range command.Flags {
			if !strings.HasPrefix(flag.Name, "-") || strings.ContainsAny(flag.Name, " =<") || flag.Description == "" {
				t.Errorf("%s: flag parsed badly: %+v", command.Name, flag)
			}
			if flag.Name == "--loudness" && flag.Argument != "<LUFS>" || flag.Name == "--dither" && flag.Argument != "<bool>" {
				t.Errorf("%s: flag argument parsed badly: %+v", command.Name, flag)
			}
		}
	}
	for name := range dispatchedCommands(t) {
		if !found[name] {
			t.Errorf("help --json lacks %s", name)
		}
	}
	stdout.Reset()
	if handled, code := handleCLIHelp([]string{"help", "render", "--json"}, &stdout, &stderr); !handled || code != 0 || !strings.Contains(stdout.String(), `"name": "render"`) || strings.Contains(stdout.String(), `"name": "stems"`) {
		t.Fatalf("help render --json: handled=%t code=%d\n%s", handled, code, stdout.String())
	}
}

// TestHelpWithJSONPrintsOneDocument covers --help and unknown commands when
// --json is given: each prints one document.
func TestHelpWithJSONPrintsOneDocument(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		code    int
		command string
		error   string
	}{
		{[]string{"check", "--help", "--json"}, 0, "check", ""},
		{[]string{"lib", "update", "--help", "--json"}, 0, "lib update", ""},
		{[]string{"--help", "--json"}, 0, "", ""},
		{[]string{"nosuch", "--json"}, 2, "", "unknown_command"},
	} {
		var stdout, stderr bytes.Buffer
		handled, code := handleCLIHelp(tc.args, &stdout, &stderr)
		var document struct {
			Schema   string `json:"schema"`
			Commands []struct {
				Name string `json:"name"`
			} `json:"commands"`
			Error *struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		decoder := json.NewDecoder(&stdout)
		if err := decoder.Decode(&document); err != nil || decoder.More() {
			t.Fatalf("%v: standard output is not one document: %v", tc.args, err)
		}
		if !handled || code != tc.code || document.Schema != helpSchema {
			t.Fatalf("%v: handled=%t code=%d schema %q", tc.args, handled, code, document.Schema)
		}
		switch {
		case tc.error != "":
			if document.Error == nil || document.Error.Code != tc.error || document.Error.Message == "" {
				t.Fatalf("%v: error %+v", tc.args, document.Error)
			}
		case tc.command != "":
			if len(document.Commands) != 1 || document.Commands[0].Name != tc.command {
				t.Fatalf("%v: commands %+v", tc.args, document.Commands)
			}
		default:
			if len(document.Commands) < 40 {
				t.Fatalf("%v: %d commands", tc.args, len(document.Commands))
			}
		}
	}
	// A command run with --json and no --help is the command's business.
	if handled, _ := handleCLIHelp([]string{"check", "--json"}, &bytes.Buffer{}, &bytes.Buffer{}); handled {
		t.Fatal("check --json was taken as a help request")
	}
}
