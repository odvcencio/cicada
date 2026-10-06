package project

import (
	"bytes"
	"fmt"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/voice/modal"
	"m31labs.dev/cicada/notation"
	"os"
	"strings"
	"testing"
)

func TestModeledScoresRoundTripAndVoiceBudget(t *testing.T) {
	for _, name := range modal.Names {
		t.Run(name, func(t *testing.T) {
			source, err := os.ReadFile("../examples/modeled/" + name + ".cicada")
			if err != nil {
				t.Fatal(err)
			}
			score, ds := notation.Parse(source)
			for _, d := range ds {
				if d.Severity == "error" {
					t.Fatal(d)
				}
			}
			p, ds := FromScore(score)
			if p == nil {
				t.Fatalf("project: %+v", ds)
			}
			cfg, err := CompileEngine(p, 48000, 128)
			if err != nil {
				t.Fatal(err)
			}
			profile, _ := modal.ParseProfile(name)
			if cfg.Track[0].Kind != engine.VoiceModal || cfg.Track[0].Modal != profile {
				t.Fatal("wrong modal profile")
			}
			encoded, err := CanonicalJSON(p)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := DecodeJSON(encoded)
			if err != nil {
				t.Fatal(err)
			}
			again, err := CanonicalJSON(decoded)
			if err != nil || !bytes.Equal(encoded, again) {
				t.Fatal("JSON changed")
			}
			text, err := ToSource(decoded)
			if err != nil {
				t.Fatal(err)
			}
			round, ds := notation.Parse(text)
			if round == nil {
				t.Fatal(ds)
			}
			p2, ds := FromScore(round)
			if p2 == nil {
				t.Fatal(ds)
			}
			if _, err := CompileEngine(p2, 48000, 128); err != nil {
				t.Fatal(err)
			}
			cfg.MaxVoices = modal.MaxVoices - 1
			if _, err := engine.New(cfg); err == nil {
				t.Fatal("unaccounted strike tails")
			}
		})
	}
}

func TestModeledParametersAndAllocatedVoiceBudget(t *testing.T) {
	for _, param := range []string{"octave = 7", "octave = 1Hz", "cutoff = 200Hz"} {
		score, ds := notation.Parse([]byte("cicada 2\ntrack sound model_steelpan {" + param + "}\npattern p { c4 }\nscene s { sound=p }\nsong {s}\n"))
		_, checked := Check(score)
		ds = append(ds, checked...)
		found := false
		for _, d := range ds {
			found = found || d.Severity == "error"
		}
		if !found {
			t.Errorf("accepted %s", param)
		}
	}
	var source strings.Builder
	source.WriteString("cicada 2\npattern p {c4}\n")
	for i := 0; i < 9; i++ {
		fmt.Fprintf(&source, "track t%d model_steelpan {}\n", i)
	}
	source.WriteString("scene s {\n")
	for i := 0; i < 9; i++ {
		fmt.Fprintf(&source, "t%d=p\n", i)
	}
	source.WriteString("}\nsong {s}\n")
	score, _ := notation.Parse([]byte(source.String()))
	if p, _ := FromScore(score); p != nil {
		t.Fatal("nine four-strike tracks passed 32-voice ceiling")
	}
}
