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
	"sync/atomic"

	"m31labs.dev/cicada/audioasset"
	"m31labs.dev/cicada/host/audioencoding"
	"m31labs.dev/cicada/kernel/voice/sample"
)

const Format = "cicada.instrument-pack/1"
const MaxPCMBytes = 256 << 20
const MaxManifestBytes = 2 << 20

type Asset struct {
	ID           string  `json:"id"`
	Path         string  `json:"path"`
	SHA256       string  `json:"sha256"`
	Bytes        int64   `json:"bytes"`
	WAVSHA256    string  `json:"wav_sha256,omitempty"`
	WAVBytes     int64   `json:"wav_bytes,omitempty"`
	Encoding     string  `json:"encoding,omitempty"`
	PCMHash      string  `json:"pcm_sha256,omitempty"`
	Scale        float32 `json:"scale,omitempty"`
	Frames       int     `json:"frames"`
	Rate         int     `json:"rate"`
	Channels     int     `json:"channels"`
	SourceURL    string  `json:"source_url"`
	SourceSHA256 string  `json:"source_sha256"`
	License      string  `json:"license"`
	LicenseURL   string  `json:"license_url"`
	Attribution  string  `json:"attribution,omitempty"`
}

func (a Asset) encodedAudio() audioencoding.Asset {
	return audioencoding.Asset{Encoding: a.Encoding, SHA256: a.SHA256, Bytes: a.Bytes,
		DecodedBytes: a.WAVBytes, PCMHash: a.PCMHash, Frames: a.Frames,
		Rate: a.Rate, Channels: a.Channels, Scale: a.Scale}
}

func (a Asset) validEncoding() bool {
	if a.Encoding == "" {
		return strings.HasSuffix(a.Path, ".wav.gz") && validHash(a.WAVSHA256) && a.WAVBytes >= 44 && a.WAVBytes <= MaxPCMBytes && a.PCMHash == "" && a.Scale == 0
	}
	if a.encodedAudio().Validate() != nil {
		return false
	}
	if a.Encoding == "flac" {
		return strings.HasSuffix(a.Path, ".flac") && a.WAVBytes == 0 && a.WAVSHA256 == ""
	}
	return strings.HasSuffix(a.Path, ".wav.gz") && validHash(a.WAVSHA256)
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

// User recordings retain their source checksum without inventing remote
// URLs or granting a public redistribution license.
func validLicense(a Asset) bool {
	if a.License == "user recording" || a.License == "owner recording" {
		return a.SourceURL == "" && a.LicenseURL == "" && a.Attribution == ""
	}
	return remoteURL(a.SourceURL) && remoteURL(a.LicenseURL) &&
		(a.License == "CC0-1.0" || a.License == "CC-BY-4.0" && a.Attribution != "")
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
		if a.ID == "" || ids[a.ID] || !ValidPath(a.Path) || !a.validEncoding() || !validHash(a.SHA256) || !validHash(a.SourceSHA256) || a.Bytes < 1 || a.Bytes > MaxPCMBytes || a.Frames < 1 || a.Frames > 8<<20 || a.Rate < 8000 || a.Rate > 192000 || a.Channels < 1 || a.Channels > 2 || !validLicense(a) {
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
	return LoadAtRate(dir, manifestPath, pin, 48000)
}

// LoadAtRate admits a pack for the requested output rate before playback. It
// decodes the audio on every call. A native host that compiles one project more
// than once opens its packs with ResidentCap.Open and loads them with
// Pack.LoadShared instead, which shares one decoded copy.
func LoadAtRate(dir, manifestPath, pin string, rate int) (*Prepared, error) {
	pack, err := Open(dir, manifestPath, pin)
	if err != nil {
		return nil, err
	}
	return pack.Decode(rate)
}

// Pack is a pack whose manifest has been read and checked, and whose audio has
// not been read. Its manifest says how much PCM the audio decodes to, so a host
// can refuse a pack that is too large before it allocates any of it.
type Pack struct {
	dir, manifestPath, digest string
	manifest                  Manifest
}

// Open reads the manifest at manifestPath inside dir, checks it against the
// optional pin and validates it. It reads no audio.
func Open(dir, manifestPath, pin string) (*Pack, error) {
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
	sum := digest(data)
	if pin != "" && sum != pin {
		return nil, fmt.Errorf("instrument manifest hash mismatch")
	}
	m, err := DecodeManifest(data)
	if err != nil {
		return nil, err
	}
	return &Pack{dir: dir, manifestPath: manifestPath, digest: sum, manifest: m}, nil
}

// Path is the manifest path as the caller spelled it to Open.
func (pk *Pack) Path() string { return pk.manifestPath }

// Manifest is the validated manifest. Treat its slices as read-only.
func (pk *Pack) Manifest() Manifest { return pk.manifest }

// ResidentPCMBytes is the float32 PCM that decoding the pack leaves in memory,
// from the manifest alone.
func (pk *Pack) ResidentPCMBytes() int64 { return pk.manifest.ResidentPCMBytes() }

// ResidentPCMBytes is the float32 PCM that decoding the pack leaves in memory:
// four bytes per sample and channel of each asset that a zone plays. An asset
// that no zone plays is decoded to verify it and then dropped, so it does not
// count. Zones that share an asset share its PCM, so the asset counts once.
func (m Manifest) ResidentPCMBytes() int64 {
	played := make(map[string]bool, len(m.Zones))
	for _, z := range m.Zones {
		played[z.Asset] = true
	}
	var total int64
	for _, a := range m.Assets {
		if played[a.ID] {
			total += int64(a.Frames) * int64(a.Channels) * 4
		}
	}
	return total
}

// CheckFiles confirms, without reading any audio, that each asset is a regular
// file in the pack's folder at the size its manifest declares. It catches a
// missing or truncated file, not a damaged one: Decode checks the hashes.
func (pk *Pack) CheckFiles() error {
	root, err := os.OpenRoot(pk.dir)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, a := range pk.manifest.Assets {
		info, err := root.Stat(path.Join(path.Dir(pk.manifestPath), a.Path))
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() != a.Bytes {
			return fmt.Errorf("compressed sample hash/size mismatch: %s", a.ID)
		}
	}
	return nil
}

var decodes atomic.Uint64

// Decodes counts the times this process has decoded a pack's audio, whether for
// one caller (Decode, DecodeUnadmitted, LoadAtRate) or on a miss of the shared
// cache. It lets a test or a diagnostic tell a reused pack from a decoded one.
func Decodes() uint64 { return decodes.Load() }

// Decode reads, verifies and decodes the pack's audio, and admits the pack for
// the output rate. Every call decodes a new copy of the PCM.
func (pk *Pack) Decode(rate int) (*Prepared, error) {
	p, err := pk.DecodeUnadmitted()
	if err != nil {
		return nil, err
	}
	// Validate maps, PCM bounds and ratios before returning any prepared bank.
	if err = p.Admit(rate); err != nil {
		return nil, err
	}
	return p, nil
}

// DecodeUnadmitted is Decode without the check that the pack can play at an
// output rate. A caller that admits several rates decodes once and calls Admit
// for each.
func (pk *Pack) DecodeUnadmitted() (*Prepared, error) {
	decodes.Add(1)
	root, err := os.OpenRoot(pk.dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	m := pk.manifest
	p := &Prepared{Manifest: m}
	regions := map[string]sample.Region{}
	for _, a := range m.Assets {
		f, err := root.Open(path.Join(path.Dir(pk.manifestPath), a.Path))
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
	// A host reserves the manifest's figure against its memory cap before it
	// decodes, so the PCM must be what the manifest says.
	if held, declared := p.ResidentPCMBytes(), m.ResidentPCMBytes(); held != declared {
		return nil, fmt.Errorf("pack %s decodes to %d bytes of PCM, but its manifest declares %d", m.ID, held, declared)
	}
	return p, nil
}

// Admit checks that the pack can play at the output rate: the zone maps are
// unambiguous, the PCM bounds hold and every playback ratio stays within the
// voice's range. Decoding admits the pack at one rate; Admit asks again for
// another.
func (p *Prepared) Admit(rate int) error {
	_, err := p.New(rate)
	return err
}

func DecodeAsset(a Asset, compressed []byte) (sample.Region, error) {
	var r sample.Region
	if !a.validEncoding() {
		return r, fmt.Errorf("invalid sample encoding: %s", a.ID)
	}
	if a.Encoding != "" {
		pcm, err := audioencoding.Decode(a.encodedAudio(), compressed)
		if err != nil {
			return r, fmt.Errorf("sample %s: %w", a.ID, err)
		}
		if a.Encoding == "wav-gzip" {
			g, err := gzip.NewReader(bytes.NewReader(compressed))
			if err != nil {
				return r, err
			}
			h := sha256.New()
			n, err := io.Copy(h, io.LimitReader(g, a.WAVBytes+1))
			g.Close()
			if err != nil || n != a.WAVBytes || hex.EncodeToString(h.Sum(nil)) != a.WAVSHA256 {
				return r, fmt.Errorf("decoded WAV hash/size mismatch: %s", a.ID)
			}
		}
		r = sample.Region{Left: pcm[0], SampleRate: a.Rate, End: a.Frames}
		if a.Channels == 2 {
			r.Right = pcm[1]
		}
		return r, nil
	}
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
