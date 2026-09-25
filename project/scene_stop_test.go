package project

import (
	"bytes"
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
)

func TestSceneStopMatchesLegacyOffAndOmissionKeeps(t *testing.T) {
	base := "track bass acid {}\npattern riff { 1 . 3 . }\nscene main { bass = riff }\nscene outro { bass = ACTION }\nsong { main outro }\n"
	parse := func(source string) (*Project, engine.Config) {
		t.Helper()
		score, diagnostics := notation.Parse([]byte(source))
		if len(diagnostics) != 0 {
			t.Fatalf("source diagnostics: %+v", diagnostics)
		}
		p, diagnostics := FromScore(score)
		if p == nil || len(diagnostics) != 0 {
			t.Fatalf("project diagnostics: %+v", diagnostics)
		}
		cfg, err := CompileEngine(p, 48_000, 128)
		if err != nil {
			t.Fatal(err)
		}
		return p, cfg
	}
	stopped, stopConfig := parse(strings.Replace(base, "ACTION", "stop", 1))
	legacy, _ := parse(strings.Replace(base, "ACTION", "off", 1))
	if !SemanticEqual(stopped, legacy) || stopConfig.Scenes[1].Track[0].Mode != engine.SceneOff {
		t.Fatal("stop changed the legacy off action")
	}
	generated, err := ToSource(stopped)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(generated, []byte("bass = stop")) || bytes.Contains(generated, []byte("bass = off")) {
		t.Fatalf("project source did not print stop: %s", generated)
	}
	omitted := strings.Replace(base, "scene outro { bass = ACTION }", "scene outro {}", 1)
	_, omittedConfig := parse(omitted)
	_, keepConfig := parse(strings.Replace(base, "ACTION", "keep", 1))
	if omittedConfig.Scenes[1].Track[0].Mode != engine.SceneKeep || keepConfig.Scenes[1].Track[0].Mode != engine.SceneKeep {
		t.Fatal("omitting a scene binding did not keep the track playing")
	}
}

func TestLegacyPatternNamedStopKeepsItsMeaning(t *testing.T) {
	source := []byte("track bass acid {}\npattern stop { 1 . }\nscene main { bass = stop }\nscene quiet { bass = off }\nsong { main quiet }\n")
	score, diagnostics := notation.Parse(source)
	if len(diagnostics) != 0 {
		t.Fatalf("legacy score diagnostics: %+v", diagnostics)
	}
	p, diagnostics := FromScore(score)
	if p == nil || len(diagnostics) != 0 {
		t.Fatalf("legacy project diagnostics: %+v", diagnostics)
	}
	cfg, err := CompileEngine(p, 48_000, 128)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Scenes[0].Track[0].Mode != engine.SceneSlot || cfg.Scenes[1].Track[0].Mode != engine.SceneOff {
		t.Fatal("legacy pattern stop or off action changed meaning")
	}
	generated, err := ToSource(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(generated, []byte("bass = off")) {
		t.Fatalf("generated source lost unambiguous legacy off action: %s", generated)
	}
}
