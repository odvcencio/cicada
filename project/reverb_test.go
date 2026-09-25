package project

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
)

func TestReverbSendProjectRoundTrip(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "examples", "fx-bus.cicada"))
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
	if len(p.Effects) != 3 || p.Tracks[0].Mixer.SendA != .3 || p.Tracks[0].Mixer.SendB != .35 || p.Tracks[1].Mixer.SendB != .2 {
		t.Fatalf("effect routes changed during lowering: %+v %+v", p.Effects, p.Tracks)
	}
	jsonData, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeJSON(jsonData)
	if err != nil {
		t.Fatal(err)
	}
	rewritten, err := ToSource(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(rewritten, []byte("fx reverb")) || !bytes.Contains(rewritten, []byte("send_b = 0.35")) {
		t.Fatalf("source round trip lost reverb: %s", rewritten)
	}
	cfg, err := CompileEngine(decoded, 48_000, 128)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ReverbB == nil || cfg.DelayA == nil || cfg.Track[0].SendB != .35 {
		t.Fatalf("live effect routes are wrong: %+v %+v", cfg.ReverbB, cfg.Track[0])
	}
	if _, err := engine.New(cfg); err != nil {
		t.Fatal(err)
	}
}

func TestReverbSendRequiresReturn(t *testing.T) {
	source := "cicada 1\ntrack bass acid { send_b = 0.4 }\npattern riff acid steps=1 { 1 }\nscene main { bass=riff }\nsong { main }\n"
	score, diagnostics := notation.Parse([]byte(source))
	if score == nil || len(diagnostics) == 0 || diagnostics[0].Code != "CICADA-REFERENCE" {
		t.Fatalf("missing reverb reference diagnostic: %+v", diagnostics)
	}
}
