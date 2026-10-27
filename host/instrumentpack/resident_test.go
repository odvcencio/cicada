package instrumentpack

import (
	"errors"
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel/voice/sample"
)

// attackZone is a one-shot zone over keys low..high and velocities vlow..vhigh,
// in its own group, on a silent region.
func attackZone(group, low, high, vlow, vhigh uint8, release bool) sample.Zone {
	return sample.Zone{
		Region: sample.Region{Left: make([]float32, 256), SampleRate: 48000, RootKey: (low + high) / 2, End: 256},
		KeyLow: low, KeyHigh: high, VelocityLow: vlow, VelocityHigh: vhigh, Layer: (vlow + vhigh) / 2,
		Group: group, Count: 1, Gain: 1, Release: release,
	}
}

// HasAttackZone is what check relies on to predict engine fault 29, so it must
// agree with sample.Instrument.NoteOn for every note and velocity.
func TestHasAttackZoneAgreesWithInstrumentNoteOn(t *testing.T) {
	p := &Prepared{Manifest: Manifest{Config: sample.DefaultInstrumentConfig()}, Zones: []sample.Zone{
		attackZone(0, 40, 50, 10, 127, false), // velocities 1 to 9 have no zone
		attackZone(1, 60, 60, 1, 50, false),   // velocities 51 and up have no zone
		attackZone(2, 70, 75, 1, 127, true),   // a release zone starts no sound
		attackZone(3, 80, 90, 1, 127, false),
		attackZone(3, 80, 90, 1, 127, true),
	}}
	instrument, err := p.New(48000)
	if err != nil {
		t.Fatal(err)
	}
	covered := 0
	for note := 0; note <= 127; note++ {
		for velocity := 1; velocity <= 127; velocity++ {
			_, err := instrument.NoteOn(uint8(note), uint8(velocity))
			want := p.HasAttackZone(uint8(note), uint8(velocity))
			if (err == nil) != want {
				t.Fatalf("note %d velocity %d: NoteOn error %v, HasAttackZone %t", note, velocity, err, want)
			}
			if want {
				covered++
			}
		}
		if p.HasAttackZone(uint8(note), 0) {
			t.Fatalf("note %d at velocity 0 has an attack zone, but velocity 0 never plays", note)
		}
	}
	// 11 keys x 118 velocities, 1 key x 50, 11 keys x 127: the zones are not all empty.
	if want := 11*118 + 50 + 11*127; covered != want {
		t.Fatalf("%d covered notes, want %d", covered, want)
	}
}

func TestResidentPCMBytesCountsASharedRegionOnce(t *testing.T) {
	shared := make([]float32, 100)
	p := &Prepared{Zones: []sample.Zone{
		{Region: sample.Region{Left: shared, Right: make([]float32, 50)}},
		{Region: sample.Region{Left: shared}}, // a second zone on the same region
		{Region: sample.Region{}},             // an empty region holds nothing
	}}
	// 150 samples at 4 bytes each.
	if got := p.ResidentPCMBytes(); got != 600 {
		t.Fatalf("resident PCM is %d bytes, want 600", got)
	}
}

func TestResidentCapRefusesThePackThatCrossesItWithTheLimitAndSize(t *testing.T) {
	pack := &Prepared{Zones: []sample.Zone{{Region: sample.Region{Left: make([]float32, 150)}}}} // 600 bytes
	c, err := NewResidentCap(1200)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Add("packs/a/manifest.json", pack); err != nil {
		t.Fatalf("600 bytes of 1200: %v", err)
	}
	if err := c.Add("packs/b/manifest.json", pack); err != nil {
		t.Fatalf("a total equal to the cap is allowed: %v", err)
	}
	err = c.Add("packs/c/manifest.json", pack)
	var over *ResidentPCMError
	if !errors.As(err, &over) || over.Pack != "packs/c/manifest.json" || over.Bytes != 1800 || over.Limit != 1200 {
		t.Fatalf("third pack: %#v", err)
	}
	for _, want := range []string{"--max-resident-pcm", "1800 bytes", "1200 bytes", "packs/c/manifest.json"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
}

func TestResidentCapDefaultsToOneGiBAndRefusesNegativeLimits(t *testing.T) {
	c, err := NewResidentCap(0)
	if err != nil || c.limit != 1<<30 || DefaultMaxResidentPCM != 1<<30 {
		t.Fatalf("default cap %+v (%v), want 1 GiB", c, err)
	}
	if _, err := NewResidentCap(-1); err == nil {
		t.Fatal("a negative cap was accepted")
	}
}

func TestFormatBytes(t *testing.T) {
	for _, tc := range []struct {
		bytes int64
		want  string
	}{
		{0, "0 bytes"},
		{1048575, "1048575 bytes"},
		{1 << 20, "1 MiB (1048576 bytes)"},
		{72_000_000, "68.66 MiB (72000000 bytes)"},
		{1 << 30, "1 GiB (1073741824 bytes)"},
		{3 << 29, "1.50 GiB (1610612736 bytes)"},
	} {
		if got := FormatBytes(tc.bytes); got != tc.want {
			t.Errorf("FormatBytes(%d) = %q, want %q", tc.bytes, got, tc.want)
		}
	}
}
