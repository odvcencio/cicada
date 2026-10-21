package edit_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
)

func TestBuiltinSoundParameterEditsPreserveSourceAndValidateCatalog(t *testing.T) {
	source := `cicada 2
// keep this arrangement
track bass acid { cutoff = 723.25Hz } // resting tone
track beat drums { bd_decay = // preserve this annotation
  400ms }
pattern p { 1 . 5 . }
pattern d drums { bd: x . . . }
scene main { bass=p beat=d }
song { main }
`
	for _, newline := range []string{"\n", "\r\n"} {
		before := []byte(strings.ReplaceAll(source, "\n", newline))
		apply := func(track, param, value string, reset bool) ([]byte, error) {
			var raw json.RawMessage
			if !reset {
				raw, _ = json.Marshal(value)
			}
			r, err := edits.Apply(before, edits.Envelope{Version: edits.EnvelopeVersion, Intents: []edits.Intent{&edits.SetParam{Entity: edits.EntityID("param:" + track + "." + param), Value: raw}}}, edits.Options{Compiler: m5Compiler{}, ParamWriter: edits.ParamWriterInstrument})
			if err != nil {
				return nil, err
			}
			return r.Source, nil
		}
		updated, err := apply("bass", "cutoff", "1250.125", false)
		if err != nil {
			t.Fatal(err)
		}
		want := bytes.Replace(before, []byte("cutoff = 723.25Hz"), []byte("cutoff = 1250.125Hz"), 1)
		if !bytes.Equal(updated, want) {
			t.Fatalf("builtin edit changed unrelated source:\n%s", updated)
		}
		updated, err = apply("beat", "bd_decay", "0.75s", false)
		if err != nil || !bytes.Equal(updated, bytes.Replace(before, []byte("  400ms }"), []byte("  0.75s }"), 1)) {
			t.Fatalf("duration/comment preservation: %v\n%s", err, updated)
		}
		reset, err := apply("bass", "cutoff", "", true)
		if err != nil || !bytes.Equal(reset, bytes.Replace(before, []byte("cutoff = 723.25Hz"), nil, 1)) {
			t.Fatalf("builtin reset: %v\n%s", err, reset)
		}
		if _, err := apply("beat", "bd_decay", "", true); err == nil || !strings.Contains(err.Error(), "preserve the annotation") {
			t.Fatalf("reset lost comment guard: %v", err)
		}
		for _, bad := range [][3]string{{"bass", "cutoff", "10ms"}, {"bass", "cutoff", "9000Hz"}, {"bass", "filter", "unknown"}, {"bass", "octave", "2.5"}, {"bass", "level", "-6dB"}, {"beat", "bd_tune", "10Hz"}, {"beat", "ch_metal", "maybe"}, {"beat", "nope", "1"}} {
			if _, err := apply(bad[0], bad[1], bad[2], false); err == nil {
				t.Errorf("accepted invalid built-in parameter %v", bad)
			}
		}
	}
}
