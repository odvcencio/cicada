package smf

import (
	"bytes"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestPerformanceMIDIExportsIndependentCrossBarGates(t *testing.T) {
	source := `track keys piano {}
pattern phrase performance length=7680ticks {
 note 60 at=17ticks length=4090ticks velocity=82;
 note 64 at=19ticks length=1201ticks velocity=105;
 note 67 at=4050ticks length=513ticks velocity=47;
}

scene main { keys=phrase }
song { main*4 }
`
	score, ds := notation.Parse([]byte(source))
	p, ds := project.FromScore(score)
	if p == nil {
		t.Fatal(ds)
	}
	file, err := FromProject(p, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Tracks[1].Notes) != 6 {
		t.Fatalf("lost MIDI notes: %+v", file.Tracks[1].Notes)
	}
	for iteration := int64(0); iteration < 2; iteration++ {
		for _, want := range []Note{{Tick: 17, Dur: 4090, Note: 60, Vel: 82}, {Tick: 19, Dur: 1201, Note: 64, Vel: 105}, {Tick: 4050, Dur: 513, Note: 67, Vel: 47}} {
			found := false
			for _, n := range file.Tracks[1].Notes {
				found = found || n.Tick == iteration*7680+want.Tick && n.Dur == want.Dur && n.Note == want.Note && n.Vel == want.Vel
			}
			if !found {
				t.Fatal("MIDI changed performed timing or velocity", iteration, want)
			}
		}
	}
	var encoded bytes.Buffer
	if err := Encode(file, &encoded); err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(&encoded)
	if err != nil || len(decoded.Tracks[1].Notes) != 6 || !hasPerformedGate(decoded.Tracks[1].Notes, 17, 4090, 60, 82) {
		t.Fatal("performed SMF roundtrip failed", err)
	}
	selected, err := FromPattern(p, "phrase")
	if err != nil || selected.Tracks[0].EndTick != 7680 || len(selected.Tracks[1].Notes) != 3 {
		t.Fatal("pattern export shortened the loop", err)
	}
}

func TestPerformanceMIDIPlacementOriginsAndExclusiveEnd(t *testing.T) {
	score, ds := notation.Parse([]byte(`cicada 2
track keys piano {}
pattern phrase performance length=7680ticks { note 60 at=17ticks length=4090ticks velocity=82; note 64 at=19ticks length=1201ticks velocity=105; }
arrange { place a keys phrase { at=3840ticks length=3840ticks } place b keys phrase { at=11520ticks length=7680ticks } }
`))
	p, ds := project.FromScore(score)
	if p == nil {
		t.Fatal(ds)
	}
	file, err := FromProject(p, 0)
	if err != nil {
		t.Fatal(err)
	}
	n := file.Tracks[1].Notes
	if len(n) != 4 || n[0].Tick != 3857 || n[0].Dur != 3823 || n[2].Tick != 11537 || n[2].Dur != 4090 {
		t.Fatalf("placement timing or release differs: %+v", n)
	}
}

func hasPerformedGate(notes []Note, tick, duration int64, pitch, velocity uint8) bool {
	for _, n := range notes {
		if n.Tick == tick && n.Dur == duration && n.Note == pitch && n.Vel == velocity {
			return true
		}
	}
	return false
}

func TestPerformedMIDIClosesChainsAndChasesSceneEntry(t *testing.T) {
	base := `track keys piano { CHAIN }
pattern phrase performance length=7680ticks { note 60 at=17ticks length=5983ticks velocity=82; }
scene a { keys=FIRST }
scene b { keys=SECOND }
song { a b }
`
	for _, test := range []struct {
		name, chain, first, second string
		tick, duration             int64
	}{
		{"chain stops", "chain=phrase", "keep", "off", 17, 3823},
		{"scene entry chases", "", "off", "phrase", 3840, 2160},
		{"chain keeps phase", "chain=phrase", "keep", "phrase", 17, 5983},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := strings.NewReplacer("CHAIN", test.chain, "FIRST", test.first, "SECOND", test.second).Replace(base)
			score, ds := notation.Parse([]byte(source))
			p, ds := project.FromScore(score)
			if p == nil {
				t.Fatal(ds)
			}
			file, err := FromProject(p, 0)
			if err != nil {
				t.Fatal(err)
			}
			n := file.Tracks[1].Notes
			if len(n) != 1 || n[0].Tick != test.tick || n[0].Dur != test.duration {
				t.Fatalf("MIDI differs from performance lifecycle: %+v", n)
			}
		})
	}
}
