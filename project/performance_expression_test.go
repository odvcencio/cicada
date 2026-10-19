package project

import (
	"encoding/json"
	"reflect"
	"testing"

	"m31labs.dev/cicada/notation"
)

func TestPerformedGesturesSurviveSourceJSONAndFormatting(t *testing.T) {
	const source = `cicada 2
track lead acid {}
pattern phrase performance length=3840ticks {
 note 60 at=17ticks length=703ticks velocity=82 {
  gesture at=0ticks bend=0ct pressure=0.2 timbre=0.5;
  gesture at=123ticks bend=123.5ct pressure=0.7 timbre=0.9 vibrato=5ct;
  gesture at=456ticks bend=-30ct pressure=0.4 timbre=0.2 vibrato=7ct rate=7.25Hz;
 }
}

scene main { lead=phrase }
song { main*2 }
`
	score, ds := notation.Parse([]byte(source))
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	p, ds := FromScore(score)
	if p == nil || len(ds) != 0 {
		t.Fatal(ds)
	}
	points := p.Patterns[0].Performance.Notes[0].Expressions
	if len(points) != 3 || points[1].Tick != 123 || points[1].Value.PitchCents != 123.5 || points[1].Value.Pressure != .7 || points[1].Value.VibratoDepthCents != 5 || points[1].VibratoRateHz != 5 || points[2].VibratoRateHz != 7.25 {
		t.Fatal("gesture source changed", points)
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := DecodeJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := ToSource(restored)
	if err != nil {
		t.Fatal(err)
	}
	document, err := notation.ParseDocument(canonical)
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := notation.Format(document)
	if err != nil {
		t.Fatal(err)
	}
	againScore, ds := notation.Parse(formatted)
	if len(ds) != 0 {
		t.Fatalf("format: %v\n%s", ds, formatted)
	}
	again, ds := FromScore(againScore)
	if again == nil || len(ds) != 0 || !reflect.DeepEqual(again.Patterns[0].Performance, p.Patterns[0].Performance) {
		t.Fatalf("round trip changed gestures: %v\n%s", ds, formatted)
	}
}
