package render

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/instrumentpack"
	"m31labs.dev/cicada/host/schedule"
	"m31labs.dev/cicada/internal/testpack"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// A pack that decodes to 72,000,000 bytes, over the 64 MiB resident budget.
// It renders natively, the native cap can refuse it, and the browser image path
// still refuses it.
func TestRenderPackAboveTheResidentBudget(t *testing.T) {
	dir := t.TempDir()
	pack := testpack.Write(t, dir, testpack.Options{ID: "big", Assets: 3, Frames: 6_000_000, Zones: []testpack.Zone{{KeyLow: 48, KeyHigh: 59}, {KeyLow: 60, KeyHigh: 71}, {KeyLow: 72, KeyHigh: 83}}})
	if pack.PCMBytes != 72_000_000 || pack.PCMBytes <= 64<<20 {
		t.Fatalf("fixture holds %d bytes, want 72000000 (above 64 MiB)", pack.PCMBytes)
	}
	source := "cicada 2\ntempo 120\n" + pack.Sampler("sound", 2) + "track lead sound {}\npattern melody notes steps=4 { c4 . e4 . }\nscene main { lead = melody }\nsong { main }\n"
	score, ds := notation.Parse([]byte(source))
	for _, d := range ds {
		if d.Severity == "error" {
			t.Fatal(ds)
		}
	}
	options := Options{SampleRate: 48000, Bits: 24, TailSec: .2, Block: 256, AssetDir: dir, AssetRoot: dir}

	t.Run("renders", func(t *testing.T) {
		var wav bytes.Buffer
		report, err := WAV(score, options, &wav)
		if err != nil {
			t.Fatalf("render refused a pack of %d bytes: %v", pack.PCMBytes, err)
		}
		if report.Peak == 0 {
			t.Fatal("the render of the pack is silent")
		}
	})
	t.Run("cap names the limit and the size", func(t *testing.T) {
		capped := options
		capped.MaxResidentPCM = 48 << 20
		_, err := WAV(score, capped, &bytes.Buffer{})
		var over *instrumentpack.ResidentPCMError
		if !errors.As(err, &over) || over.Limit != 48<<20 || over.Bytes != pack.PCMBytes {
			t.Fatalf("want a *ResidentPCMError for %d of %d bytes: %#v", pack.PCMBytes, 48<<20, err)
		}
		for _, want := range []string{"--max-resident-pcm", "50331648 bytes", "72000000 bytes"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("refusal lacks %q: %v", want, err)
			}
		}
	})
	t.Run("baseline render obeys the same cap", func(t *testing.T) {
		baseline := options
		baseline.SamplerBaseline = true
		if _, err := WAV(score, baseline, &bytes.Buffer{}); err != nil {
			t.Fatalf("baseline render refused a pack of %d bytes: %v", pack.PCMBytes, err)
		}
		baseline.MaxResidentPCM = 48 << 20
		if _, err := WAV(score, baseline, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "--max-resident-pcm") {
			t.Fatalf("baseline render ignored the cap: %v", err)
		}
	})
	t.Run("browser image path refuses it", func(t *testing.T) {
		p, more := project.FromScore(score)
		if p == nil {
			t.Fatal(more)
		}
		if _, err := schedule.CompileResident(p, dir, 48000, 128); err == nil || !strings.Contains(err.Error(), "64 MiB") {
			t.Fatalf("CompileResident must keep the 64 MiB rule: %v", err)
		}
	})
}

// The cap is in bytes of decoded PCM, whatever the channel count.
func TestRenderCapCountsStereoPCM(t *testing.T) {
	dir := t.TempDir()
	pack := testpack.Write(t, dir, testpack.Options{Frames: 10_000, Channels: 2})
	if pack.PCMBytes != 80_000 {
		t.Fatalf("fixture holds %d bytes, want 80000", pack.PCMBytes)
	}
	source := fmt.Sprintf("cicada 2\ntempo 120\n%strack lead sound {}\npattern melody notes steps=4 { c4 . . . }\nscene main { lead = melody }\nsong { main }\n", pack.Sampler("sound", 2))
	score, _ := notation.Parse([]byte(source))
	options := Options{SampleRate: 48000, Bits: 24, TailSec: .1, AssetDir: dir, AssetRoot: dir}
	options.MaxResidentPCM = 80_000
	if _, err := WAV(score, options, &bytes.Buffer{}); err != nil {
		t.Fatalf("a cap equal to the pack's PCM refused it: %v", err)
	}
	options.MaxResidentPCM = 79_999
	if _, err := WAV(score, options, &bytes.Buffer{}); err == nil {
		t.Fatal("a cap a byte below the pack's PCM admitted it")
	}
}
