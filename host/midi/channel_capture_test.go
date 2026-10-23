package midi

import "testing"

func TestMIDI1SharedChannelKeepsPolyPressureSustainAndReleasedControls(t *testing.T) {
	var input NoteInput
	read := func(data ...byte) ([]NoteMessage, []SharedControls) {
		t.Helper()
		notes, shared, err := input.ProcessEvents("keys", data)
		if err != nil {
			t.Fatal(err)
		}
		return notes, shared
	}
	_, shared := read(0xe2, 127, 127)
	if len(shared) != 1 || shared[0].Kind != SharedChannelKind || shared[0].PitchCents != 200 {
		t.Fatal("pre-note channel movement was lost", shared)
	}
	group := shared[0].ID
	a, _ := read(0x92, 60, 100)
	b, _ := read(0x92, 64, 90)
	other, _ := read(0x93, 67, 80)
	if a[0].Shared.ID != group || b[0].Shared.ID != group || other[0].Shared.ID == group || a[0].Local.PitchCents != 0 || a[0].PitchCents != 200 {
		t.Fatal("notes did not share their own channel controls", a, b, other)
	}
	notes, _ := read(0xa2, 60, 96)
	if len(notes) != 1 || notes[0].SharedOnly || notes[0].Local.Pressure != 96.0/127 {
		t.Fatal("polyphonic aftertouch did not remain local", notes)
	}
	notes, shared = read(0xd2, 32)
	if len(notes) != 2 || len(shared) != 1 || shared[0].Pressure != 32.0/127 || !notes[0].SharedOnly || notes[0].Local.Pressure != 96.0/127 || notes[0].Pressure != 96.0/127 || notes[1].Pressure != 32.0/127 {
		t.Fatal("channel pressure overwrote the independent polyphonic pressure", notes, shared)
	}
	read(0xb2, 64, 127)
	read(0x82, 60, 0)
	read(0x82, 64, 0)
	notes, _ = read(0xa2, 60, 127)
	if len(notes) != 0 {
		t.Fatal("a released key changed its frozen local pressure", notes)
	}
	notes, shared = read(0xe2, 0, 0)
	if len(notes) != 2 || shared[0].PitchCents != -200 || notes[0].Local.Pressure != 96.0/127 || notes[0].PitchCents != -200 {
		t.Fatal("shared bend froze with a sustained key", notes, shared)
	}
	off, _ := read(0xb2, 64, 0)
	if len(off) != 2 || off[0].ID != a[0].ID || off[1].ID != b[0].ID {
		t.Fatal("pedal release lost the original gates", off)
	}
	notes, shared = read(0xb2, 74, 127)
	if len(notes) != 0 || len(shared) != 1 || shared[0].ID != group || shared[0].Timbre != 1 {
		t.Fatal("channel movement disappeared after gate release", notes, shared)
	}
	_, shared = read(0xb2, 121, 0)
	if shared[0].PitchCents != 0 || shared[0].Pressure != 0 || shared[0].Timbre != .5 || shared[0].ID != group {
		t.Fatal("controller reset lost the shared channel identity or neutral values", shared)
	}
}

func TestMIDI1SharedIdentitySeparatesDevicesAndZoneReconfiguration(t *testing.T) {
	var input NoteInput
	a := inputMessages(t, &input, "keys", 0x92, 60, 100)[0]
	b := inputMessages(t, &input, "other", 0x92, 60, 100)[0]
	unaffected := inputMessages(t, &input, "keys", 0x9e, 67, 90)[0]
	if a.Shared.ID == b.Shared.ID || a.Shared.ID == unaffected.Shared.ID {
		t.Fatal("ordinary inputs shared a controller identity")
	}
	configureInputZone(t, &input, "keys", 0, 4)
	mpe := inputMessages(t, &input, "keys", 0x92, 60, 100)[0]
	if !mpe.MPE || mpe.Shared.Kind != "" || mpe.Shared.ID == a.Shared.ID {
		t.Fatal("a new zone inherited the ordinary channel identity", mpe)
	}
	values, shared, err := input.ProcessEvents("keys", []byte{0xde, 64})
	if err != nil || len(values) != 1 || len(shared) != 1 || shared[0].ID != unaffected.Shared.ID || values[0].ID != unaffected.ID {
		t.Fatal("zone configuration changed an unrelated ordinary channel", values, shared, err)
	}
	configureInputZone(t, &input, "keys", 0, 0)
	c := inputMessages(t, &input, "keys", 0x92, 60, 100)[0]
	if c.MPE || c.Shared.Kind != SharedChannelKind || c.Shared.ID == a.Shared.ID || c.Shared.ID == mpe.Shared.ID {
		t.Fatal("disabling a zone reused a prior controller lane", c)
	}
	input.Disconnect("keys")
	d := inputMessages(t, &input, "keys", 0x92, 60, 100)[0]
	if d.Shared.ID == c.Shared.ID || d.PitchCents != 0 || d.Pressure != 0 || d.Timbre != .5 {
		t.Fatal("reconnecting inherited the old channel controls", d)
	}
}

func TestMIDI1SharedMasterBecomingOrdinaryReleasesItsOldBinding(t *testing.T) {
	var input NoteInput
	configureInputZone(t, &input, "keys", 15, 4)
	inputMessages(t, &input, "keys", 0xdf, 100)
	on := inputMessages(t, &input, "keys", 0x9f, 67, 90)[0]
	if on.MPE || on.Shared.Kind != "" || on.Shared.Pressure != 100.0/127 {
		t.Fatal("direct master-channel note did not use its zone controls", on)
	}
	configureInputZone(t, &input, "keys", 0, 14) // Removes the upper master without making channel 16 a lower member.
	if len(input.notes) != 0 {
		t.Fatal("a former master kept an orphaned live binding", input.notes)
	}
	next := inputMessages(t, &input, "keys", 0x9f, 67, 90)[0]
	if next.Shared.Kind != SharedChannelKind || next.Shared.ID == on.Shared.ID || next.Pressure != 0 {
		t.Fatal("the ordinary channel inherited a retired zone", next)
	}
}
