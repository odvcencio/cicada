package render

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/instrumentpack"
	"m31labs.dev/cicada/host/schedule"
	"m31labs.dev/cicada/internal/testpack"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// parsePackScore parses a score that plays the pack through one sampler per
// voice count, each on its own track.
func parsePackScore(t *testing.T, pack testpack.Pack, voices ...int) *notation.Score {
	t.Helper()
	var source strings.Builder
	source.WriteString("cicada 2\ntempo 120\n")
	for i, n := range voices {
		source.WriteString(pack.Sampler(fmt.Sprintf("s%d", i), n))
		fmt.Fprintf(&source, "track t%d s%d {}\n", i, i)
	}
	source.WriteString("pattern hit notes steps=4 { c4 . . . }\nscene main {")
	for i := range voices {
		fmt.Fprintf(&source, " t%d = hit", i)
	}
	source.WriteString(" }\nsong { main }\n")
	score, ds := notation.Parse([]byte(source.String()))
	for _, d := range ds {
		if d.Severity == "error" {
			t.Fatal(ds)
		}
	}
	return score
}

// The direct renderer and the scheduled renderer read the same pack. One
// process decodes it once for both, and each sampler keeps its own settings.
func TestRenderSharesAPackWithTheScheduleCompileAndKeepsEachSamplersSettings(t *testing.T) {
	dir := t.TempDir()
	pack := testpack.Write(t, dir, testpack.Options{ID: "shared", Frames: 4000})
	score := parsePackScore(t, pack, 2, 5)
	t.Cleanup(instrumentpack.ReleaseShared)

	p, more := project.FromScore(score)
	if p == nil {
		t.Fatal(more)
	}
	cfg, err := schedule.Compile(p, dir, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	compiled := cfg.Track[0].Prepared.(*engine.PackFactory)

	banks, err := preparePackVoices(score, Options{SampleRate: 48000, AssetDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(banks) != 2 {
		t.Fatalf("%d banks, want 2", len(banks))
	}
	for name, bank := range banks {
		if &bank.Zones[0].Region.Left[0] != &compiled.Zones[0].Region.Left[0] {
			t.Errorf("sampler %s holds its own copy of the PCM", name)
		}
	}
	if banks["s0"].Manifest.Config.Voices != 2 || banks["s1"].Manifest.Config.Voices != 5 {
		t.Errorf("voice counts %d and %d, want 2 and 5: samplers on one pack share each other's settings",
			banks["s0"].Manifest.Config.Voices, banks["s1"].Manifest.Config.Voices)
	}
}

// A render whose packs are over the cap is refused from the manifests, before
// any audio is opened, on both render paths.
func TestRenderRefusesPacksOverTheCapBeforeReadingTheirAudio(t *testing.T) {
	dir := t.TempDir()
	pack := testpack.Write(t, dir, testpack.Options{ID: "unread", Frames: 1000}) // 4000 bytes
	score := parsePackScore(t, pack, 2)
	t.Cleanup(instrumentpack.ReleaseShared)
	audio := filepath.Join(dir, filepath.Dir(pack.Manifest), "samples", "take0.wav.gz")
	if err := os.Remove(audio); err != nil {
		t.Fatal(err)
	}
	for _, path := range []struct {
		name     string
		baseline bool
	}{{"scheduled render", false}, {"sampler baseline", true}} {
		t.Run(path.name, func(t *testing.T) {
			options := Options{SampleRate: 48000, Bits: 24, TailSec: .1, AssetDir: dir, AssetRoot: dir, SamplerBaseline: path.baseline}
			options.MaxResidentPCM = 3999
			_, err := WAV(score, options, &bytes.Buffer{})
			var over *instrumentpack.ResidentPCMError
			if !errors.As(err, &over) || over.Bytes != 4000 || over.Limit != 3999 {
				t.Fatalf("a pack over the cap must be refused by its manifest: %v", err)
			}
			options.MaxResidentPCM = 4000
			if _, err = WAV(score, options, &bytes.Buffer{}); err == nil || errors.As(err, &over) || !strings.Contains(err.Error(), "take0") {
				t.Fatalf("a pack that fits the cap must reach its audio: %v", err)
			}
		})
	}
}

// Two samplers that name one pack share one decoded copy, so the cap counts it once.
func TestRenderCapCountsAPackOnceWhenSeveralSamplersNameIt(t *testing.T) {
	dir := t.TempDir()
	pack := testpack.Write(t, dir, testpack.Options{ID: "twice", Frames: 1000}) // 4000 bytes
	score := parsePackScore(t, pack, 2, 2)
	t.Cleanup(instrumentpack.ReleaseShared)
	options := Options{SampleRate: 48000, Bits: 24, TailSec: .1, AssetDir: dir, AssetRoot: dir, MaxResidentPCM: 4000}
	if _, err := WAV(score, options, &bytes.Buffer{}); err != nil {
		t.Fatalf("a cap equal to the pack refused a score whose two samplers share it: %v", err)
	}
	options.MaxResidentPCM = 3999
	if _, err := WAV(score, options, &bytes.Buffer{}); err == nil {
		t.Fatal("a cap a byte below the pack admitted it")
	}
}
