package project

import (
	"bytes"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/notation"
)

const tapeScore = `cicada 2
tempo 74
fx echo tape { time = 3/16 feedback = 0.6 wow = 0.5 flutter = 0.2 saturation = 0.4 damp = 3200Hz }
track rim drums { send echo = 0.8 pre }
pattern hit drums { rs: x... .... .... .... }
automate echo.time { @1.1.1 608ms @3.1.1 300ms smooth }
scene main { rim = hit }
song { main*4 }
`

func tapeProject(t *testing.T, source string) (*Project, []notation.Diagnostic) {
	t.Helper()
	score, ds := notation.Parse([]byte(source))
	if len(ds) != 0 {
		return nil, ds
	}
	return FromScore(score)
}

func TestTapeEchoCompilesAsDelayReturn(t *testing.T) {
	p, ds := tapeProject(t, tapeScore)
	if p == nil {
		t.Fatal(ds)
	}
	cfg, err := CompileEngine(p, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DelayA != nil || cfg.TapeA == nil {
		t.Fatalf("delay %v tape %v", cfg.DelayA, cfg.TapeA)
	}
	if cfg.TapeA.Division != fx.ThreeSixteenths || cfg.TapeA.Feedback != .6 || cfg.TapeA.DampHz != 3200 || cfg.TapeA.Saturation != .4 {
		t.Fatalf("params %+v", cfg.TapeA)
	}
	if cfg.Track[0].SendA != .8 || !cfg.Track[0].SendAPre {
		t.Fatalf("send %+v", cfg.Track[0])
	}
	if len(cfg.Automation) == 0 {
		t.Fatal("echo.time automation was not compiled")
	}
	if _, err := kernelimage.Encode(cfg); err == nil || !strings.Contains(err.Error(), "tape echo") {
		t.Fatalf("portable image must refuse tape, got %v", err)
	}
}

func TestTapeEchoSourceRoundTrip(t *testing.T) {
	p, ds := tapeProject(t, tapeScore)
	if p == nil {
		t.Fatal(ds)
	}
	encoded, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	p, err = DecodeJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	source, err := ToSource(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(source, []byte("tape")) {
		t.Fatalf("tape lost: %s", source)
	}
	if p, ds := tapeProject(t, string(source)); p == nil {
		t.Fatal(ds)
	}
}

func TestTapeEchoRefusals(t *testing.T) {
	for name, source := range map[string]string{
		"second return": strings.Replace(tapeScore, "track rim", "fx d delay { time = 1/8 }\ntrack rim", 1),
		"feedback":      strings.Replace(tapeScore, "feedback = 0.6", "feedback = 0.99", 1),
		"unknown":       strings.Replace(tapeScore, "wow = 0.5", "width = 1", 1),
		"as insert":     strings.Replace(tapeScore, "send echo = 0.8 pre", "insert = echo", 1),
		"slow time":     strings.Replace(tapeScore, "time = 3/16", "time = 2500ms", 1),
	} {
		if p, _ := tapeProject(t, source); p != nil {
			if _, err := CompileEngine(p, 48000, 128); err == nil {
				t.Errorf("%s: accepted", name)
			}
		}
	}
}
