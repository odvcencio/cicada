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

const agentsPage = "../../docs/manual/agents.md"

// agentDocStep is one marked block of docs/manual/agents.md: a file to write
// into the example folder, or a command whose JSON output the page shows.
type agentDocStep struct {
	line    int
	file    string   // set for <!-- agent-example-file: NAME -->
	command []string // set for <!-- agent-example: ARGS [exit=N] -->
	exit    int
	body    string
}

// readAgentDocSteps reads the marked blocks in page order. A marker comment
// is followed, after any blank lines, by the fenced block it applies to.
func readAgentDocSteps(t *testing.T) []agentDocStep {
	t.Helper()
	data, err := os.ReadFile(agentsPage)
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
		case strings.HasPrefix(line, "<!-- agent-example-file: "):
			step.file = strings.TrimSuffix(strings.TrimPrefix(line, "<!-- agent-example-file: "), " -->")
		case strings.HasPrefix(line, "<!-- agent-example: "):
			args := strings.Fields(strings.TrimSuffix(strings.TrimPrefix(line, "<!-- agent-example: "), " -->"))
			if last := args[len(args)-1]; strings.HasPrefix(last, "exit=") {
				code, err := strconv.Atoi(strings.TrimPrefix(last, "exit="))
				if err != nil {
					t.Fatalf("%s:%d: bad exit status %q", agentsPage, i+1, last)
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
			t.Fatalf("%s:%d: a marker must be followed by a fenced block", agentsPage, i+1)
		}
		end := j + 1
		for end < len(lines) && strings.TrimSpace(lines[end]) != fence {
			end++
		}
		if end == len(lines) {
			t.Fatalf("%s:%d: unterminated block", agentsPage, j+1)
		}
		step.body = strings.Join(lines[j+1:end], "\n") + "\n"
		steps = append(steps, step)
		i = end
	}
	return steps
}

// TestAgentDocumentationExamples runs the commands docs/manual/agents.md
// shows, in page order, in one folder that holds the page's files, and
// requires every value the page shows to match the binary's output. The page
// may shorten output: an object may leave out fields and an array may stop
// early, but nothing it shows may differ.
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
	dir := t.TempDir()
	commands := 0
	for _, step := range readAgentDocSteps(t) {
		if step.file != "" {
			if err := os.WriteFile(filepath.Join(dir, step.file), []byte(step.body), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		commands++
		stdout, code := cicada(t, bin, dir, step.command...)
		if code != step.exit {
			t.Fatalf("%s:%d: cicada %s exited %d, the page says %d", agentsPage, step.line, strings.Join(step.command, " "), code, step.exit)
		}
		var shown, actual any
		if err := json.Unmarshal([]byte(step.body), &shown); err != nil {
			t.Fatalf("%s:%d: the shown output is not JSON: %v", agentsPage, step.line, err)
		}
		decode(t, strings.Join(step.command, " "), stdout, &actual)
		if problem := jsonShownIn(shown, actual, "$"); problem != "" {
			t.Errorf("%s:%d: cicada %s: %s\nactual output:\n%s", agentsPage, step.line, strings.Join(step.command, " "), problem, stdout)
		}
	}
	if commands < 6 {
		t.Fatalf("%s shows only %d commands", agentsPage, commands)
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
