package smf

import (
	"bytes"
	"testing"

	"m31labs.dev/cicada/kernel/expression"
	"m31labs.dev/cicada/kernel/seq"
)

func TestSharedMIDIMasterOutlivesGatesAndUsesIndependentPorts(t *testing.T) {
	file := File{PPQ: 960, Tracks: []TrackChunk{{Name: "touch", EndTick: 1920, Groups: []SharedGesture{
		{Name: "one", Points: []seq.GroupPoint{{Value: expression.Controls{Timbre: .5}}, {Tick: 960, Value: expression.Controls{PitchCents: 100, Pressure: .8, Timbre: .9}}}},
		{Name: "two", Points: []seq.GroupPoint{{Value: expression.Controls{PitchCents: -50, Timbre: .5}}}},
	}, Notes: []Note{
		{Tick: 17, Dur: 103, Note: 60, Vel: 103, Gesture: &NoteGesture{Group: 1, Points: []seq.PerformedExpression{{Params: seq.Expression{Set: true, Timbre: .5}}, {Tick: 103, Params: seq.Expression{Set: true, Pressure: .5, Timbre: .5}}}}},
		{Tick: 19, Dur: 301, Note: 64, Vel: 87, Gesture: &NoteGesture{Group: 1}},
		{Tick: 400, Dur: 100, Note: 67, Vel: 90, Gesture: &NoteGesture{Group: 2}},
	}}}}
	prepared, err := prepareGestureMIDI(file)
	if err != nil || len(prepared.Tracks) != 2 {
		t.Fatal("shared lanes did not get independent master ports", err, len(prepared.Tracks))
	}
	factor := int64(prepared.PPQ / file.PPQ)
	if midiPortAt(prepared.Tracks[0], 0) == midiPortAt(prepared.Tracks[1], 0) {
		t.Fatal("release tails can hear another lane's master")
	}
	var wire bytes.Buffer
	if err := Encode(file, &wire); err != nil {
		t.Fatal(err)
	}
	// Decode the actual bytes, not the intermediate export model.
	decoded, err := Decode(bytes.NewReader(wire.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if err := MusicalDiff(file, decoded); err != nil {
		t.Fatal("shared MIDI round trip changed controls", err)
	}
	masterAfterOff := false
	for _, e := range decoded.Tracks[0].Events {
		if e.Tick == 960*factor && e.Status == 0xd0 && e.Data1 == 102 {
			masterAfterOff = true
		}
	}
	if !masterAfterOff {
		t.Fatal("shared pressure after the final gate was discarded")
	}
	final := bytes.Index(wire.Bytes(), []byte{0xd1, 64})
	off := bytes.Index(wire.Bytes(), []byte{0x81, 60, 0})
	if final < 0 || off <= final {
		t.Fatal("final member snapshot did not precede release", final, off)
	}
	if len(decoded.Tracks[0].Notes) != 2 || len(decoded.Tracks[1].Notes) != 1 {
		t.Fatal("shared routing changed gates")
	}
	if file.Tracks[0].Groups[0].Points[1].Tick != 960 || file.Tracks[0].Notes[0].Gesture.Group != 1 {
		t.Fatal("MIDI export mutated the score")
	}
}

func TestSharedMIDIRangeRepeatsAndFreezesOnSelectionChange(t *testing.T) {
	p := &seq.Pattern{Len: 1, Performance: &seq.Performance{LengthTicks: 960, Groups: []seq.ControlGroup{{Name: "touch", Points: []seq.GroupPoint{{Tick: 130, Value: expression.Controls{Pressure: .8, Timbre: .5}}}}}, Notes: []seq.PerformedNote{{Note: 60, Velocity: 100, EndTick: 120, Group: 1}}}}
	track := TrackChunk{EndTick: 3840}
	appendPerformedRange(&track, p, 0, 0, 960, 3840, 0, 0)
	appendPerformedRange(&track, p, 0, 960, 1920, 3840, 0, 0)
	if len(track.Groups) != 1 || len(track.Groups[0].Points) != 4 || track.Groups[0].Points[2].Tick != 960 || track.Groups[0].Points[2].Value.Pressure != 0 {
		t.Fatal("loop reset or shared identity lost", track.Groups)
	}
	closePerformedAt(&track, 1920)
	appendPerformedRange(&track, p, 0, 1920, 2880, 3840, 0, 0)
	if len(track.Groups) != 2 || track.Notes[2].Gesture.Group != 2 || track.Groups[0].Points[len(track.Groups[0].Points)-1].Tick >= 1920 {
		t.Fatal("new selection reused an outgoing release lane")
	}
}
