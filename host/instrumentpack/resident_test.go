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

// declaring is a pack whose manifest declares the given PCM: one mono asset of
// bytes/4 frames that one zone plays. It reads no files.
func declaring(manifest string, bytes int64) *Pack {
	return &Pack{
		dir: "/packs", manifestPath: manifest, digest: manifest,
		manifest: Manifest{Assets: []Asset{{ID: "a", Frames: int(bytes / 4), Channels: 1}}, Zones: []Zone{{Asset: "a"}}},
	}
}

func TestResidentCapRefusesThePackThatCrossesItWithTheLimitAndSize(t *testing.T) {
	c, err := NewResidentCap(1200)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Reserve(declaring("packs/a/manifest.json", 600)); err != nil {
		t.Fatalf("600 bytes of 1200: %v", err)
	}
	if err := c.Reserve(declaring("packs/b/manifest.json", 600)); err != nil {
		t.Fatalf("a total equal to the cap is allowed: %v", err)
	}
	err = c.Reserve(declaring("packs/c/manifest.json", 600))
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

// Samplers that name one pack share one decoded copy, so the cap counts it once.
func TestResidentCapCountsAPackThatSeveralSamplersNameOnce(t *testing.T) {
	c, _ := NewResidentCap(1000)
	pack := declaring("packs/a/manifest.json", 600)
	for range 3 {
		if err := c.Reserve(pack); err != nil {
			t.Fatalf("the same pack, counted again: %v", err)
		}
	}
	if c.used != 600 {
		t.Fatalf("tally %d bytes, want 600", c.used)
	}
	// A different folder or different manifest bytes is a different pack.
	elsewhere := declaring("packs/a/manifest.json", 600)
	elsewhere.dir = "/other"
	if err := c.Reserve(elsewhere); err == nil {
		t.Fatal("a pack in another folder was counted as the first")
	}
}

func TestManifestResidentPCMCountsEachPlayedAssetOnce(t *testing.T) {
	m := Manifest{
		Assets: []Asset{
			{ID: "kick", Frames: 100, Channels: 1},
			{ID: "snare", Frames: 50, Channels: 2},
			{ID: "spare", Frames: 1000, Channels: 2}, // verified, then dropped: no zone plays it
		},
		Zones: []Zone{{Asset: "kick"}, {Asset: "kick"}, {Asset: "snare"}},
	}
	// kick 100 x 1 x 4, snare 50 x 2 x 4.
	if got := m.ResidentPCMBytes(); got != 800 {
		t.Fatalf("declared PCM %d bytes, want 800", got)
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

func TestSetDefaultMaxResidentPCMChangesWhatZeroSelects(t *testing.T) {
	t.Cleanup(func() { SetDefaultMaxResidentPCM(0) })
	if err := SetDefaultMaxResidentPCM(3 << 30); err != nil {
		t.Fatal(err)
	}
	c, _ := NewResidentCap(0)
	if c.Limit() != 3<<30 {
		t.Fatalf("cap %d after SetDefaultMaxResidentPCM(3 GiB)", c.Limit())
	}
	if c, _ = NewResidentCap(5); c.Limit() != 5 {
		t.Fatalf("an explicit limit must win over the process default: %d", c.Limit())
	}
	if err := SetDefaultMaxResidentPCM(-1); err == nil {
		t.Fatal("a negative default was accepted")
	}
	if err := SetDefaultMaxResidentPCM(0); err != nil {
		t.Fatal(err)
	}
	if c, _ = NewResidentCap(0); c.Limit() != DefaultMaxResidentPCM {
		t.Fatalf("zero must restore the built-in cap: %d", c.Limit())
	}
}

func TestZoneMapKeepsTheKeysAndDropsTheAudio(t *testing.T) {
	p := &Prepared{Zones: []sample.Zone{attackZone(0, 40, 50, 10, 127, false), attackZone(1, 60, 60, 1, 50, true)}}
	zones := p.ZoneMap()
	if len(zones) != 2 {
		t.Fatalf("%d zones, want 2", len(zones))
	}
	for i, z := range zones {
		if z.Region.Left != nil || z.Region.Right != nil || z.KeyLow != p.Zones[i].KeyLow || z.Release != p.Zones[i].Release {
			t.Errorf("zone %d: %+v", i, z)
		}
	}
	if p.Zones[0].Region.Left == nil {
		t.Fatal("ZoneMap took the audio out of the pack it copied")
	}
	if !zones.HasAttackZone(45, 100) || zones.HasAttackZone(45, 5) || zones.HasAttackZone(60, 10) {
		t.Fatal("the map answers differently from the pack's zones")
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
