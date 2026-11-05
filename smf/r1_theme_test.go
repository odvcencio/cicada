package smf

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/project"
)

func TestR1ThemeModulationPreservesOriginalMIDI(t *testing.T) {
	root, err := filepath.Abs("../examples/detail/r1-theme-sway")
	if err != nil {
		t.Fatal(err)
	}
	mainPath, bandPath := filepath.Join(root, "main.cicada"), filepath.Join(root, "band.cicada")
	main, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	band, err := os.ReadFile(bandPath)
	if err != nil {
		t.Fatal(err)
	}
	const motion = `// R1 adds motion without changing the theme's notes.
lfo drift { wave = triangle rate = 8bars }
sway keys_wander { rate = 2bars octaves = 2 }
sway brass_wander { rate = 4bars octaves = 2 }
contour pad_swell { period = 4bars @0 -1 @1/4 1 smooth @1 -1 }

mod drift -> strings_pad.pan depth = 0.2
mod keys_wander -> keys.level depth = 1.5dB
mod pad_swell -> strings_pad.level depth = 2dB

`
	const modified = "cutoff * (0.5 + amp) * (1 + 0.2 * mod(brass_wander))"
	const original = "cutoff * (0.5 + amp)"
	if strings.Count(string(main), motion) != 1 || strings.Count(string(band), modified) != 1 {
		t.Fatal("R1 modulation fixture changed; update the reference comparison")
	}
	baseline := map[string][]byte{
		mainPath: []byte(strings.Replace(string(main), motion, "", 1)),
		bandPath: []byte(strings.Replace(string(band), modified, original, 1)),
	}
	encode := func(overrides map[string][]byte) []byte {
		t.Helper()
		score, ds, err := project.LoadScore(mainPath, overrides)
		if err != nil || len(ds) != 0 {
			t.Fatalf("load: %v; diagnostics: %+v", err, ds)
		}
		p, ds := project.FromScore(score)
		if p == nil || len(ds) != 0 {
			t.Fatalf("project: %+v", ds)
		}
		file, err := FromProject(p, 0)
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := Encode(file, &out); err != nil {
			t.Fatal(err)
		}
		return out.Bytes()
	}
	before, after := encode(baseline), encode(nil)
	if !bytes.Equal(before, after) {
		t.Fatal("R1 modulation changed the theme MIDI")
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(before)); got != "665cf9907a0f4678e151624c3769ec569737ef6842013b6c925778576dd8738f" {
		t.Fatalf("theme baseline changed: %s", got)
	}
}
