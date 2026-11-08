package notation

import (
	"bytes"
	"strings"
	"testing"
)

const samplerKitSource = `cicada 2
tempo 120
sampler shells { pack = "packs/shells/manifest.json" sha256 = "0000000000000000000000000000000000000000000000000000000000000000" root = c4 voices = 10 }
kit snare_kit {
  sd = shells.d2
  rs = shells.c#2
  cp = shells.eb2
  bd = builtin.bd
}
track snare snare_kit { level = -6dB sd_level = -3dB rs_pan = -0.5 }
pattern drop drums steps = 8 humanize = 6 { rs: .... x8... sd: .... ..x2. }
scene main { snare = drop }
song { main }
`

func TestKitBindsSamplerPitchesWithSharpsAndFlats(t *testing.T) {
	score, diagnostics := Parse([]byte(samplerKitSource))
	if score == nil || len(score.Kits) != 1 {
		t.Fatalf("kit not parsed: %+v", diagnostics)
	}
	want := map[string]string{"sd": "shells.d2", "rs": "shells.c#2", "cp": "shells.eb2", "bd": "builtin.bd"}
	for _, binding := range score.Kits[0].Bindings {
		if want[binding.Lane] != binding.Target {
			t.Errorf("lane %s binds %q, want %q", binding.Lane, binding.Target, want[binding.Lane])
		}
	}
	for _, d := range diagnostics {
		if d.Severity == "error" {
			t.Errorf("unexpected diagnostic: %+v", d)
		}
	}
}

func TestSamplerKitFormatsStably(t *testing.T) {
	document, err := ParseDocument([]byte(samplerKitSource))
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := Format(document)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(formatted), "shells.c#2") {
		t.Fatalf("formatter changed the sharp pitch:\n%s", formatted)
	}
	again, err := ParseDocument(formatted)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Format(again)
	if err != nil || !bytes.Equal(formatted, second) {
		t.Fatalf("formatting is not stable: %v", err)
	}
}

func TestSamplerKitRejectsBadBindings(t *testing.T) {
	for _, test := range []struct{ name, old, new, code, text string }{
		{"unknown sampler", "sd = shells.d2", "sd = drumz.d2", "CICADA-REFERENCE", "no sampler named drumz"},
		{"unknown sampler with sharp", "rs = shells.c#2", "rs = drumz.c#2", "CICADA-REFERENCE", "no sampler named drumz"},
		{"bad pitch", "sd = shells.d2", "sd = shells.h2", "CICADA-PARAM", "not a note name"},
		{"pitch out of range", "sd = shells.d2", "sd = shells.g#9", "CICADA-PARAM", "not a note name"},
		{"unsupported lane control", "sd_level = -3dB", "sd_tune = 1.1", "CICADA-PARAM", "unknown parameter sd_tune"},
		{"humanize too large", "humanize = 6", "humanize = 99", "CICADA-PARAM", "humanize must be"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, diagnostics := Parse([]byte(strings.Replace(samplerKitSource, test.old, test.new, 1)))
			for _, d := range diagnostics {
				if d.Code == test.code && strings.Contains(d.Message, test.text) {
					return
				}
			}
			t.Fatalf("want %s containing %q, got %+v", test.code, test.text, diagnostics)
		})
	}
}

func TestSamplerKitRefusesAnAssetSampler(t *testing.T) {
	source := strings.Replace(samplerKitSource, `pack = "packs/shells/manifest.json" sha256 = "0000000000000000000000000000000000000000000000000000000000000000"`, `asset = wave`, 1)
	source = strings.Replace(source, "sampler shells", `asset wave "wave.wav" { sha256 = "0000000000000000000000000000000000000000000000000000000000000000" format = wav frames = 100 rate = 48000Hz channels = 1 }`+"\nsampler shells", 1)
	_, diagnostics := Parse([]byte(source))
	for _, d := range diagnostics {
		if d.Code == "CICADA-UNSUPPORTED" && strings.Contains(d.Message, "sample packs") {
			return
		}
	}
	t.Fatalf("want an unsupported diagnostic, got %+v", diagnostics)
}

// Only a sharp needs the sampler pitch token, so qualified names that start
// like a flat pitch (bb808, eb2kick) still parse as names, as they did before.
func TestKitTargetsThatStartLikeAFlatPitchStillParse(t *testing.T) {
	source := []byte("cicada 2\ntempo 120\nkit k {\n  sd = tone.bb808\n  rs = tone.ab1_snare\n  cp = tone.eb2kick\n}\n")
	score, diagnostics := Parse(source)
	for _, d := range diagnostics {
		if d.Code == "CICADA-SYNTAX" {
			t.Fatalf("syntax error: %+v", d)
		}
	}
	if score == nil || len(score.Kits) != 1 {
		t.Fatalf("kit not parsed: %+v", diagnostics)
	}
	want := map[string]string{"sd": "tone.bb808", "rs": "tone.ab1_snare", "cp": "tone.eb2kick"}
	for _, binding := range score.Kits[0].Bindings {
		if want[binding.Lane] != binding.Target {
			t.Errorf("lane %s binds %q, want %q", binding.Lane, binding.Target, want[binding.Lane])
		}
	}
}
