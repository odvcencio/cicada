package project

import (
	"encoding/json"
	"m31labs.dev/cicada/notation"
	"strings"
	"testing"
)

func TestPerformedGesturesRespectPitchDerivedDelayBounds(t *testing.T) {
	const source = `instrument pluck { voice poly { out=comb(noise(),1 / pitch,0.8,0.3) } }
track keys pluck {}
pattern phrase performance length=3840ticks {
 note 48 at=17ticks length=703ticks velocity=82 {
  gesture at=0ticks bend=0ct pressure=0.2 timbre=0.5;
  gesture at=123ticks bend=0ct pressure=0.7 timbre=0.9 vibrato=0ct rate=5Hz;
 }
}
scene main { keys=phrase }
song { main*2 }
`
	for _, controls := range []string{
		"bend=-9600ct pressure=0.7 timbre=0.9 vibrato=0ct",
		"bend=9600ct pressure=0.7 timbre=0.9 vibrato=0ct",
		"bend=0ct pressure=0.7 timbre=0.9 vibrato=9600ct",
	} {
		bad := strings.Replace(source, "at=123ticks bend=0ct pressure=0.7 timbre=0.9 vibrato=0ct", "at=123ticks "+controls, 1)
		score, ds := notation.Parse([]byte(bad))
		if len(ds) > 0 {
			t.Fatal(ds)
		}
		if _, ds := Check(score); !hasErrors(ds) {
			t.Fatal("source accepted unsafe performed delay period", controls)
		}
	}
	score, ds := notation.Parse([]byte(source))
	p, ds := FromScore(score)
	if p == nil || len(ds) > 0 {
		t.Fatal(ds)
	}
	point := &p.Patterns[0].Performance.Notes[0].Expressions[1]
	point.Value.PitchCents = -9600
	if ValidateProject(p) == nil {
		t.Fatal("semantic gestures bypassed delay bounds")
	}
	data, _ := json.Marshal(p)
	if _, err := DecodeJSON(data); err == nil {
		t.Fatal("JSON gestures bypassed delay bounds")
	}
	point.Value.PitchCents = -3600
	if err := ValidateProject(p); err != nil {
		t.Fatal("valid 48 kHz period refused", err)
	}
	if _, err := CompileEngine(p, 96000, 128); err == nil {
		t.Fatal("render rate bypassed performed delay bounds")
	}
}
