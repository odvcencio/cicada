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

// A drum kit whose rows repeat on different cycles needs more than 16 bar
// patterns as one track; the import then splits it into a track per drum.
func TestImportSplitsAKitThatNeedsTooManyBarPatterns(t *testing.T) {
	var notes []Note
	for bar := int64(0); bar < 20; bar++ {
		start := bar * 3840
		notes = append(notes, Note{Tick: start + (bar%5)*240, Note: 36, Vel: 127, Chan: 9, Dur: 120})
		notes = append(notes, Note{Tick: start + 480 + (bar%4)*240, Note: 42, Vel: 100, Chan: 9, Dur: 120})
	}
	f := File{Format: 1, PPQ: 960, Tracks: []TrackChunk{{Name: "kit", Notes: notes}}}
	source, report, err := ImportSource(f, ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Tracks != 2 || strings.Count(string(source), " drums {}") != 2 {
		t.Fatalf("want one drums track per drum: %+v\n%s", report, source)
	}
}

// Two hits of one drum in the same step keep the louder and are counted.
func TestImportMergesDrumHitsInOneStep(t *testing.T) {
	f := File{Format: 0, PPQ: 960, Tracks: []TrackChunk{{Notes: []Note{
		{Tick: 0, Note: 36, Vel: 70, Chan: 9, Dur: 120}, {Tick: 0, Note: 36, Vel: 127, Chan: 9, Dur: 120},
		{Tick: 960, Note: 42, Vel: 100, Chan: 9, Dur: 60}, {Tick: 990, Note: 42, Vel: 100, Chan: 9, Dur: 60},
	}}}}
	source, report, err := ImportSource(f, ImportOptions{GridTicks: 240})
	if err != nil {
		t.Fatal(err)
	}
	if report.MergedHits != 2 || !strings.Contains(string(source), "bd: X") {
		t.Fatalf("want two merged hits and the louder kick: %+v\n%s", report, source)
	}
}

// Parts on different channels of one MIDI track stay separate tracks.
func TestImportKeepsChannelsApart(t *testing.T) {
	f := File{Format: 0, PPQ: 960, Tracks: []TrackChunk{{Notes: []Note{
		{Tick: 0, Note: 36, Vel: 110, Chan: 0, Dur: 960}, {Tick: 0, Note: 72, Vel: 60, Chan: 1, Dur: 960},
	}}}}
	source, report, err := ImportSource(f, ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Tracks != 2 || report.QuantizedVelocities != 0 || strings.Contains(string(source), "[") {
		t.Fatalf("want two single-note tracks with their own velocities: %+v\n%s", report, source)
	}
}

// Every chord note whose velocity changes is counted.
func TestImportCountsEachChordVelocityEdit(t *testing.T) {
	f := File{Format: 0, PPQ: 960, Tracks: []TrackChunk{{Notes: []Note{
		{Note: 60, Vel: 80, Dur: 960}, {Note: 64, Vel: 80, Dur: 960}, {Note: 67, Vel: 100, Dur: 960},
	}}}}
	_, report, err := ImportSource(f, ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.QuantizedVelocities != 2 {
		t.Fatalf("want 2 velocity edits, got %+v", report)
	}
}

// A long name is shortened, and a name the score cannot use falls back to
// midi-N for that track only.
func TestImportShortensLongNamesAndFallsBackPerTrack(t *testing.T) {
	long := strings.Repeat("strings ", 10)
	f := File{Format: 1, PPQ: 960, Tracks: []TrackChunk{
		{Name: long, Notes: []Note{{Note: 60, Vel: 100, Dur: 960}}},
		{Name: "track", Notes: []Note{{Note: 64, Vel: 100, Dur: 960}}},
		{Name: "Bass", Notes: []Note{{Note: 36, Vel: 100, Dur: 960}}},
	}}
	source, _, err := ImportSource(f, ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	if !strings.Contains(text, "track strings-strings") || !strings.Contains(text, "track bass piano") {
		t.Fatalf("named tracks lost their names:\n%s", text)
	}
}

// Only the hit that plays counts edits, so the report does not depend on the
// order of two hits that merge.
func TestImportCountsEditsOnlyForTheHitThatPlays(t *testing.T) {
	quiet, loud := Note{Note: 36, Vel: 50, Chan: 9, Dur: 120}, Note{Note: 36, Vel: 60, Chan: 9, Dur: 120}
	var reports []ImportReport
	for _, pair := range [][]Note{{quiet, loud}, {loud, quiet}} {
		_, report, err := ImportSource(File{Format: 0, PPQ: 960, Tracks: []TrackChunk{{Notes: pair}}}, ImportOptions{})
		if err != nil {
			t.Fatal(err)
		}
		reports = append(reports, report)
	}
	if reports[0] != reports[1] || reports[0].MergedHits != 1 || reports[0].QuantizedVelocities != 1 {
		t.Fatalf("reports differ by note order or miscount: %+v vs %+v", reports[0], reports[1])
	}
}

// A kit that needs splitting splits only its own MIDI track; another track's
// chords stay chords.
func TestImportSplitsOnlyTheTrackThatNeedsIt(t *testing.T) {
	var kit []Note
	for bar := int64(0); bar < 20; bar++ {
		start := bar * 3840
		kit = append(kit, Note{Tick: start + (bar%5)*240, Note: 36, Vel: 127, Chan: 9, Dur: 120})
		kit = append(kit, Note{Tick: start + 480 + (bar%4)*240, Note: 42, Vel: 100, Chan: 9, Dur: 120})
	}
	pad := []Note{{Note: 60, Vel: 90, Dur: 3840}, {Note: 64, Vel: 90, Dur: 3840}, {Note: 67, Vel: 90, Dur: 3840}}
	f := File{Format: 1, PPQ: 960, Tracks: []TrackChunk{{Name: "kit", Notes: kit}, {Name: "pad", Notes: pad}}}
	source, report, err := ImportSource(f, ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Tracks != 3 || !strings.Contains(string(source), "[c4 e4 g4]") {
		t.Fatalf("want two drum tracks and one pad chord track: %+v\n%s", report, source)
	}
}
