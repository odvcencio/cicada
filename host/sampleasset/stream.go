package sampleasset

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"

	"m31labs.dev/cicada/audioasset"
	"m31labs.dev/cicada/kernel/stream"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// WAVSource retains a verified file, never resident PCM for the full asset.
// One worker owns ReadFrames and Close. The file must remain immutable while
// open, as with pinned sample regions. Hashing/admission happen before playback.
type WAVSource struct {
	file   *os.File
	header audioasset.WAVHeader
	buffer [1024 * 8]byte
}

func OpenStream(dir string, asset project.Asset) (*WAVSource, error) {
	if !notation.ValidAssetPath(asset.Path) || asset.Format != "wav" {
		return nil, fmt.Errorf("invalid streamed asset path or format")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	f, err := root.Open(asset.Path)
	if err != nil {
		return nil, err
	}
	valid := false
	defer func() {
		if !valid {
			f.Close()
		}
	}()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("streamed asset must be a regular file")
	}
	hash, err := audioasset.SHA256(f)
	if err != nil {
		return nil, err
	}
	if hash != asset.SHA256 {
		return nil, fmt.Errorf("streamed asset hash: expected %s; actual %s", asset.SHA256, hash)
	}
	h, err := audioasset.ReadWAVHeader(f)
	if err != nil {
		return nil, err
	}
	if h.Frames != asset.Frames || h.RateHz != asset.RateHz || h.Channels != asset.Channels || h.RateHz > 192000 {
		return nil, fmt.Errorf("streamed asset dimensions differ or source rate is unsupported")
	}
	valid = true
	return &WAVSource{file: f, header: h}, nil
}

func (s *WAVSource) Asset(id uint32) stream.Asset {
	return stream.Asset{ID: id, Frames: s.header.Frames, RateHz: s.header.RateHz, Channels: s.header.Channels}
}
func (s *WAVSource) Close() error { return s.file.Close() }

// ReadFrames decodes bounded chunks with cancellation checks between reads.
// OS reads cannot always be interrupted; the render owner never waits on them.
func (s *WAVSource) ReadFrames(ctx context.Context, first int64, left, right []float32) error {
	if len(left) != len(right) || first < 0 || first > s.header.Frames || int64(len(left)) > s.header.Frames-first {
		return fmt.Errorf("invalid streamed PCM bounds")
	}
	bytesPerSample := s.header.BitDepth / 8
	stride := bytesPerSample * s.header.Channels
	for base := 0; base < len(left); {
		if err := ctx.Err(); err != nil {
			return err
		}
		count := min(1024, len(left)-base)
		data := s.buffer[:count*stride]
		if _, err := s.file.ReadAt(data, s.header.DataOffset+(first+int64(base))*int64(stride)); err != nil {
			return err
		}
		for frame := 0; frame < count; frame++ {
			var l, r float32
			for ch := 0; ch < s.header.Channels; ch++ {
				b := data[(frame*s.header.Channels+ch)*bytesPerSample:]
				var value float32
				switch {
				case s.header.Encoding == "float":
					value = math.Float32frombits(binary.LittleEndian.Uint32(b))
				case s.header.BitDepth == 16:
					value = float32(int16(binary.LittleEndian.Uint16(b))) / 32768
				case s.header.BitDepth == 24:
					integer := int32(b[0]) | int32(b[1])<<8 | int32(b[2])<<16
					value = float32(integer<<8>>8) / 8388608
				default:
					value = float32(float64(int32(binary.LittleEndian.Uint32(b))) / 2147483648)
				}
				if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
					return fmt.Errorf("non-finite streamed PCM at frame %d", first+int64(base+frame))
				}
				if ch == 0 {
					l = value
				} else {
					r = value
				}
			}
			if s.header.Channels == 1 {
				r = l
			}
			left[base+frame], right[base+frame] = l, r
		}
		base += count
	}
	return ctx.Err()
}
