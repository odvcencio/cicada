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

func TestDriveSourceProjectAndEngineRoundTrip(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "examples", "fx", "drive-insert.cicada"))
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
	if len(p.Effects) != 1 || p.Effects[0].ID != "drive" || p.Tracks[0].Mixer.Insert != "drive" || p.Tracks[1].Mixer.Insert != "none" {
		t.Fatalf("drive routing did not survive lowering: %+v, %+v", p.Effects, p.Tracks)
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
	if !bytes.Contains(rewritten, []byte("fx drive {")) || !bytes.Contains(rewritten, []byte("insert = drive")) {
		t.Fatalf("source round trip lost drive: %s", rewritten)
	}
	cfg, err := CompileEngine(decoded, 48_000, 128)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Track[0].InsertDrive == nil || cfg.Track[0].InsertDrive.Shape != fx.Hard || cfg.Track[0].InsertDrive.GainDB != 18 || cfg.Track[1].InsertDrive != nil {
		t.Fatalf("engine drive routing is wrong: %+v %+v", cfg.Track[0].InsertDrive, cfg.Track[1].InsertDrive)
	}
	if _, err := engine.New(cfg); err != nil {
		t.Fatal(err)
	}
}

func TestDriveDeclarationRejectsInvalidValuesAndMissingInsert(t *testing.T) {
	base := "cicada 1\nfx drive { shape = hard gain = 18db tone = 12khz mix = 0.75 }\ntrack bass acid { insert = drive }\npattern riff acid steps=1 { 1 }\nscene main { bass=riff }\nsong { main }\n"
	for _, tc := range []struct{ from, to, code string }{
		{"gain = 18db", "gain = 37db", "CICADA-PARAM"},
		{"gain = 18db", "gain = 18hz", "CICADA-PARAM"},
		{"tone = 12khz", "tone = 999hz", "CICADA-PARAM"},
		{"mix = 0.75", "mix = 1.1", "CICADA-PARAM"},
		{"shape = hard", "shape = triangle", "CICADA-PARAM"},
		{"shape = hard", "shape = hard shape = fold", "CICADA-DUPLICATE"},
	} {
		source := bytes.Replace([]byte(base), []byte(tc.from), []byte(tc.to), 1)
		score, diagnostics := notation.Parse(source)
		if score != nil {
			if p, extra := FromScore(score); p != nil {
				t.Fatalf("accepted invalid drive %q", tc.to)
			} else {
				diagnostics = append(diagnostics, extra...)
			}
		}
		found := false
		for _, diagnostic := range diagnostics {
			if diagnostic.Code == tc.code && diagnostic.Position.Line > 0 && diagnostic.Position.Column > 0 {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing %s diagnostic for %q: %+v", tc.code, tc.to, diagnostics)
		}
	}
	score, diagnostics := notation.Parse([]byte("cicada 1\ntrack bass acid { insert = drive }\npattern riff acid steps=1 { 1 }\nscene main { bass=riff }\nsong { main }\n"))
	if score == nil || len(diagnostics) == 0 || diagnostics[0].Code != "CICADA-REFERENCE" {
		t.Fatalf("missing insert reference diagnostic: %+v", diagnostics)
	}
}
