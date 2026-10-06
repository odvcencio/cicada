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

func TestDelayCapabilityImage(t *testing.T) {
	source, err := os.ReadFile("../../examples/pluck.cicada")
	if err != nil {
		t.Fatal(err)
	}
	score, ds := notation.Parse(source)
	if score == nil || len(ds) != 0 {
		t.Fatalf("parse: %+v", ds)
	}
	p, ds := project.FromScore(score)
	if p == nil {
		t.Fatalf("project: %+v", ds)
	}
	for _, rate := range []int{44_100, 48_000} {
		cfg, err := project.CompileEngine(p, rate, 128)
		if err != nil {
			t.Fatal(err)
		}
		data, err := kernelimage.Encode(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if binary.LittleEndian.Uint16(data[4:6]) != 13 || binary.LittleEndian.Uint16(data[30:32]) != kernelimage.DelayCapability {
			t.Fatal("delay image header changed")
		}
		decoded, err := kernelimage.Decode(data, rate, 128)
		if err != nil || !reflect.DeepEqual(cfg, decoded) {
			t.Fatalf("delay image roundtrip: %v", err)
		}
		if _, err := engine.New(decoded); err != nil {
			t.Fatal(err)
		}
		data[30] = 0
		if _, err := kernelimage.Decode(data, rate, 128); err == nil {
			t.Fatal("delay image accepted without capability")
		}
		data[30] = 4
		if _, err := kernelimage.Decode(data, rate, 128); err == nil {
			t.Fatal("unknown capability accepted")
		}
	}
	legacy, err := kernelimage.Encode(firstAcidConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint16(legacy[30:32]) != 0 {
		t.Fatal("legacy image requires a new capability")
	}
	if _, err := kernelimage.Decode(legacy, 48_000, 128); err != nil {
		t.Fatal(err)
	}
}

func TestDelayLegacyImagesStillLoad(t *testing.T) {
	cfg := engine.Config{SampleRate: 48_000, MaxBlock: 128, Tracks: 1, MaxVoices: 1, BPMMilli: 120_000,
		Patterns: []engine.PatternBank{{}}, Scenes: []engine.Scene{}, Song: []engine.SongEntry{}}
	cfg.Track[0].Kind = engine.VoiceAcid
	encoded, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for version := uint16(8); version <= 13; version++ {
		data := append([]byte(nil), encoded...)
		if version < 11 {
			data = removeImageRange(data, 32+3+2+37, 3)
		}
		if version < 12 {
			data = removeImageRange(data, 32+3, 2)
		}
		binary.LittleEndian.PutUint16(data[4:6], version)
		decoded, err := kernelimage.Decode(data, 48_000, 128)
		if err != nil || !reflect.DeepEqual(cfg, decoded) {
			t.Fatalf("legacy image %d changed: %v", version, err)
		}
		if _, err := engine.New(decoded); err != nil {
			t.Fatalf("legacy image %d load: %v", version, err)
		}
	}
	t.Log("METRIC: older kernel images loaded | versions=8,9,10,11,12,13 count=6")
}
