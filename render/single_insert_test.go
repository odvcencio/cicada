package render

import (
	"bytes"
	"testing"

	"m31labs.dev/cicada/notation"
)

var singleInserts = map[string]string{
	"drive":   "fx x drive {}",
	"eq":      "fx x eq { type = peak frequency = 1000Hz gain = 6dB q = 0.7 }",
	"comp":    "fx x comp {}",
	"deesser": "fx x deesser {}",
	"ampcab":  "fx x ampcab {}",
	"chorus":  "fx x chorus { voices = 2 rate = 0.17Hz depth = 2.4ms spread = 0.7 mix = 0.5 }",
}

// singleInsertScore declares one effect of the given kind and inserts it on
// the only track; an empty kind leaves the track without an insert.
func singleInsertScore(t *testing.T, kind string) *notation.Score {
	t.Helper()
	declaration, insert := "", ""
	if kind != "" {
		declaration, insert = singleInserts[kind]+"\n", " insert = x"
	}
	source := `cicada 2
tempo 120
instrument pad { voice poly { out = saw(pitch) * adsr(gate, 5ms, 100ms, 0.8, 100ms) * velocity * 0.2 } }
` + declaration + `track a pad { level = 0dB` + insert + ` }
pattern p { step = 1/4 [a3 c#4 e4] - - - }
scene s { a = p }
song { s*2 }
`
	score, ds := notation.Parse([]byte(source))
	for _, d := range ds {
		if d.Severity == "error" {
			t.Fatal(d.Error())
		}
	}
	return score
}

// The direct mixer builds only a drive insert. A single insert of any other
// kind renders through the engine, which runs every documented track insert,
// so cicada render plays what Studio plays. The direct mixer used to render a
// single chorus as if the track had no insert.
func TestSingleTrackInsertPlaysOnEveryRenderPath(t *testing.T) {
	for kind := range singleInserts {
		if got, want := UsesScheduledEngine(singleInsertScore(t, kind), Options{}), kind != "drive"; got != want {
			t.Errorf("single %s insert: scheduled engine %v, want %v", kind, got, want)
		}
	}
	render := func(score *notation.Score) []byte {
		var out bytes.Buffer
		if _, err := WAV(score, Options{Bits: 32, TailSec: 0.1}, &out); err != nil {
			t.Fatal(err)
		}
		return out.Bytes()
	}
	dry := render(singleInsertScore(t, ""))
	chorus := render(singleInsertScore(t, "chorus"))
	if bytes.Equal(dry, chorus) {
		t.Fatal("a single chorus insert rendered the same as no insert")
	}
}
