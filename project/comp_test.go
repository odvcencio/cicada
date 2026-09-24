package project

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
)

func TestCompressorBusProjectRoundTrip(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "examples", "fx", "compressor-bus.cicada"))
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
	if len(p.Effects) != 1 || p.Effects[0].ID != "comp" {
		t.Fatalf("compressor declaration was lost: %+v", p.Effects)
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
	if !bytes.Contains(rewritten, []byte("fx comp")) || !bytes.Contains(rewritten, []byte("sidechain = beat")) {
		t.Fatalf("source round trip lost compressor: %s", rewritten)
	}
	cfg, err := CompileEngine(decoded, 48_000, 128)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CompMusic == nil || cfg.CompSidechainTrack != 2 {
		t.Fatalf("sidechain track was not lowered: %+v", cfg)
	}
	if _, err := engine.New(cfg); err != nil {
		t.Fatal(err)
	}
	decoded.Effects[0].Params["sidechain"] = Value{Unit: "enum", Text: "missing"}
	if err := ValidateProject(decoded); err == nil {
		t.Fatal("accepted an unknown compressor sidechain track")
	}
	invalidSource := bytes.Replace(source, []byte("sidechain = beat"), []byte("sidechain = missing"), 1)
	_, diagnostics = notation.Parse(invalidSource)
	found := false
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == "CICADA-REFERENCE" && diagnostic.Position.Line > 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("unknown sidechain lacks a source-positioned reference diagnostic: %+v", diagnostics)
	}
	selfSource := bytes.Replace(source, []byte("  sidechain = beat\n"), nil, 1)
	selfScore, selfDiagnostics := notation.Parse(selfSource)
	for _, diagnostic := range selfDiagnostics {
		if diagnostic.Severity == "error" {
			t.Fatalf("self-detecting compressor source: %+v", diagnostic)
		}
	}
	selfProject, selfDiagnostics := FromScore(selfScore)
	if selfProject == nil {
		t.Fatalf("self-detecting compressor project: %+v", selfDiagnostics)
	}
	selfConfig, err := CompileEngine(selfProject, 48_000, 128)
	if err != nil || selfConfig.CompMusic == nil || selfConfig.CompSidechainTrack != 0 {
		t.Fatalf("compressor did not default to music-bus detection: %+v, %v", selfConfig, err)
	}
}
