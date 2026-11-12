package notation

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/kernel/voice/keyboard"
	"m31labs.dev/cicada/kernel/voice/modal"
)

// TestPatternSyntaxIsClassified reads the grammar and walks every rule a
// pattern can contain, with its keywords, punctuation and fields, and
// requires PatternSyntax to classify each: a new piece of pattern syntax
// fails here until someone says which input it is.
func TestPatternSyntaxIsClassified(t *testing.T) {
	data, err := os.ReadFile("../language/grammar/grammar.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	define := regexp.MustCompile(`g\.Define\("([a-z_]+)"`)
	bodies := map[string]string{}
	locations := define.FindAllStringSubmatchIndex(text, -1)
	for i, at := range locations {
		end := len(text)
		if i+1 < len(locations) {
			end = locations[i+1][0]
		}
		body := text[at[1]:end]
		if cut := strings.Index(body, "g.Test("); cut >= 0 {
			body = body[:cut]
		}
		bodies[text[at[2]:at[3]]] = body
	}
	sym := regexp.MustCompile(`sym\("([^"]+)"\)`)
	str := regexp.MustCompile(`str\("((?:[^"\\]|\\.)*)"\)`)
	field := regexp.MustCompile(`field\("([a-z_]+)"`)
	seen := map[string]bool{}
	var missing []string
	need := func(name string) {
		if _, ok := PatternSyntax[name]; !ok && !containsName(missing, name) {
			missing = append(missing, name)
		}
	}
	var walk func(rule string)
	walk = func(rule string) {
		if seen[rule] {
			return
		}
		seen[rule] = true
		need(rule)
		body := bodies[rule]
		for _, m := range str.FindAllStringSubmatch(body, -1) {
			need(strings.ReplaceAll(m[1], `\\`, `\`))
		}
		for _, m := range field.FindAllStringSubmatch(body, -1) {
			need(m[1])
		}
		if rule == "expression_row_label" {
			label := regexp.MustCompile(`\(([a-z|]+)\):`).FindStringSubmatch(body)
			if label == nil {
				t.Fatal("expression_row_label lists no rows")
			}
			for _, row := range strings.Split(label[1], "|") {
				need("row " + row)
			}
		}
		for _, m := range sym.FindAllStringSubmatch(body, -1) {
			walk(m[1])
		}
	}
	for _, root := range []string{"acid_pattern", "note_pattern", "drum_pattern", "performance_pattern"} {
		if bodies[root] == "" {
			t.Fatalf("the grammar has no %s", root)
		}
		walk(root)
	}
	for _, rule := range []string{"acid_note", "modifier", "chord_note", "drum_hit", "velocity_hit", "performed_expression", "expression_row_label"} {
		if !seen[rule] {
			t.Fatalf("the walk did not reach %s; the grammar reader is broken", rule)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("pattern syntax the voice check does not classify: %q", missing)
	}
	for name, input := range PatternSyntax {
		if input != "structure" && !containsName(append(append(append([]string(nil), MelodicInputs...), PerformedInputs...), DrumInputs...), input) {
			t.Errorf("%s is classified as %q, which is not an input", name, input)
		}
	}
}

// TestPatternInputsRefusesWhatItDoesNotModel: a setting the check does not
// know is named, not passed.
func TestPatternInputsRefusesWhatItDoesNotModel(t *testing.T) {
	_, err := PatternInputs(Pattern{Name: "p", Attrs: []Param{{Name: "wobble", Value: "3"}}})
	if err == nil || !strings.Contains(err.Error(), "pattern p carries the setting wobble") {
		t.Fatalf("%v", err)
	}
	score, ds := Parse([]byte("cicada 2\ntempo 120\ntrack t acid {}\npattern p { [a2 c3 e3] . a2^ c3?50 }\nscene main { t = p }\nsong { main }\n"))
	if hasErrors(ds) {
		t.Fatal(ds)
	}
	inputs, err := PatternInputs(score.Patterns[0])
	if err != nil || strings.Join(inputs, ",") != "accent,chance,brackets" {
		t.Fatalf("%v %v", inputs, err)
	}
}

func containsName(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

func hasErrors(ds []Diagnostic) bool {
	for _, d := range ds {
		if d.Severity == "error" {
			return true
		}
	}
	return false
}

// DrumLaneNames copies the engine's lane names so that notation need not
// import the drum DSP; the two lists must stay equal.
func TestDrumLaneNamesMatchTheEngine(t *testing.T) {
	if DrumLaneNames != drum.Names {
		t.Fatalf("notation.DrumLaneNames %v, kernel/voice/drum.Names %v", DrumLaneNames, drum.Names)
	}
}

// Every voice a track can play has a row in VoiceInputs, so the voice-change
// check never meets a voice it cannot compare: every built-in voice (audio
// apart: it plays clips, not patterns) and every kind of declared voice. A
// built-in voice added without a row fails here.
func TestEveryVoiceHasAnInputsRow(t *testing.T) {
	score, ds := Parse([]byte(`cicada 2
tempo 120
instrument mono { voice mono { out = saw(pitch) * env(gate, 200ms) } }
instrument poly { voice poly { out = saw(pitch) * env(gate, 200ms) * velocity } }
kit built { bd = builtin.bd sd = builtin.sd }
kit modeled { bd = model.kick }
kit graph { bd = mono sd = mono }
track t acid {}
pattern p { a2 . }
scene main { t = p }
song { main }
`))
	if hasErrors(ds) {
		t.Fatal(ds)
	}
	kinds := []string{"acid", "drums", "piano", "guitar", "mono", "poly", "built", "modeled", "graph"}
	for _, name := range keyboard.Names {
		kinds = append(kinds, name)
	}
	for _, name := range modal.Names {
		kinds = append(kinds, "model_"+name)
	}
	for _, kind := range kinds {
		if !BuiltinVoice(2, kind) && !VoiceDeclared(score, kind) {
			t.Errorf("%s is not a voice", kind)
		}
		row := VoiceInputs(score, kind)
		if row == nil {
			t.Errorf("%s has no inputs row", kind)
			continue
		}
		inputs := MelodicInputs
		if VoiceLanes(score, kind) != nil {
			inputs = DrumInputs
		}
		for _, input := range inputs {
			if _, ok := row[input]; !ok {
				t.Errorf("%s: no entry for %s", kind, input)
			}
		}
	}
}
