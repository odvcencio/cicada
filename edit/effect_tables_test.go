package edit_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// tableScore holds one ampcab on a track and one convolution on master.
const tableScore = "cicada 2\ntempo 120\nasset vocal \"v.wav\" { sha256 = \"" + "0000000000000000000000000000000000000000000000000000000000000000" + "\" format = wav frames = 4800 rate = 48000Hz channels = 1 }\nfx cab ampcab {}\nfx space convolution { asset = vocal }\ntrack bass acid { insert = cab }\nmaster { insert = space }\npattern riff { a1 . }\nscene main { bass = riff }\nsong { main }\n"

// tableAccepts reports whether setparam's hand-written table takes a value:
// the intent is not refused (the compile after it is not the table's).
func tableAccepts(owner, field, value string) bool {
	_, err := edits.Apply([]byte(tableScore), edits.Envelope{Version: 1, Intents: []edits.Intent{&edits.SetParam{Entity: edits.EntityID("param:" + owner + "." + field), Value: json.RawMessage(fmt.Sprintf("%q", value))}}}, edits.Options{Compiler: m5Compiler{}})
	var refused *edits.IntentError
	return !errors.As(err, &refused)
}

// validatorAccepts writes the value into the score text and reports whether
// the language's validator takes it. An ampcab is compiled for the engine
// too; a convolution would need its asset's audio there, so its settings
// are checked by project validation.
func validatorAccepts(owner, field, value string) bool {
	declaration := map[string]string{"cab": "fx cab ampcab {}", "space": "fx space convolution { asset = vocal }"}[owner]
	written := strings.TrimSuffix(declaration, "}")
	if strings.HasSuffix(written, "{") {
		written += " "
	}
	source := strings.Replace(tableScore, declaration, written+field+" = "+value+" }", 1)
	if owner == "cab" {
		// The engine would open the convolution's asset; the ampcab needs
		// neither the asset nor the convolution.
		source = strings.NewReplacer("fx space convolution { asset = vocal }\n", "", "master { insert = space }\n", "").Replace(source)
		source = source[:strings.Index(source, "asset ")] + source[strings.Index(source, "fx cab"):]
	}
	score, ds := notation.Parse([]byte(source))
	for _, d := range ds {
		if d.Severity == "error" {
			return false
		}
	}
	p, ds := project.FromScore(score)
	for _, d := range ds {
		if d.Severity == "error" {
			return false
		}
	}
	if p == nil || project.ValidateProject(p) != nil {
		return false
	}
	if owner == "space" {
		return true
	}
	_, err := project.CompileEngine(p, 48000, 512)
	return err == nil
}

// TestHandWrittenEffectTablesMatchTheValidator checks the two settings tables
// the paramdefs registry does not hold, ampcab's (edit/mixer.go) and
// convolution's (edit/effects.go), against the language's validator: each
// bound is taken by both, and a value just past it, or between the allowed
// ones, is refused by both.
func TestHandWrittenEffectTablesMatchTheValidator(t *testing.T) {
	for _, tc := range []struct {
		owner, field, value string
		want                bool
	}{
		{"cab", "drive", "0", true}, {"cab", "drive", "1", true}, {"cab", "drive", "-0.01", false}, {"cab", "drive", "1.01", false},
		{"cab", "tone", "0", true}, {"cab", "tone", "1", true}, {"cab", "tone", "-0.01", false}, {"cab", "tone", "1.01", false},
		{"cab", "output", "-24dB", true}, {"cab", "output", "12dB", true}, {"cab", "output", "-24.1dB", false}, {"cab", "output", "12.1dB", false},
		{"cab", "cabinet", "0", true}, {"cab", "cabinet", "2", true}, {"cab", "cabinet", "-1", false}, {"cab", "cabinet", "3", false},
		{"space", "mix", "0", true}, {"space", "mix", "1", true}, {"space", "mix", "-0.01", false}, {"space", "mix", "1.01", false},
		{"space", "partition", "64frames", true}, {"space", "partition", "2048frames", true}, {"space", "partition", "32frames", false}, {"space", "partition", "4096frames", false}, {"space", "partition", "96frames", false},
	} {
		table, validator := tableAccepts(tc.owner, tc.field, tc.value), validatorAccepts(tc.owner, tc.field, tc.value)
		if table != tc.want || validator != tc.want {
			t.Errorf("%s.%s = %s: the table takes it %v, the validator %v; want %v", tc.owner, tc.field, tc.value, table, validator, tc.want)
		}
	}
}
