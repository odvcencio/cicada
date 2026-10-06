package audioassets

import (
	"crypto/sha256"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/internal/testwav"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

var output = flag.String("out", "", "directory for a generated WAV and edition-2 example")

func TestGenerateExample(t *testing.T) {
	dir := *output
	if dir == "" {
		dir = t.TempDir()
	}
	if err := os.MkdirAll(filepath.Join(dir, "audio"), 0755); err != nil {
		t.Fatal(err)
	}
	data := testwav.Bytes(48000, 1, 16, 4800, 1)
	source := fmt.Sprintf(`title "Voice and chops"
tempo 120
asset vocal "audio/example.wav" {
  sha256 = "%x"
  format = wav
  frames = 4800
  rate = 48000Hz
  channels = 1
  source = generated
}
clip vocal-a vocal { start = 10ms end = 4800frames gain = -2dB fade_in = 1ms fade_out = 2ms }
sampler vocal-hit { asset = vocal root = c3 mode = oneshot voices = 8 }
track chops vocal-hit { level = -8dB }
track vox audio { level = -6dB }
pattern hits { c3 . c3 . }
scene verse { chops = hits vox = vocal-a }
song { verse*4 }
`, sha256.Sum256(data))
	for path, content := range map[string][]byte{"audio/example.wav": data, "cicada.mod": []byte("project audio-example\ncicada 2\n"), "main.cicada": []byte(source)} {
		if err := os.WriteFile(filepath.Join(dir, path), content, 0644); err != nil {
			t.Fatal(err)
		}
	}
	score, ds := notation.ParseEdition([]byte(source), 2)
	for _, d := range ds {
		if d.Severity == "error" {
			t.Fatal(d)
		}
	}
	for _, d := range project.VerifyAssets(score, dir) {
		if d.Severity == "error" {
			t.Fatal(d)
		}
	}
	if p, ds := project.FromScore(score); p == nil {
		t.Fatal(ds)
	}
	t.Logf("generated %d-byte WAV sha256=%x", len(data), sha256.Sum256(data))
}
