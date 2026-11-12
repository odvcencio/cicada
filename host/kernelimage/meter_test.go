package kernelimage_test

import (
	"bytes"
	"encoding/binary"
	"testing"

	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/seq"
)

// A meter table is the image's last appendix: u8 count, then per change u32
// bar, u8 beats, u8 unit. A malformed table is refused as an image error, and
// a 3/4 table round-trips (#358 review).
func TestMalformedMeterTablesAreRefused(t *testing.T) {
	cfg := firstAcidConfig(t)
	cfg.Meters = []seq.MeterChange{{Meter: seq.Meter{Beats: 3, Unit: 4}}}
	image, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint32(image[8:])&kernelimage.MeterFlag == 0 || !bytes.Equal(image[len(image)-7:], []byte{1, 0, 0, 0, 0, 3, 4}) {
		t.Fatalf("meter table: flags %#x, tail % x", binary.LittleEndian.Uint32(image[8:]), image[len(image)-7:])
	}
	back, err := kernelimage.Decode(image, cfg.SampleRate, cfg.MaxBlock)
	if err != nil || len(back.Meters) != 1 || back.Meters[0] != cfg.Meters[0] {
		t.Fatalf("round trip: %v %+v", err, back.Meters)
	}
	for _, c := range []struct {
		name string
		edit func([]byte) []byte
	}{
		{"no changes", func(b []byte) []byte { b[len(b)-7] = 0; return b }},
		{"a unit of 3", func(b []byte) []byte { b[len(b)-1] = 3; return b }},
		{"no beats", func(b []byte) []byte { b[len(b)-2] = 0; return b }},
		{"a first change after bar 0", func(b []byte) []byte { b[len(b)-6] = 2; return b }},
		{"a truncated table", func(b []byte) []byte { return b[:len(b)-1] }},
		{"the flag without a table", func(b []byte) []byte { return b[:len(b)-7] }},
	} {
		bad := c.edit(append([]byte(nil), image...))
		if _, err := kernelimage.Decode(bad, cfg.SampleRate, cfg.MaxBlock); err == nil {
			t.Errorf("%s: accepted", c.name)
		}
	}
	plain := firstAcidConfig(t)
	clean, err := kernelimage.Encode(plain)
	if err != nil || binary.LittleEndian.Uint32(clean[8:])&kernelimage.MeterFlag != 0 {
		t.Fatalf("a 4/4 image carries the meter flag: %v", err)
	}
}
