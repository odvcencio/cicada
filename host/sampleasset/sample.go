// Package sampleasset prepares verified project assets for the sample voice.
// File access and decoding finish before a region reaches an audio owner.
package sampleasset

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"

	"m31labs.dev/cicada/audioasset"
	"m31labs.dev/cicada/kernel/voice/sample"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// MaxFrames bounds the resident PCM of one prepared asset (64 MiB stereo).
const MaxFrames = 8 << 20

// LoadSampler connects a lane A sampler declaration to lane B's bounded pool.
func LoadSampler(dir string, p *project.Project, name string) (*sample.Pool, error) {
	if p == nil {
		return nil, fmt.Errorf("sample project is missing")
	}
	for _, sampler := range p.Samplers {
		if sampler.Name != name {
			continue
		}
		if sampler.RootMIDI < 12 || sampler.RootMIDI > 95 || sampler.Voices < 1 || sampler.Voices > 32 ||
			(sampler.Mode != "oneshot" && sampler.Mode != "loop") {
			return nil, fmt.Errorf("invalid sampler declaration %s", name)
		}
		for _, asset := range p.Assets {
			if asset.Name == sampler.Asset {
				region, err := LoadRegion(dir, asset, 0, 0, sampler.RootMIDI, sampler.Mode == "loop")
				if err != nil {
					return nil, err
				}
				return sample.NewPool(48000, sampler.Voices, region)
			}
		}
		return nil, fmt.Errorf("sampler %s references an unknown asset", name)
	}
	return nil, fmt.Errorf("unknown sampler %s", name)
}

// LoadRegion retains source frame coordinates. A zero end selects the full
// asset; a mono asset uses an empty right channel, as required by sample.Region.
func LoadRegion(dir string, asset project.Asset, start, end int64, rootKey int, loop bool) (sample.Region, error) {
	var region sample.Region
	if !notation.ValidAssetPath(asset.Path) || asset.Format != "wav" || rootKey < 0 || rootKey > 127 {
		return region, fmt.Errorf("invalid sample asset path, format or root key")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return region, err
	}
	defer root.Close()
	f, err := root.Open(asset.Path)
	if err != nil {
		return region, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return region, err
	}
	if !info.Mode().IsRegular() {
		return region, fmt.Errorf("sample asset must be a regular file")
	}
	hash, err := audioasset.SHA256(f)
	if err != nil {
		return region, err
	}
	if hash != asset.SHA256 {
		return region, fmt.Errorf("sample asset hash: expected %s; actual %s", asset.SHA256, hash)
	}
	h, err := audioasset.ReadWAVHeader(f)
	if err != nil {
		return region, err
	}
	if h.Frames != asset.Frames || h.RateHz != asset.RateHz || h.Channels != asset.Channels {
		return region, fmt.Errorf("sample asset dimensions differ from the declaration")
	}
	if h.Frames > MaxFrames {
		return region, fmt.Errorf("sample asset exceeds %d resident frames", MaxFrames)
	}
	if end == 0 {
		end = h.Frames
	}
	// Validate before allocation, including rates unsupported by the sample DSP.
	if start < 0 || end <= start || end > h.Frames || h.RateHz > 192000 {
		return region, fmt.Errorf("sample region bounds or source rate are unsupported")
	}
	region = sample.Region{Left: make([]float32, int(h.Frames)), SampleRate: h.RateHz,
		RootKey: uint8(rootKey), Start: int(start), End: int(end), Loop: loop,
		LoopStart: int(start), LoopEnd: int(end)}
	if h.Channels == 2 {
		region.Right = make([]float32, int(h.Frames))
	}
	// The header validator has already checked every chunk and its padding.
	for offset := int64(12); offset < info.Size(); {
		if _, err = f.Seek(offset, io.SeekStart); err != nil {
			return sample.Region{}, err
		}
		var chunk [8]byte
		if _, err = io.ReadFull(f, chunk[:]); err != nil {
			return sample.Region{}, err
		}
		length := int64(binary.LittleEndian.Uint32(chunk[4:]))
		if string(chunk[:4]) == "data" {
			reader := bufio.NewReaderSize(io.LimitReader(f, length), 64<<10)
			bytesPerSample := h.BitDepth / 8
			var frame [8]byte
			for i := range region.Left {
				if _, err = io.ReadFull(reader, frame[:bytesPerSample*h.Channels]); err != nil {
					return sample.Region{}, err
				}
				for channel := 0; channel < h.Channels; channel++ {
					b := frame[channel*bytesPerSample:]
					var value float32
					switch {
					case h.Encoding == "float":
						value = math.Float32frombits(binary.LittleEndian.Uint32(b))
					case h.BitDepth == 16:
						value = float32(int16(binary.LittleEndian.Uint16(b))) / 32768
					case h.BitDepth == 24:
						integer := int32(b[0]) | int32(b[1])<<8 | int32(b[2])<<16
						value = float32(integer<<8>>8) / 8388608
					default:
						value = float32(float64(int32(binary.LittleEndian.Uint32(b))) / 2147483648)
					}
					if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
						return sample.Region{}, fmt.Errorf("sample asset contains non-finite PCM at frame %d", i)
					}
					if channel == 0 {
						region.Left[i] = value
					} else {
						region.Right[i] = value
					}
				}
			}
			return region, region.Validate()
		}
		offset += 8 + length + (length & 1)
	}
	return sample.Region{}, fmt.Errorf("sample asset has no data chunk")
}
