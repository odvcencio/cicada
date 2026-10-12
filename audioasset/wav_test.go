package audioasset

import (
	"bytes"
	"encoding/binary"
	"fmt"
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

func extensibleWAV(channels, bits, validBits, encoding int, mask uint32) []byte {
	original := testwav.Bytes(48000, channels, bits, 24, encoding)
	data := append(bytes.Clone(original[:36]), make([]byte, 24)...)
	data = append(data, original[36:]...)
	binary.LittleEndian.PutUint32(data[4:], uint32(len(data)-8))
	binary.LittleEndian.PutUint32(data[16:], 40)
	binary.LittleEndian.PutUint16(data[20:], 0xfffe)
	binary.LittleEndian.PutUint16(data[36:], 22)
	binary.LittleEndian.PutUint16(data[38:], uint16(validBits))
	binary.LittleEndian.PutUint32(data[40:], mask)
	copy(data[44:60], []byte{byte(encoding), 0, 0, 0, 0, 0, 0x10, 0, 0x80, 0, 0, 0xaa, 0, 0x38, 0x9b, 0x71})
	return data
}

func TestReadWAVHeaderExtensiblePCMAndFloat(t *testing.T) {
	for _, channels := range []int{1, 2} {
		for _, bits := range []int{16, 24, 32} {
			for _, mask := range []uint32{0, map[int]uint32{1: 4, 2: 3}[channels]} {
				t.Run(fmt.Sprintf("PCM%d/%dch/mask%d", bits, channels, mask), func(t *testing.T) {
					data := extensibleWAV(channels, bits, bits, 1, mask)
					h, err := ReadWAVHeader(bytes.NewReader(data))
					if err != nil || h.Encoding != "pcm" || h.Channels != channels || h.BitDepth != bits || h.Frames != 24 || h.DataOffset != 68 {
						t.Fatalf("extensible PCM: %+v %v", h, err)
					}
				})
			}
		}
		h, err := ReadWAVHeader(bytes.NewReader(extensibleWAV(channels, 32, 32, 3, 0)))
		if err != nil || h.Encoding != "float" || h.BitDepth != 32 || h.Channels != channels {
			t.Fatalf("extensible float: %+v %v", h, err)
		}
	}
	// Reduced-precision integer samples still occupy the full signed container.
	for _, depth := range [][2]int{{24, 20}, {32, 24}} {
		h, err := ReadWAVHeader(bytes.NewReader(extensibleWAV(2, depth[0], depth[1], 1, 3)))
		if err != nil || h.BitDepth != depth[0] || h.Encoding != "pcm" {
			t.Fatalf("left-aligned PCM: %+v %v", h, err)
		}
	}
}

func TestReadWAVHeaderExtensibleWithLISTAndPadding(t *testing.T) {
	original := extensibleWAV(2, 24, 24, 1, 3)
	list := []byte{'L', 'I', 'S', 'T', 5, 0, 0, 0, 'I', 'N', 'F', 'O', 0, 0}
	data := append(bytes.Clone(original[:60]), list...)
	data = append(data, original[60:]...)
	binary.LittleEndian.PutUint32(data[4:], uint32(len(data)-8))
	h, err := ReadWAVHeader(bytes.NewReader(data))
	if err != nil || h.DataOffset != 82 || h.Frames != 24 || h.BitDepth != 24 {
		t.Fatalf("extensible WAV with ancillary chunk: %+v %v", h, err)
	}
}

func TestReadWAVHeaderRejectsMalformedExtensible(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func([]byte) []byte
	}{
		{"short fmt", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[16:], 39); return b }},
		{"short extension declaration", func(b []byte) []byte { binary.LittleEndian.PutUint16(b[36:], 21); return b }},
		{"extension exceeds fmt", func(b []byte) []byte { binary.LittleEndian.PutUint16(b[36:], 23); return b }},
		{"truncated extension", func(b []byte) []byte { b = b[:59]; binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8)); return b }},
		{"zero valid bits", func(b []byte) []byte { binary.LittleEndian.PutUint16(b[38:], 0); return b }},
		{"valid bits exceed container", func(b []byte) []byte { binary.LittleEndian.PutUint16(b[38:], 25); return b }},
		{"float precision", func(b []byte) []byte { return extensibleWAV(2, 32, 24, 3, 3) }},
		{"unsupported PCM container", func(b []byte) []byte { return extensibleWAV(2, 8, 8, 1, 3) }},
		{"unsupported float container", func(b []byte) []byte { return extensibleWAV(2, 64, 32, 3, 3) }},
		{"compressed subtype", func(b []byte) []byte { b[44] = 6; return b }},
		{"GUID high tag bytes", func(b []byte) []byte { b[46] = 1; return b }},
		{"GUID tail", func(b []byte) []byte { b[59] ^= 1; return b }},
		{"GUID byte order", func(b []byte) []byte { b[50], b[51] = b[51], b[50]; return b }},
		{"stereo mask mismatch", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[40:], 4); return b }},
		{"unsupported speaker layout", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[40:], 0x30); return b }},
		{"multichannel", func(b []byte) []byte { return extensibleWAV(6, 24, 24, 1, 0) }},
		{"RF64", func(b []byte) []byte { copy(b[:4], "RF64"); return b }},
	} {
		t.Run(test.name, func(t *testing.T) {
			if h, err := ReadWAVHeader(bytes.NewReader(test.change(extensibleWAV(2, 24, 24, 1, 3)))); err == nil {
				t.Fatalf("accepted malformed extensible WAV: %+v", h)
			}
		})
	}
}

func FuzzReadWAVHeader(f *testing.F) {
	f.Add(testwav.Bytes(48000, 1, 16, 24, 1))
	f.Add(testwav.Bytes(44100, 2, 32, 17, 3))
	f.Add(extensibleWAV(2, 24, 24, 1, 3))
	f.Add(extensibleWAV(1, 32, 32, 3, 4))
	f.Add([]byte("RIFF"))
	f.Fuzz(func(t *testing.T, data []byte) {
		h, err := ReadWAVHeader(bytes.NewReader(data))
		if err == nil && (h.Frames <= 0 || h.Channels < 1 || h.Channels > 2 || h.RateHz < 8000 || h.RateHz > 384000) {
			t.Fatalf("invalid successful header: %+v", h)
		}
	})
}
