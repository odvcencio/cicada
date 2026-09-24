package smf

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func firstAcidProject(t *testing.T) *project.Project {
	t.Helper()
	source, err := os.ReadFile(filepath.Join("..", "examples", "first-acid.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	score, diagnostics := notation.Parse(source)
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			t.Fatalf("parse: %+v", diagnostic)
		}
	}
	p, diagnostics := project.FromScore(score)
	if p == nil {
		t.Fatalf("compile: %+v", diagnostics)
	}
	return p
}

func sortedNotes(notes []Note) []Note {
	copyOf := slices.Clone(notes)
	slices.SortFunc(copyOf, func(a, b Note) int {
		for _, cmp := range []int{
			compareInt64(a.Tick, b.Tick), compareInt64(a.Dur, b.Dur),
			int(a.Note) - int(b.Note), int(a.Vel) - int(b.Vel),
			int(a.Chan) - int(b.Chan), int(a.Track) - int(b.Track),
		} {
			if cmp != 0 {
				return cmp
			}
		}
		return 0
	})
	return copyOf
}

func compareInt64(a, b int64) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func TestFirstAcidTypeOneRoundTrip(t *testing.T) {
	p := firstAcidProject(t)
	file, err := FromProject(p, 16)
	if err != nil {
		t.Fatal(err)
	}
	if file.PPQ != 960 || len(file.Tracks) != len(p.Tracks)+1 || file.TempoMicros != 434783 {
		t.Fatalf("unexpected MIDI header: %+v", file)
	}
	if file.Tracks[0].Name != p.Title || len(file.Tracks[0].Meta) != 3 {
		t.Fatal("missing conductor metadata")
	}
	var slide bool
	for i, track := range file.Tracks[1:] {
		if len(track.Notes) == 0 || track.Name != p.Tracks[i].ID {
			t.Fatalf("missing notes on %s", p.Tracks[i].ID)
		}
		for _, note := range track.Notes {
			if note.Track != uint8(i+1) || note.Dur < 1 || note.Tick < 0 {
				t.Fatalf("invalid note: %+v", note)
			}
			if track.Name == "drums" && note.Chan != 9 {
				t.Fatalf("drums on channel %d", note.Chan)
			}
			if track.Name == "bass" {
				for _, next := range track.Notes {
					if next.Tick > note.Tick && note.Tick+note.Dur == next.Tick+1 {
						slide = true
					}
				}
			}
		}
	}
	if !slide {
		t.Fatal("source slide did not overlap target by one tick")
	}
	var encoded bytes.Buffer
	if err := Encode(file, &encoded); err != nil {
		t.Fatal(err)
	}
	golden, err := os.ReadFile(filepath.Join("..", "testdata", "golden", "first-acid.mid"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded.Bytes(), golden) {
		t.Fatal("first-acid MIDI bytes differ from pinned type 1 fixture")
	}
	if string(encoded.Bytes()[:4]) != "MThd" || !bytes.Contains(encoded.Bytes(), []byte{0xff, 0x2f, 0}) {
		t.Fatal("missing SMF header or end-of-track")
	}
	decoded, err := Decode(bytes.NewReader(encoded.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.PPQ != file.PPQ || decoded.TempoMicros != file.TempoMicros || len(decoded.Tracks) != len(file.Tracks) {
		t.Fatalf("SMF header changed: %+v", decoded)
	}
	for i := range file.Tracks {
		if file.Tracks[i].Name != decoded.Tracks[i].Name || !slices.Equal(sortedNotes(file.Tracks[i].Notes), sortedNotes(decoded.Tracks[i].Notes)) {
			t.Fatalf("track %d event set changed on SMF round trip", i)
		}
	}
	if err := MusicalDiff(file, decoded); err != nil {
		t.Fatalf("musical event set changed: %v", err)
	}
	slices.Reverse(decoded.Tracks[1].Notes)
	if err := MusicalDiff(file, decoded); err != nil {
		t.Fatalf("event ordering changed musical diff: %v", err)
	}
	decoded.Tracks[1].Notes[0].Vel++
	if err := MusicalDiff(file, decoded); err == nil {
		t.Fatal("velocity change escaped musical diff")
	}
}

func TestPatternExportAndMIDIValidation(t *testing.T) {
	p := firstAcidProject(t)
	file, err := FromPattern(p, "bass-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Tracks) != 2 || file.Tracks[1].Name != "bass" || file.Tracks[0].EndTick != 3840 || len(file.Tracks[1].Notes) == 0 {
		t.Fatalf("unexpected pattern export: %+v", file)
	}
	if _, err := FromPattern(p, "missing"); err == nil {
		t.Fatal("missing pattern accepted")
	}
	file.Tracks[1].Notes[0].Dur = 0
	if err := Encode(file, &bytes.Buffer{}); err == nil {
		t.Fatal("zero duration MIDI note accepted")
	}
}

func TestPatternExportPreservesProbabilityIdentity(t *testing.T) {
	for _, sourceSlot := range []int{0, 7} {
		t.Run(fmt.Sprintf("slot-%d", sourceSlot), func(t *testing.T) {
			p := firstAcidProject(t)
			for i := range p.Patterns {
				if p.Patterns[i].ID == "beat-a" {
					p.Patterns[i].Lanes["bd"][0].Probability = 50
				}
			}
			if p.Tracks[1].Slots[0] == nil || *p.Tracks[1].Slots[0] != "beat-a" {
				t.Fatal("expected beat-a in drums slot zero")
			}
			if sourceSlot != 0 {
				p.Tracks[1].Slots[sourceSlot] = p.Tracks[1].Slots[0]
				p.Tracks[1].Slots[0] = nil
			}
			full, err := FromProject(p, 1)
			if err != nil {
				t.Fatal(err)
			}
			single, err := FromPattern(p, "beat-a")
			if err != nil {
				t.Fatal(err)
			}
			want := slices.Clone(full.Tracks[2].Notes)
			for i := range want {
				want[i].Track = 1
			}
			if !slices.Equal(want, single.Tracks[1].Notes) {
				t.Fatalf("pattern notes differ from arrangement drums: want %+v, got %+v", want, single.Tracks[1].Notes)
			}
		})
	}
}

func TestMIDIUsesFirstProbabilityPassAndRatchetTicks(t *testing.T) {
	p := firstAcidProject(t)
	var bass *project.Pattern
	for i := range p.Patterns {
		if p.Patterns[i].ID == "bass-a" {
			bass = &p.Patterns[i]
			break
		}
	}
	if bass == nil || bass.Data[0] == nil {
		t.Fatal("missing bass-a first step")
	}
	bass.Data[0].Ratchet = 3
	file, err := FromProject(p, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, onset := range []int64{0, 86, 172, 3840, 3926, 4012} {
		found := false
		for _, note := range file.Tracks[1].Notes {
			found = found || note.Tick == onset
		}
		if !found {
			t.Fatalf("missing ratchet onset %d", onset)
		}
	}
	bass.Data[0].Ratchet = 1
	bass.Data[0].Probability = 50
	file, err = FromProject(p, 2)
	if err != nil {
		t.Fatal(err)
	}
	first, second := false, false
	for _, note := range file.Tracks[1].Notes {
		first = first || note.Tick == 0
		second = second || note.Tick == 3840
	}
	if first != second {
		t.Fatal("probability changed between repeated bars despite iteration-zero MIDI rule")
	}
}

func TestDecodeTypeZeroAndRunningStatus(t *testing.T) {
	track := []byte{
		0, 0xff, 0x51, 3, 7, 0xa1, 0x20,
		0, 0x90, 60, 100,
		0x81, 0x70, 62, 100, // running note-on after 240 ticks
		0, 0x80, 60, 0,
		0x81, 0x70, 62, 0, // running note-off after another 240 ticks
		0, 0xff, 0x2f, 0,
	}
	header := []byte{'M', 'T', 'h', 'd', 0, 0, 0, 6, 0, 0, 0, 1, 3, 0xc0}
	data := append(header, []byte{'M', 'T', 'r', 'k', 0, 0, 0, byte(len(track))}...)
	data = append(data, track...)
	decoded, err := Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Format != 0 || decoded.PPQ != 960 || decoded.TempoMicros != 500_000 || len(decoded.Tracks) != 1 || len(decoded.Tracks[0].Notes) != 2 {
		t.Fatalf("unexpected type zero decode: %+v", decoded)
	}
	if !slices.Equal(sortedNotes(decoded.Tracks[0].Notes), []Note{
		{Track: 0, Tick: 0, Dur: 240, Note: 60, Vel: 100, Chan: 0},
		{Track: 0, Tick: 240, Dur: 240, Note: 62, Vel: 100, Chan: 0},
	}) {
		t.Fatalf("wrong running-status note intervals: %+v", decoded.Tracks[0].Notes)
	}
}
