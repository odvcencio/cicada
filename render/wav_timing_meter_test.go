package render

import (
	"bytes"
	"encoding/binary"
	"testing"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// A song with a meter writes cica version 3: version 2's fields, then the
// meter's beats and unit and two zero bytes, then the tempo steps. A song
// without one writes the version 1 and 2 bytes it always did.
func TestTimingChunkVersion3RecordsTheMeter(t *testing.T) {
	changes := []project.TempoChange{{Bar: 2, BPMMilli: 90_000}}
	var plain, stepped, waltz bytes.Buffer
	if err := writeMetadata(&plain, nil, 120_000, 4, 4800, nil); err != nil {
		t.Fatal(err)
	}
	if err := writeMetadata(&stepped, nil, 120_000, 4, 4800, changes); err != nil {
		t.Fatal(err)
	}
	meter := &notation.Meter{Beats: 3, Unit: 4}
	if err := writeMetadata(&waltz, meter, 120_000, 4, 4800, changes); err != nil {
		t.Fatal(err)
	}
	version := func(b []byte) uint32 { return binary.LittleEndian.Uint32(b[8:12]) }
	if plain.Len() != metadataBytes(0, nil) || plain.Len() != 24 || version(plain.Bytes()) != 1 {
		t.Fatalf("version 1: %d bytes, version %d", plain.Len(), version(plain.Bytes()))
	}
	if stepped.Len() != 32 || version(stepped.Bytes()) != 2 {
		t.Fatalf("version 2: %d bytes, version %d", stepped.Len(), version(stepped.Bytes()))
	}
	got := waltz.Bytes()
	if len(got) != metadataBytes(1, meter) || len(got) != 36 || version(got) != 3 || got[24] != 3 || got[25] != 4 || got[26] != 0 || got[27] != 0 {
		t.Fatalf("version 3: % x", got)
	}
	// The reader takes it back: a WAV of 0 frames, then the chunk.
	timing, err := readWAVTiming(bytes.NewReader(got), 0, int64(len(got)))
	if err != nil || timing.beats != 3 || timing.unit != 4 || len(timing.changes) != 1 || timing.changes[0] != changes[0] {
		t.Fatalf("read: %+v %v", timing, err)
	}
	// Four 3/4 bars, stepping from 120 to 90 BPM where zero-based bar 2
	// begins: two bars at 120 (3 s), two at 90 (4 s), plus the 4800-frame tail.
	frames, err := timing.frames(48000, 0)
	if err != nil || frames != 7*48000+4800 {
		t.Fatalf("frames %d %v", frames, err)
	}
	bad := append([]byte(nil), got...)
	bad[26] = 1
	if _, err := readWAVTiming(bytes.NewReader(bad), 0, int64(len(bad))); err == nil {
		t.Fatal("a version 3 chunk with nonzero reserved bytes was accepted")
	}
}
