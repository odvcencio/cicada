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

// Notes that start together with one length become a chord; a note that
// overlaps without matching gets another track; drums get a drums track.
func TestImportChordsOverlapsAndDrums(t *testing.T) {
	f := File{Format: 0, PPQ: 960, Tracks: []TrackChunk{{Notes: []Note{{Note: 60, Vel: 100, Dur: 960}, {Note: 64, Vel: 80, Dur: 960}, {Tick: 480, Note: 67, Vel: 90, Dur: 960}, {Note: 36, Vel: 127, Chan: 9, Dur: 120}}}}}
	source, report, err := ImportSource(f, ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Tracks != 3 || !strings.Contains(string(source), "[c4 e4]") || !strings.Contains(string(source), "bd:") {
		t.Fatalf("%+v: %s", report, source)
	}
}

// Each MIDI track keeps its own score tracks and its name, the drums share one
// track with a row per lane, and the conductor track names the score.
func TestImportKeepsMIDITracksAndNames(t *testing.T) {
	f := File{Format: 1, PPQ: 960, Tracks: []TrackChunk{
		{Name: "Harbor Song"},
		{Name: "Bass", Notes: []Note{{Note: 36, Vel: 100, Dur: 480}, {Tick: 960, Note: 43, Vel: 100, Dur: 480}}},
		{Name: "Lead Synth!", Notes: []Note{{Note: 72, Vel: 90, Dur: 960}, {Tick: 1920, Note: 74, Vel: 90, Dur: 960}}},
		{Name: "drums", Notes: []Note{{Note: 36, Vel: 127, Chan: 9, Dur: 120}, {Tick: 960, Note: 38, Vel: 100, Chan: 9, Dur: 120}, {Tick: 480, Note: 42, Vel: 70, Chan: 9, Dur: 120}}},
	}}
	source, report, err := ImportSource(f, ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, want := range []string{"title \"Harbor Song\"", "track bass piano", "track lead-synth piano", "track drums drums", "bd:", "sd:", "ch:"} {
		if !strings.Contains(text, want) {
			t.Fatalf("source lacks %q:\n%s", want, text)
		}
	}
	if report.Tracks != 3 || strings.Count(text, "track drums") != 1 {
		t.Fatalf("want bass, lead and one drums track: %+v\n%s", report, text)
	}
	score, ds := notation.Parse(source)
	if importErrors(ds) {
		t.Fatal(ds)
	}
	if p, ds := project.FromScore(score); p == nil {
		t.Fatal(ds)
	}
}

// A track name the score cannot use still imports.
func TestImportNameThatIsAKeywordStillImports(t *testing.T) {
	for _, name := range []string{"scene", "song", "track", "42 strings", "♫"} {
		f := File{Format: 1, PPQ: 960, Tracks: []TrackChunk{{Name: name, Notes: []Note{{Note: 60, Vel: 100, Dur: 960}}}}}
		source, _, err := ImportSource(f, ImportOptions{})
		if err != nil {
			t.Fatalf("name %q: %v", name, err)
		}
		if score, ds := notation.Parse(source); score == nil || importErrors(ds) {
			t.Fatalf("name %q: %+v\n%s", name, ds, source)
		}
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
