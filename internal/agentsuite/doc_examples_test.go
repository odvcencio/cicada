package agentsuite

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// agentPages are the pages whose examples run against the binary, each in
// its own empty folder, with the least number of commands each must show.
var agentPages = []struct {
	path     string
	commands int
}{
	{"../../docs/manual/agents.md", 6},
	{"../../docs/manual/agent-quickstart.md", 5},
}

// agentDocStep is one marked block of a page: a file to write into the
// example folder, a command whose output the page shows, or the folder later
// steps run in.
type agentDocStep struct {
	line    int
	file    string   // set for <!-- agent-example-file: NAME -->
	command []string // set for <!-- agent-example: ARGS [exit=N] -->
	folder  string   // set for <!-- agent-example-folder: DIR -->, which has no block
	exit    int
	json    bool // the block is JSON, which may shorten the output; otherwise it is the exact output
	body    string
}

// readAgentDocSteps reads the marked blocks of one page in page order. A
// marker comment is followed, after any blank lines, by the fenced block it
// applies to; a folder marker stands alone.
func readAgentDocSteps(t *testing.T, page string) []agentDocStep {
	t.Helper()
	data, err := os.ReadFile(page)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(data), "\n")
	fence := strings.Repeat("`", 3)
	var steps []agentDocStep
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		var step agentDocStep
		switch {
		case strings.HasPrefix(line, "<!-- agent-example-folder: "):
			folder := strings.TrimSuffix(strings.TrimPrefix(line, "<!-- agent-example-folder: "), " -->")
			steps = append(steps, agentDocStep{line: i + 1, folder: folder})
			continue
		case strings.HasPrefix(line, "<!-- agent-example-file: "):
			step.file = strings.TrimSuffix(strings.TrimPrefix(line, "<!-- agent-example-file: "), " -->")
		case strings.HasPrefix(line, "<!-- agent-example: "):
			args := strings.Fields(strings.TrimSuffix(strings.TrimPrefix(line, "<!-- agent-example: "), " -->"))
			if last := args[len(args)-1]; strings.HasPrefix(last, "exit=") {
				code, err := strconv.Atoi(strings.TrimPrefix(last, "exit="))
				if err != nil {
					t.Fatalf("%s:%d: bad exit status %q", page, i+1, last)
				}
				step.exit, args = code, args[:len(args)-1]
			}
			step.command = args
		default:
			continue
		}
		step.line = i + 1
		j := i + 1
		for j < len(lines) && strings.TrimSpace(lines[j]) == "" {
			j++
		}
		if j == len(lines) || !strings.HasPrefix(strings.TrimSpace(lines[j]), fence) {
			t.Fatalf("%s:%d: a marker must be followed by a fenced block", page, i+1)
		}
		step.json = strings.TrimSpace(lines[j]) == fence+"json"
		end := j + 1
		for end < len(lines) && strings.TrimSpace(lines[end]) != fence {
			end++
		}
		if end == len(lines) {
			t.Fatalf("%s:%d: unterminated block", page, j+1)
		}
		step.body = strings.Join(lines[j+1:end], "\n") + "\n"
		steps = append(steps, step)
		i = end
	}
	return steps
}

// TestAgentDocumentationExamples runs the commands docs/manual/agents.md and
// docs/manual/agent-quickstart.md show, in page order, each page in one
// empty folder that holds its files, and requires every value a page shows
// to match the binary's output. A page may shorten JSON output: an object may
// leave out fields and an array may stop early, but nothing it shows may
// differ. Output shown in another block, such as text, must be exact.
func TestAgentDocumentationExamples(t *testing.T) {
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
	for _, page := range agentPages {
		runAgentDocPage(t, bin, page.path, page.commands)
	}
}

func runAgentDocPage(t *testing.T, bin, page string, least int) {
	t.Helper()
	dir := t.TempDir()
	commands := 0
	for _, step := range readAgentDocSteps(t, page) {
		switch {
		case step.folder != "":
			dir = filepath.Join(dir, filepath.FromSlash(step.folder))
			if info, err := os.Stat(dir); err != nil || !info.IsDir() {
				t.Fatalf("%s:%d: the folder %s does not exist when the page moves into it", page, step.line, step.folder)
			}
			continue
		case step.file != "":
			if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(step.file)), []byte(step.body), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		commands++
		stdout, code := cicada(t, bin, dir, step.command...)
		if code != step.exit {
			t.Fatalf("%s:%d: cicada %s exited %d, the page says %d", page, step.line, strings.Join(step.command, " "), code, step.exit)
		}
		if !step.json {
			if string(stdout) != step.body {
				t.Errorf("%s:%d: cicada %s printed %q, the page shows %q", page, step.line, strings.Join(step.command, " "), stdout, step.body)
			}
			continue
		}
		var shown, actual any
		if err := json.Unmarshal([]byte(step.body), &shown); err != nil {
			t.Fatalf("%s:%d: the shown output is not JSON: %v", page, step.line, err)
		}
		decode(t, strings.Join(step.command, " "), stdout, &actual)
		if problem := jsonShownIn(shown, actual, "$"); problem != "" {
			t.Errorf("%s:%d: cicada %s: %s\nactual output:\n%s", page, step.line, strings.Join(step.command, " "), problem, stdout)
		}
	}
	if commands < least {
		t.Fatalf("%s shows only %d commands", page, commands)
	}
}

// jsonShownIn reports where shown differs from actual, or "" when every value
// shown is in actual. Numbers match within one part in 10^9.
func jsonShownIn(shown, actual any, at string) string {
	switch want := shown.(type) {
	case map[string]any:
		got, ok := actual.(map[string]any)
		if !ok {
			return fmt.Sprintf("%s: the page shows an object, the output has %T", at, actual)
		}
		for key, value := range want {
			inner, ok := got[key]
			if !ok {
				return fmt.Sprintf("%s.%s: the output has no such field", at, key)
			}
			if problem := jsonShownIn(value, inner, at+"."+key); problem != "" {
				return problem
			}
		}
		return ""
	case []any:
		got, ok := actual.([]any)
		if !ok || len(got) < len(want) {
			return fmt.Sprintf("%s: the page shows %d elements, the output has %v", at, len(want), actual)
		}
		for i := range want {
			if problem := jsonShownIn(want[i], got[i], fmt.Sprintf("%s[%d]", at, i)); problem != "" {
				return problem
			}
		}
		return ""
	case float64:
		got, ok := actual.(float64)
		if !ok || math.Abs(got-want) > 1e-9*math.Max(1, math.Abs(want)) {
			return fmt.Sprintf("%s: the page shows %v, the output has %v", at, want, actual)
		}
		return ""
	default:
		if !bytes.Equal(mustJSON(shown), mustJSON(actual)) {
			return fmt.Sprintf("%s: the page shows %v, the output has %v", at, shown, actual)
		}
		return ""
	}
}

func mustJSON(v any) []byte {
	data, _ := json.Marshal(v)
	return data
}
