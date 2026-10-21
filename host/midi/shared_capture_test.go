package midi

import "testing"

func TestMPEInputRetainsIndependentLayersAndReleasedMasterControls(t *testing.T) {
	input := NoteInput{Mode: "lower"}
	read := func(data ...byte) ([]NoteMessage, []SharedControls) {
		t.Helper()
		notes, shared, err := input.ProcessEvents("surface", data)
		if err != nil {
			t.Fatal(err)
		}
		return notes, shared
	}
	read(0xd0, 100)
	read(0xe1, 127, 127)
	notes, _ := read(0x91, 60, 100)
	on := notes[0]
	if on.Local == nil || on.Shared == nil || on.Local.PitchCents != 4800 || on.Local.Pressure != 0 || on.Shared.Pressure != float64(100)/127 || on.Pressure != on.Shared.Pressure {
		t.Fatal("initial member and master snapshots were folded together", on)
	}
	notes, shared := read(0xe0, 0, 0)
	if len(shared) != 1 || shared[0].ID != on.Shared.ID || shared[0].PitchCents != -200 || !notes[0].SharedOnly || notes[0].Local.PitchCents != 4800 || notes[0].PitchCents != 4600 {
		t.Fatal("master controls lost their identity or changed the local bend", notes, shared)
	}
	notes, _ = read(0xd1, 32)
	if notes[0].SharedOnly || notes[0].Local.Pressure != float64(32)/127 || notes[0].Pressure != float64(100)/127 {
		t.Fatal("master pressure hid the member's original pressure", notes)
	}
	read(0x81, 60, 0)
	notes, shared = read(0xb0, 74, 127)
	if len(notes) != 0 || len(shared) != 1 || shared[0].ID != on.Shared.ID || shared[0].Timbre != 1 {
		t.Fatal("master movement disappeared after the final gate release", notes, shared)
	}
	_, shared = read(0xb0, 121, 0)
	if len(shared) != 1 || shared[0].PitchCents != 0 || shared[0].Pressure != 0 || shared[0].Timbre != .5 {
		t.Fatal("controller reset did not retain a neutral shared state", shared)
	}
}

func TestMPEInputGroupIdentitySeparatesDevicesZonesAndReconnects(t *testing.T) {
	var input NoteInput
	configureInputZone(t, &input, "surface", 0, 4)
	configureInputZone(t, &input, "surface", 15, 4)
	a := inputMessages(t, &input, "surface", 0x91, 60, 100)[0]
	b := inputMessages(t, &input, "surface", 0x9e, 64, 100)[0]
	configureInputZone(t, &input, "other", 0, 4)
	c := inputMessages(t, &input, "other", 0x91, 67, 100)[0]
	if a.Shared.ID == b.Shared.ID || a.Shared.ID == c.Shared.ID || b.Shared.ID == c.Shared.ID || b.Shared.Channel != 15 {
		t.Fatal("independent input zones share a gesture identity")
	}
	inputMessages(t, &input, "surface", 0xb0, 64, 127)
	inputMessages(t, &input, "surface", 0x81, 60, 0)
	inputMessages(t, &input, "surface", 0xe1, 127, 127)
	values, shared, err := input.ProcessEvents("surface", []byte{0xe0, 127, 127})
	if err != nil || len(shared) != 1 || len(values) != 1 || values[0].ID != a.ID || values[0].Local.PitchCents != 0 || values[0].PitchCents != 200 {
		t.Fatal("sustain did not freeze the released member independently", values, shared, err)
	}
	input.Disconnect("surface")
	configureInputZone(t, &input, "surface", 0, 4)
	d := inputMessages(t, &input, "surface", 0x91, 60, 100)[0]
	if d.Shared.ID == a.Shared.ID || d.Shared.ID == b.Shared.ID {
		t.Fatal("reconnect reused an old gesture group")
	}
	configureInputZone(t, &input, "surface", 0, 3)
	e := inputMessages(t, &input, "surface", 0x91, 60, 100)[0]
	if e.Shared.ID == d.Shared.ID {
		t.Fatal("zone reconfiguration reused an old group")
	}
	input.Reset("lower")
	f := inputMessages(t, &input, "surface", 0x91, 60, 100)[0]
	if f.Shared.ID == e.Shared.ID {
		t.Fatal("input reset aliased a retained group")
	}
}

func TestOrdinaryMIDIInputNeverInventsSharedGestures(t *testing.T) {
	var input NoteInput
	for _, data := range [][]byte{{0x90, 60, 100}, {0xe0, 127, 127}, {0xd0, 100}, {0xb0, 74, 127}} {
		notes, shared, err := input.ProcessEvents("ordinary", data)
		if err != nil || len(shared) != 0 {
			t.Fatal(shared, err)
		}
		for _, note := range notes {
			if note.Local != nil || note.Shared != nil {
				t.Fatal("ordinary channel acquired an MPE layer", note)
			}
		}
	}
}

func TestMPEInputKeepsTheUnaffectedZonesHeldNotesBound(t *testing.T) {
	var input NoteInput
	configureInputZone(t, &input, "surface", 0, 15)
	a := inputMessages(t, &input, "surface", 0x91, 60, 100)[0]
	b := inputMessages(t, &input, "surface", 0x9e, 64, 100)[0]
	released := configureInputZone(t, &input, "surface", 15, 4)
	if len(released) != 1 || released[0].ID != b.ID {
		t.Fatal("upper zone setup released an unaffected lower member", released)
	}
	notes, shared, err := input.ProcessEvents("surface", []byte{0xe0, 127, 127})
	if err != nil || len(notes) != 1 || len(shared) != 1 || notes[0].ID != a.ID || notes[0].Shared.ID != a.Shared.ID || shared[0].ID != a.Shared.ID {
		t.Fatal("implicit zone shrink orphaned the held note's shared controls", notes, shared, err)
	}
}
