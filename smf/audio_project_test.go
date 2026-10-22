package smf

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"testing"

	"m31labs.dev/cicada/project"
)

const mixedMIDISource = `cicada 2
title "Notes travel"
tempo 120
asset recorded "unavailable/recording.wav" { sha256 = "%s" format = wav frames = 48000 rate = 48000Hz channels = 1 }
clip pass recorded { start = 0frames end = 48000frames }
sampler hit { asset = recorded root = c3 mode = oneshot voices = 8 }
track vox audio {}
track chops hit {}
track bass acid {}
track kit drums {}
pattern melody notes steps=4 { gate = 100%% c3 . e3 . }
pattern pulse acid steps=4 { 1 . 5 . }
pattern beat drums steps=4 { bd: X . . . }
`

func mixedMIDIProject(t *testing.T, arrangement string) *project.Project {
	t.Helper()
	return placementMIDIProject(t, fmt.Sprintf(mixedMIDISource, strings.Repeat("a", 64))+arrangement)
}

func roundTripMixedMIDI(t *testing.T, file File) {
	t.Helper()
	var wire bytes.Buffer
	if err := Encode(file, &wire); err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(&wire)
	if err != nil {
		t.Fatal(err)
	}
	if err := MusicalDiff(file, decoded); err != nil {
		t.Fatal("mixed song changed on disk", err)
	}
}

func TestMIDIExportsNotesFromScenesWithRecordingsAndSampler(t *testing.T) {
	p := mixedMIDIProject(t, `scene verse { vox=pass chops=melody bass=pulse kit=beat }
scene tail { tempo=90 vox=keep chops=off bass=off kit=off }
song { verse tail*2 }
`)
	before, err := project.CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	file, err := FromProject(p, 0)
	if err != nil {
		t.Fatal("MIDI unnecessarily required decoded PCM", err)
	}
	if len(file.Tracks) != 5 || file.Tracks[0].EndTick != 3*3840 || len(file.Tracks[0].Meta) != 4 || file.Tracks[0].Meta[3].Tick != 3840 {
		t.Fatal("conductor lost the audio-bearing song's duration or tempo", file.Tracks[0])
	}
	if file.Tracks[1].Name != "vox" || len(file.Tracks[1].Notes) != 0 || file.Tracks[1].EndTick != 3*3840 {
		t.Fatal("audio was converted to invented MIDI notes", file.Tracks[1])
	}
	for i, channel := range []uint8{0, 1, 9} {
		track := file.Tracks[i+2]
		if len(track.Notes) == 0 {
			t.Fatal("lost instrument or sampler notes", track.Name)
		}
		for _, note := range track.Notes {
			if note.Chan != channel || note.Tick >= 3840 {
				t.Fatal("audio consumed a channel or scene-off lost its boundary", note)
			}
		}
	}
	roundTripMixedMIDI(t, file)
	after, err := project.CanonicalJSON(p)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("MIDI export mutated its source project", err)
	}
}

func TestMIDIKeepsAudioPlacementLengthAndMarkers(t *testing.T) {
	p := mixedMIDIProject(t, `arrange {
 place backing vox pass { at=0ticks length=3bars }
 place notes chops melody { at=240ticks length=1000ticks }
 marker ending { at=@3.1.1 }
}`)
	file, err := FromProject(p, 0)
	if err != nil {
		t.Fatal(err)
	}
	if file.Tracks[0].EndTick != 11520 || file.Tracks[0].Meta[3].Tick != 7680 || len(file.Tracks[1].Notes) != 0 {
		t.Fatal("omitting audio shortened the song or lost its marker", file.Tracks[0])
	}
	want := []Note{{Track: 2, Tick: 240, Dur: 240, Note: 48, Vel: 100}, {Track: 2, Tick: 720, Dur: 240, Note: 52, Vel: 100}, {Track: 2, Tick: 1200, Dur: 40, Note: 48, Vel: 100}}
	got := file.Tracks[2].Notes
	if !slices.Equal(got, want) {
		t.Fatalf("sampler note timing changed: %+v; want %+v", got, want)
	}
	roundTripMixedMIDI(t, file)
	limited, err := FromProject(p, 1)
	if err != nil || limited.Tracks[0].EndTick != 3840 || len(limited.Tracks[0].Meta) != 3 {
		t.Fatal("bar limit retained late audio markers", err)
	}
}

func TestMIDIAudioTracksDoNotUseMelodicChannels(t *testing.T) {
	var source strings.Builder
	fmt.Fprintf(&source, "cicada 2\nasset raw \"missing.wav\" { sha256 = %q format=wav frames=48000 rate=48000Hz channels=1 }\nclip pass raw { start=0frames end=48000frames }\n", strings.Repeat("a", 64))
	for i := 0; i < 15; i++ {
		fmt.Fprintf(&source, "track audio%d audio {}\n", i)
	}
	source.WriteString("track bass acid {}\npattern pulse { 1 . 5 . }\nscene verse { bass=pulse\n")
	for i := 0; i < 15; i++ {
		fmt.Fprintf(&source, "audio%d=pass\n", i)
	}
	source.WriteString("}\nsong { verse }\n")
	p := placementMIDIProject(t, source.String())
	file, err := FromProject(p, 0)
	if err != nil || len(file.Tracks) != 17 || len(file.Tracks[16].Notes) == 0 || file.Tracks[16].Notes[0].Chan != 0 {
		t.Fatal("recording tracks exhausted MIDI channels", err)
	}
	roundTripMixedMIDI(t, file)
}

func TestMIDIPatternChoosesANoteInstrumentInAudioProject(t *testing.T) {
	p := mixedMIDIProject(t, "scene main { vox=pass }\nsong { main }\n")
	file, err := FromPattern(p, "melody")
	if err != nil || len(file.Tracks) != 2 || file.Tracks[1].Name != "chops" || len(file.Tracks[1].Notes) != 8 {
		t.Fatal("unassigned pattern chose an audio track instead of the sampler", err)
	}
	roundTripMixedMIDI(t, file)
}

func TestMIDIPatternRetainsEditionOneDeclaredAudioInstrument(t *testing.T) {
	p := placementMIDIProject(t, `instrument audio { voice mono { out=sine(pitch)*env(gate,90ms) } }
track lead audio {}
pattern idea { c3 . e3 . }
scene main { lead=off }
song { main }
`)
	file, err := FromPattern(p, "idea")
	if err != nil || len(file.Tracks) != 2 || len(file.Tracks[1].Notes) == 0 {
		t.Fatal("an edition-1 authored instrument was mistaken for a recording track", err)
	}
}

func TestMIDIAudioOnlyProjectRetainsAnEmptyTimeline(t *testing.T) {
	p := mixedMIDIProject(t, "scene verse { vox=pass }\nsong { verse*3 }\n")
	p.Tracks = p.Tracks[:1]
	p.Patterns, p.Samplers = []project.Pattern{}, []project.Sampler{}
	clip := "pass"
	p.Tracks[0].Slots[0] = &clip
	p.Tracks[0].Chain = []string{clip}
	file, err := FromProject(p, 0)
	if err != nil || len(file.Tracks) != 2 || file.Tracks[0].EndTick != 11520 || len(file.Tracks[1].Notes) != 0 {
		t.Fatal("recording-only timeline created notes or lost its duration", err)
	}
	roundTripMixedMIDI(t, file)
}
