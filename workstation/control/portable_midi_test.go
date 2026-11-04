package control

import "testing"

func TestPortableMidiDeviceAndChannelResolution(t *testing.T) {
	cc := 74
	m := PortableMIDI{Maps: []PortableMapping{
		{Port: "keys", Message: "cc", Number: &cc, Target: "bass.pan"},
		{Port: "keys", Message: "cc", Channel: 2, Number: &cc, Target: "bass.cutoff"},
		{Port: "pads", Message: "track", Channel: 10, Target: "drums"},
	}}
	ports := map[string]string{"keys": "Keyboard A", "pads": "Pad B"}
	if got, ok := m.Match(ports, "Keyboard A", 2, "cc", 74); !ok || got.Target != "bass.cutoff" {
		t.Fatalf("specific: %+v %v", got, ok)
	}
	if got, ok := m.Match(ports, "Keyboard A", 1, "cc", 74); !ok || got.Target != "bass.pan" {
		t.Fatalf("wildcard: %+v %v", got, ok)
	}
	if got, ok := m.Match(ports, "Pad B", 10, "track", 0); !ok || got.Target != "drums" {
		t.Fatalf("track: %+v %v", got, ok)
	}
	if _, ok := m.Match(ports, "Keyboard B", 1, "cc", 74); ok {
		t.Fatal("unbound device matched")
	}
}

func TestPortableMidiLogRangeReachesExactEndpoints(t *testing.T) {
	d := Descriptor{Min: 200, Max: 4000, Curve: "log"}
	for _, tt := range []struct {
		input int
		want  float64
	}{{0, 200}, {127, 4000}} {
		got, err := CCValue(d, tt.input)
		if err != nil || got == nil || *got != tt.want {
			t.Fatalf("CC %d: %v %v", tt.input, got, err)
		}
	}
}
