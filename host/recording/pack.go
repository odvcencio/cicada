package recording

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"m31labs.dev/cicada/host/instrumentpack"
	"m31labs.dev/cicada/kernel/voice/sample"
)

// The writer uses the sampler host's exact wire types.
type Asset = instrumentpack.Asset
type Zone = instrumentpack.Zone
type Manifest = instrumentpack.Manifest

// Pack is fully prepared before publication. Files contains only relative,
// generated names. Pin covers the map, licences and every compressed checksum.
type Pack struct {
	Manifest Manifest          `json:"manifest"`
	Pin      string            `json:"sha256"`
	Hits     []Hit             `json:"hits"`
	Files    map[string][]byte `json:"-"`
}

func ValidName(name string) bool {
	if len(name) < 1 || len(name) > 48 || name[0] < 'a' || name[0] > 'z' {
		return false
	}
	for _, c := range name {
		if c != '_' && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

func Build(name string, hits []Hit, layers int) (*Pack, error) {
	if !ValidName(name) || len(hits) == 0 || len(hits) > 256 || layers < 1 || layers > 8 {
		return nil, fmt.Errorf("use a lowercase instrument name, 1–256 hits and 1–8 layers")
	}
	p := &Pack{Hits: hits, Files: map[string][]byte{}, Manifest: Manifest{Format: "cicada.instrument-pack/1", ID: name, Description: "User recording; automatic hit slicing, velocity layers and round robins"}}
	p.Manifest.Config.Voices = 16
	p.Manifest.Config.Gain = 1
	p.Manifest.Config.Amp = sample.Envelope{Attack: 2, Sustain: 1, Release: 80}
	p.Manifest.Config.Filter.Sustain = 1
	p.Manifest.Config.Humanize.Seed = 0
	groups := map[int][]int{}
	for i, h := range hits {
		if h.Root < 0 || h.Root > 127 || h.Rate < 8000 || h.Rate > 192000 || len(h.PCM) == 0 || len(h.PCM) > MaxFrames || len(h.SourceSHA256) != 64 || h.Peak <= 0 {
			return nil, fmt.Errorf("invalid analyzed hit %d", i+1)
		}
		id := fmt.Sprintf("hit_%03d", i+1)
		wav := EncodeWAV(h.PCM, h.Rate)
		var compressed bytes.Buffer
		writer := gzip.NewWriter(&compressed)
		if _, err := writer.Write(wav); err != nil {
			return nil, err
		}
		if err := writer.Close(); err != nil {
			return nil, err
		}
		path := id + ".wav.gz"
		data := compressed.Bytes()
		p.Files[path] = data
		p.Manifest.Assets = append(p.Manifest.Assets, Asset{ID: id, Path: path, SHA256: Digest(data), Bytes: int64(len(data)), WAVSHA256: Digest(wav), WAVBytes: int64(len(wav)), Frames: len(h.PCM), Rate: h.Rate, Channels: 1, SourceSHA256: h.SourceSHA256, License: "user recording"})
		groups[h.Root] = append(groups[h.Root], i)
	}
	roots := make([]int, 0, len(groups))
	for root := range groups {
		roots = append(roots, root)
	}
	sort.Ints(roots)
	for group, root := range roots {
		indices := groups[root]
		sort.SliceStable(indices, func(i, j int) bool { return hits[indices[i]].LoudnessDB < hits[indices[j]].LoudnessDB })
		count := min(layers, len(indices))
		low, high := max(0, root-24), min(127, root+24)
		if group > 0 {
			low = max(low, (roots[group-1]+root)/2+1)
		}
		if group+1 < len(roots) {
			high = min(high, (root+roots[group+1])/2)
		}
		// Keep every source-rate zone within the existing sinc ratio contract.
		for _, i := range indices {
			for low < root && !supportedRatio(low, hits[i]) {
				low++
			}
			for high > root && !supportedRatio(high, hits[i]) {
				high--
			}
		}
		maxPeak := 0.0
		for _, i := range indices {
			maxPeak = max(maxPeak, hits[i].Peak)
		}
		for layer := 0; layer < count; layer++ {
			from, to := layer*len(indices)/count, (layer+1)*len(indices)/count
			if to-from > 32 {
				return nil, fmt.Errorf("more than 32 takes in a velocity layer; choose more layers")
			}
			vlow, vhigh := 1+layer*127/count, (layer+1)*127/count
			for position, i := range indices[from:to] {
				h := hits[i]
				p.Manifest.Zones = append(p.Manifest.Zones, Zone{Asset: p.Manifest.Assets[i].ID, Root: root, KeyLow: low, KeyHigh: high, VelocityLow: vlow, VelocityHigh: vhigh, Layer: (vlow + vhigh) / 2, Group: group + 1, Position: position, Count: to - from, End: len(h.PCM), Gain: h.Peak / maxPeak, TuneCents: h.TuneCents, OneShot: true})
			}
		}
	}
	manifest, err := json.MarshalIndent(p.Manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	manifest = append(manifest, '\n')
	p.Files["manifest.json"] = manifest
	p.Pin = Digest(manifest)
	analysis, err := json.MarshalIndent(hits, "", "  ")
	if err != nil {
		return nil, err
	}
	p.Files["analysis.json"] = append(analysis, '\n')
	p.Files["LICENSE.txt"] = []byte("user recording\nAudio comes from a user-supplied recording. This label records provenance; it grants no public redistribution rights.\n")
	root := hits[0].Root
	p.Files["instrument.cicada"] = []byte(p.Starter("manifest.json", root))
	return p, nil
}

func supportedRatio(note int, h Hit) bool {
	// Both common browser/native output rates must admit the complete map.
	for _, rate := range []float64{44100, 48000, 96000} {
		ratio := float64(h.Rate) / rate * math.Exp2((float64(note-h.Root)+h.TuneCents/100)/12)
		if ratio < .125 || ratio > 8 {
			return false
		}
	}
	return true
}

// Write publishes a complete directory using rename. Existing packs are never
// overwritten; identical content can be reused after verifying every file.
func (p *Pack) Write(dir string) error {
	if !ValidName(p.Manifest.ID) || p.Pin != Digest(p.Files["manifest.json"]) {
		return fmt.Errorf("invalid prepared pack")
	}
	if _, err := os.Stat(dir); err == nil {
		root, err := os.OpenRoot(dir)
		if err != nil {
			return err
		}
		defer root.Close()
		for name, data := range p.Files {
			existing, err := root.ReadFile(name)
			if err != nil || !bytes.Equal(existing, data) {
				return fmt.Errorf("pack destination already exists with different contents")
			}
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(parent, ".recording-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	for name, data := range p.Files {
		if filepath.Base(name) != name || strings.ContainsAny(name, "\\\x00") {
			return fmt.Errorf("invalid pack file name")
		}
		if err := os.WriteFile(filepath.Join(tmp, name), data, 0644); err != nil {
			return err
		}
	}
	return os.Rename(tmp, dir)
}

func (p *Pack) Source(manifestPath string, root int) string {
	return fmt.Sprintf("sampler %s {\n  pack = %q\n  sha256 = %q\n  root = %s\n  voices = 16\n}\n", p.Manifest.ID, filepath.ToSlash(manifestPath), p.Pin, NoteName(root))
}

func (p *Pack) Starter(manifestPath string, root int) string {
	note := NoteName(root)
	return fmt.Sprintf("cicada 2\n%s\ntrack recorded %s { level = -6dB }\npattern taps { %s . %s^ . %s . %s^ . }\nscene main { recorded = taps }\nsong { main*4 }\n", p.Source(manifestPath, root), p.Manifest.ID, note, note, note, note)
}

func NoteName(midi int) string {
	return fmt.Sprintf("%s%d", []string{"c", "db", "d", "eb", "e", "f", "gb", "g", "ab", "a", "bb", "b"}[midi%12], midi/12-1)
}
