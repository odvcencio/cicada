package kernelimage_test

import (
	"encoding/binary"
	"os"
	"reflect"
	"testing"

	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestGuitarImageVersionAndLegacyEncoding(t *testing.T) {
	source, err := os.ReadFile("../../examples/expressive-guitar.cicada")
	if err != nil {
		t.Fatal(err)
	}
	score, ds := notation.Parse(source)
	if score == nil {
		t.Fatal(ds)
	}
	p, ds := project.FromScore(score)
	if p == nil {
		t.Fatal(ds)
	}
	cfg, err := project.CompileEngine(p, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	image, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint16(image[4:]); got != 15 {
		t.Fatalf("guitar version = %d, want 15", got)
	}
	decoded, err := kernelimage.Decode(image, 48000, 128)
	if err != nil || !reflect.DeepEqual(cfg, decoded) {
		t.Fatalf("guitar image changed: %v", err)
	}
	if _, err := engine.New(decoded); err != nil {
		t.Fatal(err)
	}
	t.Run("guitar with graph delay capability", func(t *testing.T) {
		delaySource, err := os.ReadFile("../../examples/pluck.cicada")
		if err != nil {
			t.Fatal(err)
		}
		delayScore, ds := notation.Parse(delaySource)
		delayProject, ds := project.FromScore(delayScore)
		if delayProject == nil {
			t.Fatal(ds)
		}
		delayConfig, err := project.CompileEngine(delayProject, 48000, 128)
		if err != nil {
			t.Fatal(err)
		}
		mixed := cfg
		mixed.Tracks = 2
		mixed.Track[1] = delayConfig.Track[0]
		mixed.Patterns = append(append([]engine.PatternBank(nil), cfg.Patterns...), delayConfig.Patterns[0])
		data, err := kernelimage.Encode(mixed)
		if err != nil {
			t.Fatal(err)
		}
		if binary.LittleEndian.Uint16(data[4:6]) != 15 || binary.LittleEndian.Uint16(data[30:32]) != kernelimage.DelayCapability {
			t.Fatal("guitar image lost graph delay capability")
		}
		decoded, err := kernelimage.Decode(data, 48000, 128)
		if err != nil || !reflect.DeepEqual(mixed, decoded) {
			t.Fatalf("mixed image roundtrip: %v", err)
		}
		if _, err := engine.New(decoded); err != nil {
			t.Fatal(err)
		}
	})
	for _, version := range []uint16{8, 9, 10, 11, 12, 13, 14} {
		binary.LittleEndian.PutUint16(image[4:], version)
		if _, err := kernelimage.Decode(image, 48000, 128); err == nil {
			t.Fatalf("guitar layout accepted as version %d", version)
		}
	}
	cfg.Track[0].Experimental = false
	if _, err := kernelimage.Encode(cfg); err == nil {
		t.Fatal("image encoder accepted guitar without opt-in")
	}
	legacy, err := kernelimage.Encode(firstAcidConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint16(legacy[4:]) != 13 {
		t.Fatal("legacy encoding changed version")
	}
	if _, err := kernelimage.Decode(legacy, 48000, 128); err != nil {
		t.Fatal(err)
	}
	t.Log("METRIC: legacy image loading | versions 8..13 covered by existing compatibility tests; legacy encoder remains version 13")
}
