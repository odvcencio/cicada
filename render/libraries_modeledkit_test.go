package render_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/render"
)

const houseKit = "kit band_kit {\n bd = model.kick\n rs = model.cross_stick\n ch = model.hat_closed\n oh = model.hat_open\n cy = model.ride_bow\n}\n"
const houseBeat = "pattern beat drums steps=16 {\n bd: x... .... x... ....\n rs: .... x... .... x...\n ch: x.x. x.x. x.x. x.x.\n}\n"

// loadHouseProject writes a project, pins its libraries and loads its score.
func loadHouseProject(t *testing.T, files map[string]string) *notation.Score {
	t.Helper()
	root := t.TempDir()
	for name, text := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	main := filepath.Join(root, "main.cicada")
	sources, err := project.ReadSources(main, nil)
	if err != nil {
		t.Fatal(err)
	}
	sum, _, err := sources.UpdateLibraries("")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "cicada.sum"), sum, 0o644); err != nil {
		t.Fatal(err)
	}
	score, diagnostics, err := project.LoadScore(main, nil)
	if err != nil || score == nil || len(diagnostics) != 0 {
		t.Fatalf("load: %v %+v", err, diagnostics)
	}
	return score
}

// audioPeak is the largest absolute sample in the data chunk of a 32-bit float
// WAV. render.WAV appends a metadata chunk after the audio; read as samples,
// its bytes give a huge value, so the peak must stop at the end of the data.
func audioPeak(t *testing.T, wav []byte) float64 {
	t.Helper()
	const header = 44 // RIFF, fmt and the data chunk header
	if len(wav) < header || string(wav[36:40]) != "data" {
		t.Fatalf("no data chunk in the first %d bytes of a %d-byte WAV", header, len(wav))
	}
	end := header + int(binary.LittleEndian.Uint32(wav[40:44]))
	if end > len(wav) {
		t.Fatalf("the data chunk ends at byte %d, past the %d-byte WAV", end, len(wav))
	}
	peak := 0.0
	for at := header; at+4 <= end; at += 4 {
		peak = max(peak, math.Abs(float64(math.Float32frombits(binary.LittleEndian.Uint32(wav[at:])))))
	}
	return peak
}

func TestModeledKitRendersIdenticallyFromImportsAndLibraries(t *testing.T) {
	t.Setenv("CICADA_LIBRARY", t.TempDir())
	const setup = "tempo 120\nseed 7\n"
	const manifest = "project house\ncicada 2\nentry \"main.cicada\"\n"
	use := func(kit, pattern string) string {
		return fmt.Sprintf("track drums %s { level = -12dB }\nscene main { drums = %s }\nsong { main }\n", kit, pattern)
	}
	wav := func(score *notation.Score) []byte {
		t.Helper()
		var audio bytes.Buffer
		if _, err := render.WAV(score, render.Options{SampleRate: 48_000, Bits: 32, Bars: 1}, &audio); err != nil {
			t.Fatal(err)
		}
		return audio.Bytes()
	}
	inline, diagnostics := notation.Parse([]byte("cicada 2\n" + setup + houseKit + houseBeat + use("band_kit", "beat")))
	if inline == nil || len(diagnostics) != 0 {
		t.Fatalf("inline kit: %+v", diagnostics)
	}
	// The silence check must be able to fail: the same kit with no hits is silent.
	quiet, diagnostics := notation.Parse([]byte("cicada 2\n" + setup + houseKit + strings.ReplaceAll(houseBeat, "x", ".") + use("band_kit", "beat")))
	if quiet == nil || len(diagnostics) != 0 {
		t.Fatalf("kit with no hits: %+v", diagnostics)
	}
	if peak := audioPeak(t, wav(quiet)); peak >= 1e-3 {
		t.Fatalf("a kit with no hits renders a peak of %g, so the silence check below could not fail", peak)
	}
	want := wav(inline)
	if peak := audioPeak(t, want); peak < 1e-3 {
		t.Fatalf("the modeled kit is silent: peak %g", peak)
	}
	for _, test := range []struct {
		name  string
		files map[string]string
	}{
		{"standard import", map[string]string{
			"cicada.mod":  manifest,
			"main.cicada": "import \"std/fx\"\n" + setup + houseKit + houseBeat + use("band_kit", "beat"),
		}},
		{"library kit", map[string]string{
			"cicada.mod":                manifest,
			"main.cicada":               "import \"demo/band\"\n" + setup + use("band.band_kit", "band.beat"),
			"lib/demo/band/cicada.mod":  "library demo/band\ncicada 2\nsource \"band.cicada\"\nlicense \"MIT\"\nauthor \"Cicada contributors\"\nengine 2\ncapabilities 0\n",
			"lib/demo/band/band.cicada": houseKit + houseBeat,
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := wav(loadHouseProject(t, test.files))
			if !bytes.Equal(got, want) {
				t.Fatalf("render differs from the same kit written without imports (%d and %d bytes)", len(got), len(want))
			}
		})
	}
}
