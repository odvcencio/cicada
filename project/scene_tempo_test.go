package project

import (
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
)

const tempoSteps = "cicada 2\ntempo 120\nfx delay delay { time = 1/2 feedback = 0.3 mix = 0.5 }\ntrack bass acid { send delay = 0.3 }\npattern riff acid steps=1 { 1 }\nscene a { bass = riff }\nscene b { bass = riff tempo = 60 }\nscene c { bass = riff }\nscene d { bass = riff tempo = 60 }\nscene e { bass = riff tempo = 90.5 }\nsong { a b*2 c d e }\n"

func tempoProject(t *testing.T, source string) (*Project, []notation.Diagnostic) {
	t.Helper()
	score, ds := notation.Parse([]byte(source))
	if score == nil {
		return nil, ds
	}
	return FromScore(score)
}

func TestSongTempoChangesCarryAndSkipRepeats(t *testing.T) {
	p, ds := tempoProject(t, tempoSteps)
	if p == nil {
		t.Fatal(ds)
	}
	// a:120; b sets 60 at bar 1; c inherits; d sets 60 again (no change); e sets 90.5 at bar 5.
	got := SongTempoChanges(p)
	want := []TempoChange{{Bar: 1, BPMMilli: 60_000}, {Bar: 5, BPMMilli: 90_500}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("changes = %+v, want %+v", got, want)
	}
	if SlowestTempoMilli(p) != 60_000 {
		t.Fatalf("slowest tempo = %d", SlowestTempoMilli(p))
	}
	m, err := NewTempoMap(48_000, int64(p.TempoMilli), got)
	if err != nil {
		t.Fatal(err)
	}
	// Bar 0 at 120 BPM is 96000 samples; bars 1 to 4 at 60 BPM are 192000 each.
	if m.SampleAtTick(seq.TicksPerBar) != 96_000 || m.SampleAtTick(5*seq.TicksPerBar) != 96_000+4*192_000 {
		t.Fatalf("map places bars at %d and %d", m.SampleAtTick(seq.TicksPerBar), m.SampleAtTick(5*seq.TicksPerBar))
	}
	if m.TickAtSample(m.SampleAtTick(3*seq.TicksPerBar+77)) != 3*seq.TicksPerBar+77 {
		t.Fatal("tick and sample do not round trip across steps")
	}
	// One bar at 120 BPM is 2 s, four bars at 60 BPM are 16 s, and one bar at
	// 90.5 BPM is 240/90.5 s.
	if seconds := SongSeconds(p, 6); seconds < 20.65 || seconds > 20.66 {
		t.Fatalf("song seconds = %g", seconds)
	}
	if seconds := SongSeconds(p, 1); seconds != 2 {
		t.Fatalf("one bar at 120 BPM = %g s", seconds)
	}
}

func TestSyncedDelayMustFitTheSlowestSceneTempo(t *testing.T) {
	// A half-note delay is 1 s at 120 BPM but 6 s at 20 BPM, which exceeds the 4 s buffer.
	slow := strings.Replace(tempoSteps, "tempo = 60 }\nscene c", "tempo = 20 }\nscene c", 1)
	if p, ds := tempoProject(t, slow); p != nil {
		if err := ValidateProject(p); err == nil || !strings.Contains(err.Error(), "4-second") {
			t.Fatalf("a delay that does not fit the slowest scene tempo: %v", err)
		}
	} else if len(ds) == 0 || !strings.Contains(ds[0].Message, "4-second") {
		t.Fatalf("diagnostics: %+v", ds)
	}
	// The same division set by a scene is checked against the slowest tempo too.
	division := strings.Replace(tempoSteps, "scene c { bass = riff }", "scene c { bass = riff delay.time = 1/2 }", 1)
	division = strings.Replace(division, "fx delay delay { time = 1/2", "fx delay delay { time = 1/8", 1)
	division = strings.Replace(division, "tempo = 60 }\nscene c", "tempo = 20 }\nscene c", 1)
	p, ds := tempoProject(t, division)
	if p != nil {
		if _, err := CompileEngine(p, 48_000, 128); err == nil || !strings.Contains(err.Error(), "4-second") {
			t.Fatalf("a scene division that does not fit the slowest tempo: %v", err)
		}
	} else if len(ds) == 0 || !strings.Contains(ds[0].Message, "4-second") {
		t.Fatalf("diagnostics: %+v", ds)
	}
	// At 60 BPM the half note is 2 s and fits.
	if p, ds := tempoProject(t, tempoSteps); p == nil {
		t.Fatal(ds)
	} else if _, err := CompileEngine(p, 48_000, 128); err != nil {
		t.Fatal(err)
	}
}
