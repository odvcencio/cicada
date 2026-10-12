package smf

import (
	"bytes"
	"slices"
	"testing"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func placementMIDIProject(t *testing.T, source string) *project.Project {
	t.Helper()
	score, ds := notation.Parse([]byte(source))
	if score == nil || len(ds) != 0 {
		t.Fatalf("parse: %v", ds)
	}
	p, ds := project.FromScore(score)
	if p == nil || len(ds) != 0 {
		t.Fatalf("compile: %v", ds)
	}
	return p
}

func TestPlacementMIDIUsesLocalOriginAndExclusiveEnd(t *testing.T) {
	p := placementMIDIProject(t, `cicada 2
track bass acid {}
pattern motif { step = 1/8t gate = 100% c3 e3 g3 }
arrange {
  place one bass motif { at = 240ticks length = 1000ticks }
  place two bass motif { at = 2400ticks length = 480ticks }
  marker answer { at = 1200ticks }
}`)
	f, err := FromProject(p, 0)
	if err != nil {
		t.Fatal(err)
	}
	if f.Tracks[0].EndTick != 3840 {
		t.Fatalf("partial final bar was not included: %d", f.Tracks[0].EndTick)
	}
	want := []Note{
		{Track: 1, Tick: 240, Dur: 320, Note: 48, Vel: 100},
		{Track: 1, Tick: 560, Dur: 320, Note: 52, Vel: 100},
		{Track: 1, Tick: 880, Dur: 320, Note: 55, Vel: 100},
		{Track: 1, Tick: 1200, Dur: 40, Note: 48, Vel: 100},
		{Track: 1, Tick: 2400, Dur: 320, Note: 48, Vel: 100},
		{Track: 1, Tick: 2720, Dur: 160, Note: 52, Vel: 100},
	}
	if got := f.Tracks[1].Notes; !slices.Equal(got, want) {
		t.Fatalf("placement timing/pitches:\n%+v\nwant %+v", got, want)
	}
	marker := f.Tracks[0].Meta[3]
	if marker.Tick != 1200 || marker.Type != 0x06 || string(marker.Data) != "answer" {
		t.Fatalf("marker metadata: %+v", marker)
	}
	var wire bytes.Buffer
	if err := Encode(f, &wire); err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(&wire)
	if err != nil {
		t.Fatal(err)
	}
	if err := MusicalDiff(f, decoded); err != nil {
		t.Fatalf("placement MIDI changed on disk: %v", err)
	}
	loop, err := FromPattern(p, "motif")
	if err != nil || len(loop.Tracks) != 2 || loop.Tracks[1].Notes[0].Tick != 0 {
		t.Fatalf("independent pattern loop: %+v, %v", loop, err)
	}
	if p.Arrange == nil || len(p.Song) != 0 {
		t.Fatal("pattern export changed the source arrangement")
	}
}

func TestPlacementMIDIClosesTiedChordBeforeNextPlacement(t *testing.T) {
	p := placementMIDIProject(t, `cicada 2
instrument tone { voice poly { out = sine(pitch) * env(gate, 300ms) * 0.1 } }
track keys tone {}
pattern harmony notes { step = 1/4 gate = 100% [d4 f4 a4] - - - }
arrange {
  place first keys harmony { at = 0ticks length = 1440ticks }
  place next keys harmony { at = 1440ticks length = 240ticks }
}`)
	f, err := FromProject(p, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Tracks[1].Notes) != 6 {
		t.Fatalf("missing/repeated chord: %+v", f.Tracks[1].Notes)
	}
	for i, note := range f.Tracks[1].Notes {
		wantTick, wantDuration := int64(0), int64(1440)
		if i >= 3 {
			wantTick, wantDuration = 1440, 240
		}
		if note.Tick != wantTick || note.Dur != wantDuration || note.Note != []uint8{62, 65, 69}[i%3] {
			t.Fatalf("chord did not release/restart at placement boundary: %+v", note)
		}
	}
}

func TestPlacementMIDISwingRatchetsStayInsideRegion(t *testing.T) {
	p := placementMIDIProject(t, `cicada 2
track bass acid {}
pattern motif { swing = 75% c3 }
arrange { place one bass motif { at = 240ticks length = 200ticks } }`)
	p.Patterns[0].Data[0].Ratchet = 3
	f, err := FromProject(p, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []Note{{Track: 1, Tick: 360, Dur: 30, Note: 48, Vel: 100}, {Track: 1, Tick: 400, Dur: 30, Note: 48, Vel: 100}}
	if !slices.Equal(f.Tracks[1].Notes, want) {
		t.Fatalf("delayed ratchets escaped region: %+v", f.Tracks[1].Notes)
	}
}

func TestPlacementMIDIBarsLimitAndDrumChannel(t *testing.T) {
	p := placementMIDIProject(t, `cicada 2
track kit drums {}
pattern groove drums { bd: X }
arrange {
  place first kit groove { at = @1.3.1 length = 2bars }
  marker later { at = @3.1.1 }
}`)
	full, err := FromProject(p, 0)
	if err != nil || full.Tracks[0].EndTick != 11520 {
		t.Fatalf("full arrangement duration: %+v, %v", full, err)
	}
	f, err := FromProject(p, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Tracks[0].Meta) != 3 || len(f.Tracks[1].Notes) != 8 {
		t.Fatalf("range retained late markers or lost drums: %+v", f)
	}
	for _, note := range f.Tracks[1].Notes {
		if note.Chan != 9 || note.Note != 36 || note.Tick < 1920 || note.Tick+note.Dur > 3840 {
			t.Fatalf("invalid range/channel: %+v", note)
		}
	}
	for _, bars := range []int{-1, 4, 257} {
		if _, err := FromProject(p, bars); err == nil {
			t.Fatalf("accepted %d bars outside the arrangement", bars)
		}
	}
}
