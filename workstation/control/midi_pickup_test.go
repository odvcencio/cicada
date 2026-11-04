package control

import "testing"

func TestMIDIPickupWaitsForCrossingAndFollowsController(t *testing.T) {
	d := Descriptor{Min: 200, Max: 4000, Curve: "log"}
	target := 900.0
	var pickup MIDIPickup
	if _, send, err := pickup.Apply(d, 0, &target); err != nil || send {
		t.Fatalf("first movement jumped: %v %v", send, err)
	}
	value, send, err := pickup.Apply(d, 100, &target)
	if err != nil || !send || value == nil || *value <= target {
		t.Fatalf("crossing did not acquire: %v %v %v", value, send, err)
	}
	value, send, err = pickup.Apply(d, 110, &target)
	if err != nil || !send || value == nil {
		t.Fatalf("acquired controller stopped: %v %v %v", value, send, err)
	}
}

func TestMIDIPickupRearmsAfterExternalChange(t *testing.T) {
	d := Descriptor{Min: 0, Max: 127, Curve: "linear"}
	initial, external := 64.0, 100.0
	var p MIDIPickup
	if _, send, _ := p.ApplyAt(d, 64, &initial, 1000); !send {
		t.Fatal("initial target not acquired")
	}
	if _, send, _ := p.ApplyAt(d, 65, &initial, 1100); !send {
		t.Fatal("stale snapshot interrupted pickup")
	}
	if _, send, _ := p.ApplyAt(d, 66, &external, 1700); send {
		t.Fatal("external change did not rearm pickup")
	}
	if _, send, _ := p.ApplyAt(d, 101, &external, 1800); !send {
		t.Fatal("crossing new target did not acquire")
	}
}

func TestMIDIPickupHandlesExactAndOffTargets(t *testing.T) {
	d := Descriptor{Min: 0, Max: 127, Curve: "linear"}
	target := 64.0
	var exact MIDIPickup
	if _, send, err := exact.Apply(d, 64, &target); err != nil || !send {
		t.Fatalf("exact target: %v %v", send, err)
	}
	var off MIDIPickup
	fader := Descriptor{Min: -60, Max: 0, Curve: "fader", Off: true}
	if _, send, err := off.Apply(fader, 1, nil); err != nil || send {
		t.Fatalf("off target jumped: %v %v", send, err)
	}
	if value, send, err := off.Apply(fader, 0, nil); err != nil || !send || value != nil {
		t.Fatalf("off target not acquired: %v %v %v", value, send, err)
	}
}
