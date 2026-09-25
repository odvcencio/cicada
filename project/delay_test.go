package project

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/notation"
)

func TestDelaySendSourceProjectAndEngineRoundTrip(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "examples", "fx", "delay-send.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	score, diagnostics := notation.Parse(source)
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			t.Fatalf("source: %+v", diagnostic)
		}
	}
	p, diagnostics := FromScore(score)
	if p == nil {
		t.Fatalf("project: %+v", diagnostics)
	}
	if len(p.Effects) != 1 || p.Effects[0].ID != "delay" || p.Tracks[0].Mixer.SendA != .4 || p.Tracks[1].Mixer.SendA != 0 {
		t.Fatalf("delay route changed during lowering: %+v %+v", p.Effects, p.Tracks)
	}
	encoded, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	rewritten, err := ToSource(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(rewritten, []byte("time = 1/8T")) || !bytes.Contains(rewritten, []byte("send_a = 0.4")) {
		t.Fatalf("source round trip lost delay: %s", rewritten)
	}
	cfg, err := CompileEngine(decoded, 48_000, 128)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DelayA == nil || cfg.DelayA.Division != fx.EighthTriplet || cfg.Track[0].SendA != .4 {
		t.Fatalf("live delay route is wrong: %+v %+v", cfg.DelayA, cfg.Track[0])
	}
	if _, err := engine.New(cfg); err != nil {
		t.Fatal(err)
	}
}

func TestDelayTimeSourceSpellingsAndMissingReturn(t *testing.T) {
	base := "cicada 1\nfx delay { time = 1/8T }\ntrack bass acid { send_a = 0.4 }\npattern riff acid steps=1 { 1 }\nscene main { bass=riff }\nsong { main }\n"
	for _, spelling := range []string{"1/32", "1/16", "1/16T", "1/16.", "1/8", "1/8T", "1/8.", "3/16", "1/4", "1/4.", "1/2", "250ms"} {
		source := bytes.Replace([]byte(base), []byte("1/8T"), []byte(spelling), 1)
		score, diagnostics := notation.Parse(source)
		for _, diagnostic := range diagnostics {
			if diagnostic.Severity == "error" {
				t.Fatalf("time %s did not parse: %+v", spelling, diagnostic)
			}
		}
		if p, extra := FromScore(score); p == nil {
			t.Fatalf("time %s did not compile: %+v", spelling, extra)
		}
	}
	for _, invalid := range []string{"0.5", "1/64", "2500ms", "1/16t"} {
		source := bytes.Replace([]byte(base), []byte("1/8T"), []byte(invalid), 1)
		score, diagnostics := notation.Parse(source)
		if score != nil {
			if p, extra := FromScore(score); p != nil {
				t.Fatalf("accepted invalid time %s", invalid)
			} else {
				diagnostics = append(diagnostics, extra...)
			}
		}
		if len(diagnostics) == 0 {
			t.Fatalf("missing diagnostic for time %s", invalid)
		}
	}
	score, diagnostics := notation.Parse([]byte("cicada 1\ntrack bass acid { send_a = 0.4 }\npattern riff acid steps=1 { 1 }\nscene main { bass=riff }\nsong { main }\n"))
	if score == nil || len(diagnostics) == 0 || diagnostics[0].Code != "CICADA-REFERENCE" {
		t.Fatalf("missing delay reference diagnostic: %+v", diagnostics)
	}
}
