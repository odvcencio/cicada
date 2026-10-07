package smf

import (
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"strings"
	"testing"
)

func TestImportTupletNotesPreservesTicksAndVelocity(t *testing.T) {
	f := File{Format: 1, PPQ: 480, TempoMicros: 500000, Tracks: []TrackChunk{{Notes: []Note{{Tick: 0, Dur: 88, Note: 60, Vel: 64}, {Tick: 160, Dur: 88, Note: 64, Vel: 100}, {Tick: 320, Dur: 88, Note: 67, Vel: 127}}}}}
	source, report, err := ImportSource(f, ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.GridTicks != 320 || report.QuantizedOnsets != 0 || report.QuantizedDurations != 0 {
		t.Fatalf("%+v", report)
	}
	score, ds := notation.Parse(source)
	if importErrors(ds) {
		t.Fatal(ds)
	}
	p, ds := project.FromScore(score)
	if p == nil {
		t.Fatal(ds)
	}
	exported, err := FromProject(p, 0)
	if err != nil {
		t.Fatal(err)
	}
	notes := exported.Tracks[1].Notes
	if len(notes) != 3 {
		t.Fatalf("%s: %+v", source, notes)
	}
	for i, n := range notes {
		if n.Tick != int64(i*320) || n.Dur != 176 || n.Vel != f.Tracks[0].Notes[i].Vel || n.Note != f.Tracks[0].Notes[i].Note {
			t.Fatalf("note %d: %+v", i, n)
		}
	}
	if _, err := project.ToSource(p); err != nil {
		t.Fatal(err)
	}
}

func TestImportPolyphonyAndDrums(t *testing.T) {
	f := File{Format: 0, PPQ: 960, Tracks: []TrackChunk{{Notes: []Note{{Note: 60, Vel: 100, Dur: 960}, {Note: 64, Vel: 80, Dur: 960}, {Note: 36, Vel: 127, Chan: 9, Dur: 120}}}}}
	source, report, err := ImportSource(f, ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Tracks != 3 || !strings.Contains(string(source), "bd:") {
		t.Fatalf("%+v: %s", report, source)
	}
}

func TestImportRequiresExplicitOnsetQuantization(t *testing.T) {
	f := File{PPQ: 1000, Tracks: []TrackChunk{{Notes: []Note{{Tick: 1, Note: 60, Vel: 100, Dur: 100}}}}}
	if _, _, err := ImportSource(f, ImportOptions{}); err == nil {
		t.Fatal("fractional onset accepted")
	}
	if _, report, err := ImportSource(f, ImportOptions{GridTicks: 240}); err != nil || report.QuantizedOnsets == 0 {
		t.Fatalf("%+v: %v", report, err)
	}
}

func TestImportRejectsTempoChanges(t *testing.T) {
	f := File{PPQ: 960, Tracks: []TrackChunk{{Meta: []MetaEvent{{Tick: 960, Type: 0x51}}}}}
	if _, _, err := ImportSource(f, ImportOptions{}); err == nil || !strings.Contains(err.Error(), "tempo changes") {
		t.Fatal(err)
	}
}
