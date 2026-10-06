package instrumentpack

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/kernel/voice/sample"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestFullKitCatalogCoverageAndPins(t *testing.T) {
	root := filepath.Join("..", "..", "assets", "sampler", "full-kit")
	data, err := os.ReadFile(filepath.Join(root, "catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Packs []struct {
			ID, Manifest, SHA256 string
			Assets, Zones        int
			PCMBytes             int64 `json:"pcm_bytes"`
		}
		Articulations []struct {
			Name, Bank, Kind string
			Note, Layers     int
			RoundRobins      int `json:"round_robins"`
		}
	}
	if err := json.Unmarshal(data, &catalog); err != nil {
		t.Fatal(err)
	}
	required := map[string]bool{}
	for _, name := range []string{"kick", "snare_center", "snare_rimshot", "snare_cross_stick", "snare_ghost", "tom_high", "tom_mid", "tom_floor", "tom_low_floor", "hat_closed", "hat_pedal", "hat_half_open", "hat_open", "ride_bow", "ride_bell", "ride_crash", "crash_left", "crash_right", "splash", "china", "splash_choke", "ride_choke"} {
		required[name] = false
	}
	manifests := map[string]Manifest{}
	for _, pin := range catalog.Packs {
		data, err := os.ReadFile(filepath.Join(root, pin.Manifest))
		if err != nil {
			t.Fatal(err)
		}
		if fmt.Sprintf("%x", sha256.Sum256(data)) != pin.SHA256 {
			t.Fatal("manifest pin mismatch", pin.ID)
		}
		m, err := DecodeManifest(data)
		if err != nil {
			t.Fatal(err)
		}
		if len(m.Assets) != pin.Assets || len(m.Zones) != pin.Zones {
			t.Fatal("catalog count mismatch")
		}
		var pcm int64
		for _, a := range m.Assets {
			pcm += int64(a.Frames) * int64(a.Channels) * 4
		}
		if pcm != pin.PCMBytes || pcm > MaxPCMBytes {
			t.Fatal("PCM size mismatch", pin.ID)
		}
		// Admit the real map with a small PCM fixture, so missing cycles and
		// ambiguous key maps fail CI without downloading hundreds of megabytes.
		fixture := make([]float32, 1024)
		for i := range fixture {
			fixture[i] = .1
		}
		zones := make([]sample.Zone, len(m.Zones))
		for i, z := range m.Zones {
			if !z.OneShot || z.Loop || z.Release {
				t.Fatal("drum map must retain natural one-shot decay")
			}
			zones[i] = sample.Zone{Region: sample.Region{Left: fixture, SampleRate: 48000, RootKey: uint8(z.Root), End: len(fixture)}, KeyLow: uint8(z.KeyLow), KeyHigh: uint8(z.KeyHigh), VelocityLow: uint8(z.VelocityLow), VelocityHigh: uint8(z.VelocityHigh), Layer: uint8(z.Layer), Group: uint8(z.Group), Position: uint8(z.Position), Count: uint8(z.Count), Gain: z.Gain, OneShot: z.OneShot, ChokeGroup: uint8(z.ChokeGroup), ChokeSustain: z.ChokeSustain}
		}
		p, err := sample.NewInstrument(48000, zones, m.Config)
		if err != nil {
			t.Fatal(err)
		}
		for _, art := range catalog.Articulations {
			if art.Bank != pin.ID {
				continue
			}
			counts := map[int]int{}
			for _, z := range m.Zones {
				if z.KeyLow <= art.Note && z.KeyHigh >= art.Note {
					counts[z.Layer]++
					if art.Kind != "silent choke control" && z.Count != art.RoundRobins {
						t.Fatal("take count differs", art.Name)
					}
				}
			}
			if art.Kind != "silent choke control" && len(counts) != art.Layers {
				t.Fatal("dynamic count differs", art.Name)
			}
			for velocity := 0; velocity <= 127; velocity++ {
				p.Reset()
				if _, err := p.NoteOn(uint8(art.Note), uint8(velocity)); err != nil {
					t.Fatal(art.Name, velocity, err)
				}
			}
		}
		manifests[pin.ID] = m
	}
	notes := map[int]bool{}
	for _, art := range catalog.Articulations {
		if notes[art.Note] {
			t.Fatal("duplicate articulation note", art.Note)
		}
		notes[art.Note] = true
		if _, ok := manifests[art.Bank]; !ok {
			t.Fatal("unknown articulation bank")
		}
		if _, ok := required[art.Name]; ok {
			required[art.Name] = true
		}
	}
	for name, found := range required {
		if !found {
			t.Fatal("missing articulation", name)
		}
	}
	for _, note := range []int{35, 36, 37, 38, 40, 41, 42, 43, 44, 45, 46, 47, 48, 49, 50, 51, 52, 53, 55, 57, 59} {
		if !notes[note] {
			t.Fatal("missing GM kit note", note)
		}
	}
	demos, err := filepath.Glob(filepath.Join(root, "demos", "*.cicada"))
	if err != nil || len(demos) != 5 {
		t.Fatal("five groove scores required", err)
	}
	for _, demo := range demos {
		data, err := os.ReadFile(demo)
		if err != nil {
			t.Fatal(err)
		}
		score, diagnostics := notation.Parse(data)
		for _, d := range diagnostics {
			if d.Severity == "error" {
				t.Fatal(demo, d.Message)
			}
		}
		_, diagnostics = project.FromScore(score)
		for _, d := range diagnostics {
			if d.Severity == "error" {
				t.Fatal(demo, d.Message)
			}
		}
		for _, declaration := range score.Samplers {
			found := false
			for _, pin := range catalog.Packs {
				if declaration.Pack == "packs/"+pin.Manifest && declaration.SHA256 == pin.SHA256 {
					found = true
				}
			}
			if !found {
				t.Fatal("demo references a stale or unknown pack", demo)
			}
		}
	}
}
