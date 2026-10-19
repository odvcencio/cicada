package smf

import (
	"bytes"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func gestureFile() File {
	gesture := func(cents float32) *NoteGesture {
		return &NoteGesture{Points: []seq.PerformedExpression{
			{Params: seq.Expression{Set: true, PitchCents: cents, Pressure: .25, Timbre: .75}},
			{Tick: 123, Params: seq.Expression{Set: true, PitchCents: -cents, Pressure: .8, Timbre: .1}},
		}}
	}
	return File{PPQ: 960, Tracks: []TrackChunk{{Name: "Glass", EndTick: 1920, Notes: []Note{
		{Tick: 17, Dur: 703, Note: 60, Vel: 82, Gesture: gesture(100)},
		{Tick: 19, Dur: 1201, Note: 64, Vel: 105, Gesture: gesture(200)},
		{Tick: 720, Dur: 300, Note: 67, Vel: 50},
	}}}}
}

func TestGestureMIDIChannelsTimingAndRoundTrip(t *testing.T) {
	file := gestureFile()
	before := append([]seq.PerformedExpression(nil), file.Tracks[0].Notes[0].Gesture.Points...)
	out, err := prepareGestureMIDI(file)
	if err != nil {
		t.Fatal(err)
	}
	factor := int64(out.PPQ / file.PPQ)
	if out.PPQ != 32640 || len(out.Tracks) != 1 {
		t.Fatal(out.PPQ, len(out.Tracks))
	}
	track := out.Tracks[0]
	if track.Notes[0].Chan != 1 || track.Notes[1].Chan != 2 || track.Notes[2].Chan != 1 {
		t.Fatal("overlap or exact note-off reuse lost isolation", track.Notes)
	}
	for i, n := range track.Notes {
		original := file.Tracks[0].Notes[i]
		if n.Tick != original.Tick*factor || n.Dur != original.Dur*factor || n.Gesture != nil {
			t.Fatal(n)
		}
	}
	if midiPortAt(track, 0) == "\x00" || len(track.Meta) != 2 || track.Meta[0].Type != 0x09 || track.Meta[1].Type != 0x21 {
		t.Fatal("missing device/port routing", track.Meta)
	}
	// Independent bends at the two original gesture timestamps, not a shared
	// track controller. The non-expressive reused channel resets its state.
	for _, check := range []struct {
		tick    int64
		channel byte
		bend    int
	}{
		{17, 1, 12288}, {140, 1, 4096}, {19, 2, 16383}, {142, 2, 0}, {720, 1, 8192},
	} {
		found := false
		for _, event := range track.Events {
			found = found || event.Tick == check.tick*factor && event.Status == 0xe0|check.channel && int(event.Data1)+int(event.Data2)*128 == check.bend
		}
		if !found {
			t.Fatal("missing independent bend", check)
		}
	}
	if !reflect.DeepEqual(file.Tracks[0].Notes[0].Gesture.Points, before) || file.PPQ != 960 {
		t.Fatal("export mutated source")
	}
	var encoded bytes.Buffer
	if err := Encode(file, &encoded); err != nil {
		t.Fatal(err)
	}
	wire := encoded.Bytes()
	// At the reused boundary, off precedes controller reset, then the new on.
	off, reset, on := bytes.Index(wire, []byte{0x81, 60, 0}), bytes.LastIndex(wire, []byte{0xe1, 0, 64}), bytes.Index(wire, []byte{0x91, 67, 50})
	if off < 0 || reset <= off || on <= reset {
		t.Fatal("incorrect channel reuse ordering", off, reset, on)
	}
	decoded, err := Decode(bytes.NewReader(wire))
	if err != nil {
		t.Fatal(err)
	}
	if err := MusicalDiff(file, decoded); err != nil {
		t.Fatal(err)
	}
	decoded.Tracks[0].Events[len(decoded.Tracks[0].Events)-1].Data1 ^= 1
	if MusicalDiff(file, decoded) == nil {
		t.Fatal("diff ignored controller change")
	}
	decoded, _ = Decode(bytes.NewReader(wire))
	decoded.Tracks[0].Meta[len(decoded.Tracks[0].Meta)-1].Data[0]++
	if MusicalDiff(file, decoded) == nil {
		t.Fatal("diff ignored port change")
	}
	if _, _, err := ImportSource(out, ImportOptions{}); err == nil || !strings.Contains(err.Error(), "without loss") {
		t.Fatal("import silently discarded gestures", err)
	}
}

func TestGestureMIDISpillsToIndependentNamedPorts(t *testing.T) {
	file := gestureFile()
	first := file.Tracks[0].Notes[0]
	file.Tracks[0].Notes = nil
	for i := 0; i < 31; i++ {
		note := first
		note.Note, note.Tick = byte(48+i), 17
		file.Tracks[0].Notes = append(file.Tracks[0].Notes, note)
	}
	out, err := prepareGestureMIDI(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Tracks) != 3 {
		t.Fatal("lost MPE voices", len(out.Tracks))
	}
	ports := map[string]bool{}
	for i, track := range out.Tracks {
		port := midiPortAt(track, 0)
		if ports[port] || len(track.Notes) != min(15, 31-i*15) {
			t.Fatal("shared port or missing notes", i)
		}
		ports[port] = true
		for j, n := range track.Notes {
			if int(n.Chan) != j+1 {
				t.Fatal(n)
			}
		}
	}
}

func TestGestureMIDIVibratoUsesSecondsAcrossTempoChanges(t *testing.T) {
	file := File{PPQ: 960, Tracks: []TrackChunk{{Meta: []MetaEvent{{Type: 0x51, Data: []byte{7, 161, 32}}, {Tick: 960, Type: 0x51, Data: []byte{15, 66, 64}}}}}}
	tempo, err := midiTempoMap(file)
	if err != nil {
		t.Fatal(err)
	}
	note := Note{Dur: 1920, Gesture: &NoteGesture{Points: []seq.PerformedExpression{{Params: seq.Expression{Set: true, Timbre: .5, VibratoRateHz: 2, VibratoDepthCents: 100}}}}}
	budget := maxGestureMIDIEvents
	samples, sensitivity, err := midiGestureSamples(note, tempo, file.PPQ, &budget)
	if err != nil || sensitivity != 2 || len(samples) < 350 {
		t.Fatal(err, sensitivity, len(samples))
	}
	for _, sample := range samples {
		want := 100 * math.Sin(2*math.Pi*2*midiSeconds(tempo, file.PPQ, sample.tick))
		if math.Abs(sample.pitch-want) > 1e-8 {
			t.Fatal("vibrato followed ticks instead of elapsed time", sample, want)
		}
	}
	budget = 1
	if _, _, err := midiGestureSamples(note, tempo, file.PPQ, &budget); err == nil {
		t.Fatal("unbounded export")
	}
}

func TestGestureMIDIRejectsInvalidOrAmbiguousInput(t *testing.T) {
	for _, mutate := range []func(*File){
		func(f *File) { f.Tracks[0].EndTick = math.MaxInt64 },
		func(f *File) { f.Tracks[0].Notes[0].Tick = math.MaxInt64 },
		func(f *File) { f.Tracks[0].Notes[0].Chan = 16 },
		func(f *File) { f.Tracks[0].Notes[0].Gesture.Points[0].Tick = math.MinInt64 },
		func(f *File) { f.Tracks[0].Notes[0].Gesture.Points[0].Params.Pressure = float32(math.NaN()) },
		func(f *File) {
			f.Tracks[0].Notes[0].Gesture.Points[0].Params.PitchCents = 9600
			f.Tracks[0].Notes[0].Gesture.Points[0].Params.VibratoDepthCents = 1
		},
		func(f *File) { f.Tracks[0].Meta = []MetaEvent{{Type: 0x09, Data: []byte("existing")}} },
		func(f *File) { f.Tracks[0].Events = []ChannelEvent{{Status: 0xe0}} },
		func(f *File) {
			f.Tracks = append(f.Tracks, TrackChunk{Events: []ChannelEvent{{Tick: math.MaxInt64, Status: 0xb0}}})
		},
	} {
		file := gestureFile()
		mutate(&file)
		if _, err := prepareGestureMIDI(file); err == nil {
			t.Fatal("invalid gestural file accepted", file)
		}
	}
}

func TestGestureMIDIPerformanceChaseAndExclusiveEnd(t *testing.T) {
	source := `track lead acid {}
pattern phrase performance length=7680ticks {
 note 60 at=17ticks length=5983ticks velocity=82 {
  gesture at=0ticks bend=100ct pressure=0.2 timbre=0.5;
  gesture at=1000ticks bend=0ct pressure=0.7 timbre=0.5;
  gesture at=4500ticks bend=-100ct pressure=0.4 timbre=0.5;
  gesture at=5983ticks bend=200ct pressure=0.9 timbre=0.5;
 }
}
scene a { lead=off }
scene b { lead=phrase }
song { a b }
`
	score, ds := notation.Parse([]byte(source))
	p, ds := project.FromScore(score)
	if p == nil || len(ds) > 0 {
		t.Fatal(ds)
	}
	file, err := FromProject(p, 0)
	if err != nil {
		t.Fatal(err)
	}
	factor := int64(file.PPQ) / seq.PPQ
	track := file.Tracks[1]
	if len(track.Notes) != 1 || track.Notes[0].Tick != 3840*factor || track.Notes[0].Dur != 2160*factor {
		t.Fatal(track.Notes)
	}
	var pressures []ChannelEvent
	for _, e := range track.Events {
		if e.Status&0xf0 == 0xd0 {
			pressures = append(pressures, e)
		}
	}
	if !slices.Equal(pressures, []ChannelEvent{{Tick: 3840 * factor, Status: 0xd1, Data1: 89}, {Tick: 4517 * factor, Status: 0xd1, Data1: 51}}) {
		t.Fatal("chase or clipped controller changed", pressures)
	}
}
