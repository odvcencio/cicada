package audioasset

import (
	"bytes"
	"encoding/binary"
	"testing"

	"m31labs.dev/cicada/internal/testwav"
)

func TestReadWAVHeaderRejectsMalformedChunks(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func([]byte) []byte
	}{
		{"truncated header", func(b []byte) []byte { return b[:11] }},
		{"truncated payload", func(b []byte) []byte { return b[:len(b)-1] }},
		{"chunk overflow", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[40:], 0xffffffff); return b }},
		{"unsupported encoding", func(b []byte) []byte { binary.LittleEndian.PutUint16(b[20:], 6); return b }},
		{"unsupported bit depth", func(b []byte) []byte { binary.LittleEndian.PutUint16(b[34:], 8); return b }},
		{"wrong block alignment", func(b []byte) []byte { binary.LittleEndian.PutUint16(b[32:], 3); return b }},
		{"wrong byte rate", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[28:], 48000); return b }},
		{"incomplete frame", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[40:], 3); return b }},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := test.change(testwav.Bytes(48000, 1, 16, 24, 1))
			if h, err := ReadWAVHeader(bytes.NewReader(data)); err == nil {
				t.Fatalf("accepted malformed WAV: %+v", h)
			}
		})
	}
}
func TestReadWAVHeaderSkipsAncillaryChunks(t *testing.T) {
	original := testwav.Bytes(48000, 1, 16, 24, 1)
	// An odd-length JUNK chunk must include a pad byte before fmt.
	junk := []byte{'J', 'U', 'N', 'K', 3, 0, 0, 0, 1, 2, 3, 0}
	data := append(bytes.Clone(original[:12]), junk...)
	data = append(data, original[12:]...)
	binary.LittleEndian.PutUint32(data[4:], uint32(len(data)-8))
	h, err := ReadWAVHeader(bytes.NewReader(data))
	if err != nil || h.Frames != 24 || h.BitDepth != 16 {
		t.Fatalf("ancillary chunk: %+v %v", h, err)
	}
}
func FuzzReadWAVHeader(f *testing.F) {
	f.Add(testwav.Bytes(48000, 1, 16, 24, 1))
	f.Add(testwav.Bytes(44100, 2, 32, 17, 3))
	f.Add([]byte("RIFF"))
	f.Fuzz(func(t *testing.T, data []byte) {
		h, err := ReadWAVHeader(bytes.NewReader(data))
		if err == nil && (h.Frames <= 0 || h.Channels < 1 || h.Channels > 2 || h.RateHz < 8000 || h.RateHz > 384000) {
			t.Fatalf("invalid successful header: %+v", h)
		}
	})
}
