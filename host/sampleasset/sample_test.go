package sampleasset

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/internal/testwav"
	"m31labs.dev/cicada/kernel/voice/sample"
	"m31labs.dev/cicada/project"
)

func TestVerifiedWAVRegionPlayback(t *testing.T) {
	for _, bits := range []int{16, 24, 32} {
		for _, channels := range []int{1, 2} {
			for _, encoding := range []int{1, 3} {
				if encoding == 3 && bits != 32 {
					continue
				}
				t.Run(fmt.Sprintf("bits%d/channels%d/encoding%d", bits, channels, encoding), func(t *testing.T) {
					data := testwav.Bytes(48000, channels, bits, 4, encoding)
					for frame := 0; frame < 4; frame++ {
						for ch := 0; ch < channels; ch++ {
							value := float32(frame-2) / 4
							if ch == 1 {
								value = -value
							}
							b := data[44+(frame*channels+ch)*bits/8:]
							switch {
							case encoding == 3:
								binary.LittleEndian.PutUint32(b, math.Float32bits(value))
							case bits == 16:
								binary.LittleEndian.PutUint16(b, uint16(int16(value*32768)))
							case bits == 24:
								v := int32(value * 8388608)
								b[0], b[1], b[2] = byte(v), byte(v>>8), byte(v>>16)
							default:
								binary.LittleEndian.PutUint32(b, uint32(int32(float64(value)*2147483648)))
							}
						}
					}
					dir := t.TempDir()
					path := filepath.Join(dir, "take.wav")
					if err := os.WriteFile(path, data, 0600); err != nil {
						t.Fatal(err)
					}
					a := project.Asset{Path: "take.wav", Format: "wav", SHA256: fmt.Sprintf("%x", sha256.Sum256(data)), Frames: 4, RateHz: 48000, Channels: channels}
					region, err := LoadRegion(dir, a, 1, 3, 60, true)
					if err != nil {
						t.Fatal(err)
					}
					voice, err := sample.New(48000, region)
					if err != nil {
						t.Fatal(err)
					}
					if err := voice.NoteOn(60, 127); err != nil {
						t.Fatal(err)
					}
					for i := 0; i < 6; i++ {
						left, right := voice.NextStereo()
						want := float32(i%2-1) / 4
						wantRight := want
						if channels == 2 {
							wantRight = -want
						}
						if left != want || right != wantRight {
							t.Fatalf("frame %d: %v/%v != %v/%v", i, left, right, want, wantRight)
						}
					}
					data[44] ^= 1
					if err := os.WriteFile(path, data, 0600); err != nil {
						t.Fatal(err)
					}
					if _, err := LoadRegion(dir, a, 0, 0, 60, false); err == nil {
						t.Fatal("modified immutable asset admitted")
					}
				})
			}
		}
	}
}
