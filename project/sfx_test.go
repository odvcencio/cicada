package project

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
)

func TestSFXBusSourceProjectAndEngine(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "examples", "sfx-bus.cicada"))
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
	if p.Tracks[0].Mixer.Bus != "music" || p.Tracks[1].Mixer.Bus != "sfx" {
		t.Fatalf("bus routing changed during lowering: %+v", p.Tracks)
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
	if err != nil || !bytes.Contains(rewritten, []byte("bus = sfx")) {
		t.Fatalf("source round trip lost SFX bus: %s, %v", rewritten, err)
	}
	cfg, err := CompileEngine(decoded, 48_000, 128)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CompSidechainTrack != engine.SFXSidechain || !cfg.Track[1].BusSFX || cfg.Track[0].BusSFX {
		t.Fatalf("live SFX route is wrong: %+v", cfg)
	}
	if _, err := engine.New(cfg); err != nil {
		t.Fatal(err)
	}
}
