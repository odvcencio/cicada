package main

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"

	"m31labs.dev/cicada/audioasset"
	"m31labs.dev/cicada/kernel/voice/sample"
)

const maximumAssetBytes = 64 << 20

type packAsset struct {
	ID           string `json:"id"`
	Path         string `json:"path"`
	SHA256       string `json:"sha256"`
	WAVSHA256    string `json:"wav_sha256"`
	SourceSHA256 string `json:"source_sha256"`
	SourceURL    string `json:"source_url"`
	License      string `json:"license"`
	LicenseURL   string `json:"license_url"`
	Bytes        int    `json:"bytes"`
	WAVBytes     int    `json:"wav_bytes"`
	Rate         int    `json:"rate"`
	Channels     int    `json:"channels"`
	Frames       int    `json:"frames"`
}

type zone struct {
	Asset                                                   string `json:"asset"`
	Root, KeyLow, KeyHigh, VelocityLow, VelocityHigh, Layer int
	Start, End, LoopStart, LoopEnd                          int
	Position, Count                                         int
	Gain, TuneCents                                         float64
	Release, Loop, OneShot                                  bool
}

type packManifest struct {
	Format string      `json:"format"`
	ID     string      `json:"id"`
	Assets []packAsset `json:"assets"`
	Zones  []zone      `json:"zones"`
	Config struct {
		Gain float64
		Amp  struct{ Attack, Release float64 }
	} `json:"config"`
}

type samplePack struct {
	manifest   packManifest
	path, hash string
	assets     map[string]packAsset
	regions    map[string]sample.Region
}

func loadPack(path string) (*samplePack, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		path = filepath.Join(path, "manifest.json")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	b, err := io.ReadAll(io.LimitReader(f, 2<<20))
	closeErr := f.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(b) == 2<<20 {
		return nil, fmt.Errorf("pack manifest exceeds 2 MiB")
	}
	p := &samplePack{path: filepath.Dir(path), hash: fmt.Sprintf("%x", sha256.Sum256(b)), assets: make(map[string]packAsset), regions: make(map[string]sample.Region)}
	if err := json.Unmarshal(b, &p.manifest); err != nil {
		return nil, err
	}
	if p.manifest.Format != "cicada.instrument-pack/1" || p.manifest.ID != "grand" || len(p.manifest.Zones) == 0 {
		return nil, fmt.Errorf("expected a cicada.instrument-pack/1 grand pack")
	}
	if p.manifest.Config.Gain <= 0 || p.manifest.Config.Gain > 16 || p.manifest.Config.Amp.Attack < 0 || p.manifest.Config.Amp.Attack > 10000 || p.manifest.Config.Amp.Release < 0 || p.manifest.Config.Amp.Release > 10000 {
		return nil, fmt.Errorf("invalid grand envelope configuration")
	}
	for _, a := range p.manifest.Assets {
		if _, exists := p.assets[a.ID]; exists || a.ID == "" || a.License != "CC0-1.0" || len(a.SHA256) != 64 || len(a.WAVSHA256) != 64 || len(a.SourceSHA256) != 64 || a.SourceURL == "" || a.LicenseURL == "" || !filepath.IsLocal(a.Path) || a.Bytes < 1 || a.Bytes > maximumAssetBytes || a.WAVBytes < 1 || a.WAVBytes > maximumAssetBytes {
			return nil, fmt.Errorf("invalid or non-CC0 grand asset %q", a.ID)
		}
		p.assets[a.ID] = a
	}
	for _, z := range p.manifest.Zones {
		if _, exists := p.assets[z.Asset]; !exists || z.Root < 0 || z.Root > 127 || z.KeyLow < 0 || z.KeyHigh > 127 || z.KeyLow > z.KeyHigh || z.VelocityLow < 1 || z.VelocityHigh > 127 || z.VelocityLow > z.VelocityHigh || z.Layer < 1 || z.Layer > 127 || z.Gain < 0 || z.Gain > 16 || math.IsNaN(z.Gain) || math.IsInf(z.Gain, 0) || math.IsNaN(z.TuneCents) || math.IsInf(z.TuneCents, 0) || z.Loop || z.OneShot || z.Position != 0 || z.Count != 1 {
			return nil, fmt.Errorf("grand zone %q is invalid or unsupported", z.Asset)
		}
	}
	return p, nil
}

func (p *samplePack) region(z zone) (sample.Region, error) {
	r, exists := p.regions[z.Asset]
	if !exists {
		a := p.assets[z.Asset]
		root, err := os.OpenRoot(p.path)
		if err != nil {
			return sample.Region{}, err
		}
		f, err := root.Open(a.Path)
		if err != nil {
			root.Close()
			return sample.Region{}, err
		}
		compressed, err := io.ReadAll(io.LimitReader(f, maximumAssetBytes+1))
		fileErr, rootErr := f.Close(), root.Close()
		if err != nil {
			return sample.Region{}, err
		}
		if fileErr != nil {
			return sample.Region{}, fileErr
		}
		if rootErr != nil {
			return sample.Region{}, rootErr
		}
		if len(compressed) != a.Bytes || fmt.Sprintf("%x", sha256.Sum256(compressed)) != a.SHA256 {
			return sample.Region{}, fmt.Errorf("compressed grand asset hash/length mismatch: %s", a.ID)
		}
		gz, err := gzip.NewReader(bytes.NewReader(compressed))
		if err != nil {
			return sample.Region{}, err
		}
		wav, err := io.ReadAll(io.LimitReader(gz, maximumAssetBytes+1))
		gzErr := gz.Close()
		if err != nil {
			return sample.Region{}, err
		}
		if gzErr != nil {
			return sample.Region{}, gzErr
		}
		if len(wav) != a.WAVBytes || fmt.Sprintf("%x", sha256.Sum256(wav)) != a.WAVSHA256 {
			return sample.Region{}, fmt.Errorf("decoded grand WAV hash/length mismatch: %s", a.ID)
		}
		r, err = decodeWAV(wav)
		if err != nil {
			return sample.Region{}, err
		}
		if len(r.Left) != a.Frames || r.SampleRate != a.Rate || len(r.Right) > 0 && a.Channels != 2 || len(r.Right) == 0 && a.Channels != 1 {
			return sample.Region{}, fmt.Errorf("grand asset dimensions mismatch: %s", a.ID)
		}
		p.regions[z.Asset] = r
	}
	r.RootKey, r.Start = uint8(z.Root), z.Start
	r.End = z.End
	if r.End == 0 {
		r.End = len(r.Left)
	}
	return r, r.Validate()
}

func decodeWAV(b []byte) (sample.Region, error) {
	h, err := audioasset.ReadWAVHeader(bytes.NewReader(b))
	if err != nil {
		return sample.Region{}, err
	}
	if h.Frames > sampleFramesLimit {
		return sample.Region{}, fmt.Errorf("grand WAV exceeds resident frame limit")
	}
	r := sample.Region{Left: make([]float32, h.Frames), SampleRate: h.RateHz, End: int(h.Frames)}
	if h.Channels == 2 {
		r.Right = make([]float32, h.Frames)
	}
	width := h.BitDepth / 8
	for i := range r.Left {
		for c := 0; c < h.Channels; c++ {
			data := b[int(h.DataOffset)+(i*h.Channels+c)*width:]
			var value float32
			switch {
			case h.Encoding == "float":
				value = math.Float32frombits(binary.LittleEndian.Uint32(data))
			case width == 2:
				value = float32(int16(binary.LittleEndian.Uint16(data))) / 32768
			case width == 3:
				n := int32(data[0]) | int32(data[1])<<8 | int32(data[2])<<16
				value = float32(n<<8>>8) / 8388608
			case width == 4:
				value = float32(float64(int32(binary.LittleEndian.Uint32(data))) / 2147483648)
			}
			if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
				return sample.Region{}, fmt.Errorf("grand WAV contains non-finite PCM")
			}
			if c == 0 {
				r.Left[i] = value
			} else {
				r.Right[i] = value
			}
		}
	}
	return r, r.Validate()
}

const sampleFramesLimit = 8 << 20

// Layer blending follows the pack's linear amplitude weights between centres.
func (p *samplePack) selectZones(note, velocity uint8, release bool) ([]zone, []float64, error) {
	var zones []zone
	for _, z := range p.manifest.Zones {
		if z.Release == release && int(note) >= z.KeyLow && int(note) <= z.KeyHigh && int(velocity) >= z.VelocityLow && int(velocity) <= z.VelocityHigh {
			zones = append(zones, z)
		}
	}
	if len(zones) == 0 {
		if release {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("grand pack has no zone for MIDI %d velocity %d", note, velocity)
	}
	sort.Slice(zones, func(i, j int) bool { return zones[i].Layer < zones[j].Layer })
	if int(velocity) <= zones[0].Layer {
		return zones[:1], []float64{1}, nil
	}
	for i := 1; i < len(zones); i++ {
		if zones[i].Layer == zones[i-1].Layer {
			return nil, nil, fmt.Errorf("ambiguous grand dynamic layer")
		}
		if int(velocity) <= zones[i].Layer {
			weight := float64(int(velocity)-zones[i-1].Layer) / float64(zones[i].Layer-zones[i-1].Layer)
			return zones[i-1 : i+1], []float64{1 - weight, weight}, nil
		}
	}
	return zones[len(zones)-1:], []float64{1}, nil
}

type preparedNote struct {
	attack, release           [2]sample.Voice
	attackCount, releaseCount int
}

type sampledSlot struct {
	preparedNote
	note, velocity  uint8
	held, releasing bool
	age, releaseAge uint64
	serial          uint64
}

type sampledInstrument struct {
	prepared                    map[[2]uint8]preparedNote
	slots                       [8]sampledSlot
	pedal                       bool
	serial                      uint64
	attackFrames, releaseFrames uint64
}

func (p *samplePack) prepare(f fixture) (*sampledInstrument, error) {
	s := &sampledInstrument{prepared: make(map[[2]uint8]preparedNote), attackFrames: uint64(math.Round(p.manifest.Config.Amp.Attack * outputRate / 1000)), releaseFrames: max(1, uint64(math.Round(p.manifest.Config.Amp.Release*outputRate/1000)))}
	for _, e := range f.Events {
		if e.Kind != "on" {
			continue
		}
		key := [2]uint8{e.Note, e.Velocity}
		if _, exists := s.prepared[key]; exists {
			continue
		}
		var n preparedNote
		for _, release := range []bool{false, true} {
			zones, weights, err := p.selectZones(e.Note, e.Velocity, release)
			if err != nil {
				return nil, err
			}
			for i, z := range zones {
				r, err := p.region(z)
				if err != nil {
					return nil, err
				}
				v, err := sample.New(outputRate, r)
				if err != nil {
					return nil, err
				}
				if err = v.SetParams(sample.Params{Gain: p.manifest.Config.Gain * z.Gain * weights[i], FineTuneCents: z.TuneCents}); err != nil {
					return nil, err
				}
				if release {
					n.release[i] = *v
				} else {
					n.attack[i] = *v
				}
			}
			if release {
				n.releaseCount = len(zones)
			} else {
				n.attackCount = len(zones)
			}
		}
		s.prepared[key] = n
	}
	return s, nil
}

func (s *sampledInstrument) NoteOn(note, velocity uint8) error {
	n, exists := s.prepared[[2]uint8{note, velocity}]
	if !exists {
		return fmt.Errorf("sample event was not prepared")
	}
	chosen := -1
	for i := range s.slots {
		if !s.slots[i].active() {
			chosen = i
			break
		}
		if chosen < 0 || s.slots[i].serial < s.slots[chosen].serial {
			chosen = i
		}
	}
	s.serial++
	s.slots[chosen] = sampledSlot{preparedNote: n, note: note, velocity: velocity, held: true, serial: s.serial}
	for i := 0; i < n.attackCount; i++ {
		if err := s.slots[chosen].attack[i].NoteOn(note, velocity); err != nil {
			return err
		}
	}
	return nil
}

func (v *sampledSlot) active() bool {
	for i := 0; i < v.attackCount; i++ {
		if v.attack[i].Active() {
			return true
		}
	}
	for i := 0; i < v.releaseCount; i++ {
		if v.release[i].Active() {
			return true
		}
	}
	return false
}

func (s *sampledInstrument) NoteOff(note uint8) {
	// Key-up belongs to the newest held occurrence of a repeated pitch.
	chosen := -1
	for i := range s.slots {
		if s.slots[i].note == note && s.slots[i].held && (chosen < 0 || s.slots[i].serial > s.slots[chosen].serial) {
			chosen = i
		}
	}
	if chosen < 0 {
		return
	}
	s.slots[chosen].held = false
	if !s.pedal {
		s.release(&s.slots[chosen])
	}
}

func (s *sampledInstrument) release(v *sampledSlot) {
	if v.releasing {
		return
	}
	v.releasing = true
	for i := 0; i < v.releaseCount; i++ {
		_ = v.release[i].NoteOn(v.note, v.velocity)
	}
}

func (s *sampledInstrument) SetSustain(value float32) error {
	if value < 0 || value > 1 || math.IsNaN(float64(value)) {
		return fmt.Errorf("invalid sample sustain pedal")
	}
	was := s.pedal
	s.pedal = value >= .5
	if was && !s.pedal {
		for i := range s.slots {
			if !s.slots[i].held {
				s.release(&s.slots[i])
			}
		}
	}
	return nil
}

func (s *sampledInstrument) NextStereo() (float32, float32) {
	var left, right float64
	for slot := range s.slots {
		v := &s.slots[slot]
		gain := 1.0
		if s.attackFrames > 0 && v.age < s.attackFrames {
			gain = float64(v.age) / float64(s.attackFrames)
		}
		if v.releasing {
			gain *= math.Max(0, 1-float64(v.releaseAge)/float64(s.releaseFrames))
		}
		for i := 0; i < v.attackCount; i++ {
			l, r := v.attack[i].NextStereo()
			left, right = left+float64(l)*gain, right+float64(r)*gain
		}
		if v.releasing {
			for i := 0; i < v.releaseCount; i++ {
				l, r := v.release[i].NextStereo()
				left, right = left+float64(l), right+float64(r)
			}
			v.releaseAge++
			if v.releaseAge == s.releaseFrames {
				for i := 0; i < v.attackCount; i++ {
					v.attack[i].Reset()
				}
			}
		}
		v.age++
	}
	return float32(left), float32(right)
}

func (s *sampledInstrument) Reset() { s.slots = [8]sampledSlot{}; s.pedal = false; s.serial = 0 }

func (p *samplePack) usedAssets() []packAsset {
	var assets []packAsset
	for id := range p.regions {
		assets = append(assets, p.assets[id])
	}
	sort.Slice(assets, func(i, j int) bool { return assets[i].ID < assets[j].ID })
	return assets
}
