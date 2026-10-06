// Package instrumentpack admits checksummed, independently licensed audio
// packs before playback. Pack decoding never runs on an audio callback.
package instrumentpack

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"path"
	"strings"

	"m31labs.dev/cicada/audioasset"
	"m31labs.dev/cicada/kernel/voice/sample"
)

const Format = "cicada.instrument-pack/1"
const MaxPCMBytes = 256 << 20
const MaxManifestBytes = 2 << 20

type Asset struct {
	ID           string `json:"id"`
	Path         string `json:"path"`
	SHA256       string `json:"sha256"`
	Bytes        int64  `json:"bytes"`
	WAVSHA256    string `json:"wav_sha256"`
	WAVBytes     int64  `json:"wav_bytes"`
	Frames       int    `json:"frames"`
	Rate         int    `json:"rate"`
	Channels     int    `json:"channels"`
	SourceURL    string `json:"source_url"`
	SourceSHA256 string `json:"source_sha256"`
	License      string `json:"license"`
	LicenseURL   string `json:"license_url"`
	Attribution  string `json:"attribution,omitempty"`
}

type Zone struct {
	ChokeGroup                                int
	OneShot                                   bool
	ChokeSustain                              bool   `json:",omitempty"`
	Asset                                     string `json:"asset"`
	Root, KeyLow, KeyHigh                     int
	VelocityLow, VelocityHigh, Layer          int
	Group, Position, Count                    int
	Release                                   bool
	Gain, TuneCents                           float64
	Start, End, LoopStart, LoopEnd, Crossfade int
	Loop                                      bool
}

type Manifest struct {
	Format      string                  `json:"format"`
	ID          string                  `json:"id"`
	Description string                  `json:"description"`
	Config      sample.InstrumentConfig `json:"config"`
	Assets      []Asset                 `json:"assets"`
	Zones       []Zone                  `json:"zones"`
}

type Prepared struct {
	Manifest Manifest
	Zones    []sample.Zone
}

func (p *Prepared) New(rate int) (*sample.Instrument, error) {
	return sample.NewInstrument(rate, p.Zones, p.Manifest.Config)
}
func digest(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
func validHash(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 32 && s == strings.ToLower(s)
}
func remoteURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil
}
func ValidPath(s string) bool {
	return s != "" && !strings.ContainsAny(s, "\\\x00:") && !strings.HasPrefix(s, "/") && path.Clean(s) == s && s != "." && !strings.HasPrefix(s, "../")
}

func DecodeManifest(data []byte) (Manifest, error) {
	var m Manifest
	if len(data) > MaxManifestBytes {
		return m, fmt.Errorf("instrument manifest exceeds limit")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&m); err != nil {
		return m, err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return m, fmt.Errorf("trailing manifest data")
	}
	if m.Format != Format || !ValidPath(m.ID) || strings.Contains(m.ID, "/") || len(m.Assets) < 1 || len(m.Assets) > 4096 || len(m.Zones) < 1 || len(m.Zones) > 4096 {
		return m, fmt.Errorf("invalid instrument manifest")
	}
	ids := map[string]bool{}
	var total int64
	for _, a := range m.Assets {
		if a.ID == "" || ids[a.ID] || !ValidPath(a.Path) || !strings.HasSuffix(a.Path, ".wav.gz") || !validHash(a.SHA256) || !validHash(a.WAVSHA256) || !validHash(a.SourceSHA256) || a.Bytes < 1 || a.Bytes > MaxPCMBytes || a.WAVBytes < 44 || a.WAVBytes > MaxPCMBytes || a.Frames < 1 || a.Frames > 8<<20 || a.Rate < 8000 || a.Rate > 192000 || a.Channels < 1 || a.Channels > 2 || !remoteURL(a.SourceURL) || !remoteURL(a.LicenseURL) || a.License != "CC0-1.0" && a.License != "CC-BY-4.0" || a.License == "CC-BY-4.0" && a.Attribution == "" {
			return m, fmt.Errorf("invalid asset or licence: %s", a.ID)
		}
		ids[a.ID] = true
		total += int64(a.Frames) * int64(a.Channels) * 4
		if total > MaxPCMBytes {
			return m, fmt.Errorf("instrument PCM exceeds %d bytes", MaxPCMBytes)
		}
	}
	for _, z := range m.Zones {
		if !ids[z.Asset] || z.Root < 0 || z.Root > 127 || z.KeyLow < 0 || z.KeyHigh > 127 || z.KeyLow > z.KeyHigh || z.VelocityLow < 1 || z.VelocityHigh > 127 || z.VelocityLow > z.VelocityHigh || z.Layer < 1 || z.Layer > 127 || z.Group < 0 || z.Group > 255 || z.ChokeGroup < 0 || z.ChokeGroup > 255 || z.Position < 0 || z.Count < 1 || z.Count > 32 || z.Position >= z.Count {
			return m, fmt.Errorf("invalid pack zone")
		}
	}
	return m, nil
}

// Load uses a confined directory and an optional pin of the exact manifest.
// Network retrieval is deliberately separate; downloaded packs work offline.
func Load(dir, manifestPath, pin string) (*Prepared, error) {
	if !ValidPath(manifestPath) || pin != "" && !validHash(pin) {
		return nil, fmt.Errorf("invalid pack path or pin")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	f, err := root.Open(manifestPath)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxManifestBytes+1))
	f.Close()
	if err != nil {
		return nil, err
	}
	if pin != "" && digest(data) != pin {
		return nil, fmt.Errorf("instrument manifest hash mismatch")
	}
	m, err := DecodeManifest(data)
	if err != nil {
		return nil, err
	}
	p := &Prepared{Manifest: m}
	regions := map[string]sample.Region{}
	for _, a := range m.Assets {
		f, err := root.Open(path.Join(path.Dir(manifestPath), a.Path))
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(io.LimitReader(f, a.Bytes+1))
		f.Close()
		if err != nil {
			return nil, err
		}
		r, err := DecodeAsset(a, b)
		if err != nil {
			return nil, err
		}
		regions[a.ID] = r
	}
	for _, z := range m.Zones {
		r := regions[z.Asset]
		r.RootKey = uint8(z.Root)
		r.Start = z.Start
		r.End = z.End
		if r.End == 0 {
			r.End = len(r.Left)
		}
		r.Loop = z.Loop
		r.LoopStart = z.LoopStart
		r.LoopEnd = z.LoopEnd
		r.Crossfade = z.Crossfade
		p.Zones = append(p.Zones, sample.Zone{ChokeGroup: uint8(z.ChokeGroup), OneShot: z.OneShot, ChokeSustain: z.ChokeSustain, Region: r, KeyLow: uint8(z.KeyLow), KeyHigh: uint8(z.KeyHigh), VelocityLow: uint8(z.VelocityLow), VelocityHigh: uint8(z.VelocityHigh), Layer: uint8(z.Layer), Group: uint8(z.Group), Position: uint8(z.Position), Count: uint8(z.Count), Release: z.Release, Gain: z.Gain, TuneCents: z.TuneCents})
	}
	// Validate maps, PCM bounds and ratios before returning any prepared bank.
	if _, err = p.New(48000); err != nil {
		return nil, err
	}
	return p, nil
}

func DecodeAsset(a Asset, compressed []byte) (sample.Region, error) {
	var r sample.Region
	if int64(len(compressed)) != a.Bytes || digest(compressed) != a.SHA256 {
		return r, fmt.Errorf("compressed sample hash/size mismatch: %s", a.ID)
	}
	g, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return r, err
	}
	g.Multistream(false)
	b, err := io.ReadAll(io.LimitReader(g, a.WAVBytes+1))
	g.Close()
	if err != nil {
		return r, err
	}
	if int64(len(b)) != a.WAVBytes || digest(b) != a.WAVSHA256 {
		return r, fmt.Errorf("decoded sample hash/size mismatch: %s", a.ID)
	}
	h, err := audioasset.ReadWAVHeader(bytes.NewReader(b))
	if err != nil {
		return r, err
	}
	if h.Frames != int64(a.Frames) || h.Channels != a.Channels || h.RateHz != a.Rate {
		return r, fmt.Errorf("sample dimensions mismatch: %s", a.ID)
	}
	r = sample.Region{Left: make([]float32, a.Frames), SampleRate: a.Rate, End: a.Frames}
	if a.Channels == 2 {
		r.Right = make([]float32, a.Frames)
	}
	width := h.BitDepth / 8
	offset := int(h.DataOffset)
	for i := 0; i < a.Frames; i++ {
		for ch := 0; ch < a.Channels; ch++ {
			src := b[offset : offset+width]
			offset += width
			var value float32
			switch {
			case h.Encoding == "float":
				value = math.Float32frombits(binary.LittleEndian.Uint32(src))
			case width == 2:
				value = float32(int16(binary.LittleEndian.Uint16(src))) / 32768
			case width == 3:
				n := int32(src[0]) | int32(src[1])<<8 | int32(src[2])<<16
				value = float32(n<<8>>8) / 8388608
			default:
				value = float32(float64(int32(binary.LittleEndian.Uint32(src))) / 2147483648)
			}
			if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
				return sample.Region{}, fmt.Errorf("nonfinite sample PCM")
			}
			if ch == 0 {
				r.Left[i] = value
			} else {
				r.Right[i] = value
			}
		}
	}
	return r, nil
}
