package midi

import (
	"math"
	"testing"
)

func inputMessages(t *testing.T, input *NoteInput, device string, data ...byte) []NoteMessage {
	t.Helper()
	events, err := input.Process(device, data)
	if err != nil {
		t.Fatal(err)
	}
	return events
}
func configureInputZone(t *testing.T, input *NoteInput, device string, channel, count byte) []NoteMessage {
	t.Helper()
	inputMessages(t, input, device, 0xb0|channel, 101, 0)
	inputMessages(t, input, device, 0xb0|channel, 100, 6)
	return inputMessages(t, input, device, 0xb0|channel, 6, count)
}

func TestMIDI1NotesKeepOrdinaryPitchRangeAndTwoBytePressure(t *testing.T) {
	var input NoteInput
	on := inputMessages(t, &input, "keys", 0x92, 60, 100)[0]
	if on.MPE || on.ID == 0 || on.Timbre != .5 {
		t.Fatal(on)
	}
	bend := inputMessages(t, &input, "keys", 0xe2, 127, 127)[0]
	if bend.PitchCents != 200 || bend.MPE {
		t.Fatal("ordinary channel was silently switched to MPE", bend)
	}
	pressure := inputMessages(t, &input, "keys", 0xd2, 64)[0]
	if pressure.Pressure != float64(64)/127 {
		t.Fatal("two-byte channel pressure lost", pressure)
	}
	inputMessages(t, &input, "keys", 0xb2, 101, 0)
	inputMessages(t, &input, "keys", 0xb2, 100, 0)
	inputMessages(t, &input, "keys", 0xb2, 6, 12)
	if got := inputMessages(t, &input, "keys", 0xb2, 38, 50)[0].PitchCents; got != 1250 {
		t.Fatal("RPN sensitivity lost", got)
	}
	if off := inputMessages(t, &input, "keys", 0x92, 60, 0); len(off) != 1 || off[0].ID != on.ID {
		t.Fatal(off)
	}
}

func TestMIDI1MPEZonesControllersAndDrumChannel(t *testing.T) {
	var input NoteInput
	configureInputZone(t, &input, "surface", 0, 15)
	a := inputMessages(t, &input, "surface", 0x91, 60, 100)[0]
	b := inputMessages(t, &input, "surface", 0x99, 64, 90)[0]
	if !a.MPE || !b.MPE || a.ID == b.ID {
		t.Fatal("member channel 10 was treated as a drum", a, b)
	}
	if values := inputMessages(t, &input, "surface", 0xe1, 127, 127); len(values) != 1 || values[0].ID != a.ID || values[0].PitchCents != 4800 {
		t.Fatal(values)
	}
	values := inputMessages(t, &input, "surface", 0xe0, 0, 0)
	if len(values) != 2 || values[0].PitchCents != 4600 || values[1].PitchCents != -200 {
		t.Fatal("manager bend did not reach its zone", values)
	}
	inputMessages(t, &input, "surface", 0xd1, 64)
	if values := inputMessages(t, &input, "surface", 0xd0, 100); len(values) != 2 || values[0].Pressure != float64(100)/127 || values[1].Pressure != float64(100)/127 {
		t.Fatal("manager pressure was lost", values)
	}
	inputMessages(t, &input, "surface", 0xb1, 74, 64)
	if values := inputMessages(t, &input, "surface", 0xb0, 74, 127); math.Abs(values[0].Timbre-1) > 1e-12 {
		t.Fatal(values)
	}
	if got := inputMessages(t, &input, "surface", 0xa1, 60, 127); len(got) != 0 {
		t.Fatal("reserved member poly-pressure affected a note", got)
	}
	if got := configureInputZone(t, &input, "surface", 15, 15); len(got) != 2 {
		t.Fatal("zone reconfiguration stranded held notes", got)
	}
	c := inputMessages(t, &input, "surface", 0x90, 67, 100)[0]
	if !c.MPE {
		t.Fatal("upper zone did not include channel 1", c)
	}
}

func TestMIDI1SustainFreezesReleasedMPEMemberAndKeepsOtherInputs(t *testing.T) {
	input := NoteInput{Mode: "lower"}
	a := inputMessages(t, &input, "surface", 0x91, 60, 100)[0]
	inputMessages(t, &input, "other", 0x91, 60, 90)
	inputMessages(t, &input, "surface", 0xe1, 127, 127)
	inputMessages(t, &input, "surface", 0xb0, 64, 127)
	if got := inputMessages(t, &input, "surface", 0x81, 60, 0); len(got) != 0 {
		t.Fatal("pedal did not hold note", got)
	}
	if got := inputMessages(t, &input, "surface", 0xe1, 0, 0); len(got) != 0 {
		t.Fatal("released member remained mutable", got)
	}
	values := inputMessages(t, &input, "surface", 0xe0, 127, 127)
	if len(values) != 1 || values[0].PitchCents != 5000 {
		t.Fatal("released member value was not frozen under manager bend", values)
	}
	off := inputMessages(t, &input, "surface", 0xb0, 64, 0)
	if len(off) != 1 || off[0].ID != a.ID {
		t.Fatal(off)
	}
	if remaining := input.Disconnect("other"); len(remaining) != 1 || remaining[0].Device != "other" {
		t.Fatal("another input was lost", remaining)
	}
}

func TestMIDI1RetriggerPairingResetAndBounds(t *testing.T) {
	var input NoteInput
	a := inputMessages(t, &input, "keys", 0x90, 60, 100)[0]
	b := inputMessages(t, &input, "keys", 0x90, 60, 110)[0]
	if a.ID == b.ID {
		t.Fatal("retrigger reused active identity")
	}
	if off := inputMessages(t, &input, "keys", 0x80, 60, 0); len(off) != 1 || off[0].ID != a.ID {
		t.Fatal(off)
	}
	inputMessages(t, &input, "keys", 0xa0, 60, 100)
	if values := inputMessages(t, &input, "keys", 0xb0, 121, 0); len(values) != 1 || values[0].Pressure != 0 || values[0].PitchCents != 0 || values[0].Timbre != .5 {
		t.Fatal("controller reset did not reset pressure", values)
	}
	if notes := input.Reset("auto"); len(notes) != 1 || notes[0].ID != b.ID {
		t.Fatal(notes)
	}
	for _, bad := range [][]byte{{0x90, 128, 1}, {0x90, 60}, {0xd0}, {0xe0, 1, 255}} {
		if _, err := input.Process("keys", bad); err == nil {
			t.Fatal("invalid MIDI accepted", bad)
		}
	}
	for range 128 {
		inputMessages(t, &input, "keys", 0x90, 60, 100)
	}
	if _, err := input.Process("keys", []byte{0x90, 61, 100}); err == nil {
		t.Fatal("unbounded held notes")
	}
	if notes := input.Disconnect("keys"); len(notes) != 128 {
		t.Fatal("input failure blocked releases", len(notes))
	}
}
