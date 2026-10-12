package kernelimage_test

import (
	"bytes"
	"encoding/binary"
	"math"
	"reflect"
	"testing"

	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func sceneTempoImageConfig(t *testing.T, tempo string) engine.Config {
	t.Helper()
	source := []byte("tempo 120\ntrack bass acid {}\npattern riff acid steps=1 { 1 }\nscene a { bass=riff }\nscene b { bass=riff tempo = " + tempo + " }\nsong { a b*2 }\n")
	score, diagnostics := notation.Parse(source)
	if score == nil || len(diagnostics) != 0 {
		t.Fatalf("parse: %+v", diagnostics)
	}
	p, diagnostics := project.FromScore(score)
	if p == nil {
		t.Fatalf("project: %+v", diagnostics)
	}
	cfg, err := project.CompileEngine(p, 48_000, 128)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestSceneTempoSurvivesImageRoundTrip(t *testing.T) {
	cfg := sceneTempoImageConfig(t, "84")
	if got := cfg.Scenes[1].Settings; len(got) != 1 || got[0].ID != kernel.ParamGlobalTempo || got[0].Track != 0xff || got[0].Value != 84 {
		t.Fatalf("compiled settings: %+v", got)
	}
	encoded, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := kernelimage.Decode(encoded, 48_000, 128)
	if err != nil || !reflect.DeepEqual(cfg, decoded) {
		t.Fatalf("tempo changed in image round trip: %v", err)
	}
	if _, err := engine.New(decoded); err != nil {
		t.Fatalf("decoded tempo scene cannot play: %v", err)
	}
	// The record layout is the existing scene setting: owner, ID, float, division.
	var record bytes.Buffer
	record.WriteByte(0xff)
	binary.Write(&record, binary.LittleEndian, uint16(kernel.ParamGlobalTempo))
	binary.Write(&record, binary.LittleEndian, math.Float32bits(84))
	record.WriteByte(0)
	if !bytes.Contains(encoded, record.Bytes()) {
		t.Fatal("tempo is not stored as a plain scene setting record")
	}
}

func TestSceneTempoOutOfRangeIsRejected(t *testing.T) {
	cfg := sceneTempoImageConfig(t, "84")
	for _, bad := range []float32{19, 301} {
		bad := bad
		broken := cfg
		broken.Scenes = append([]engine.Scene(nil), cfg.Scenes...)
		broken.Scenes[1].Settings = []engine.SceneSetting{{Track: 0xff, ID: kernel.ParamGlobalTempo, Value: bad}}
		if _, err := kernelimage.Encode(broken); err == nil {
			t.Fatalf("encode accepted tempo %v", bad)
		}
	}
	encoded, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var good bytes.Buffer
	binary.Write(&good, binary.LittleEndian, math.Float32bits(84))
	for _, bad := range []float32{19, 301} {
		patched := bytes.Replace(encoded, good.Bytes(), binary.LittleEndian.AppendUint32(nil, math.Float32bits(bad)), 1)
		if bytes.Equal(patched, encoded) {
			t.Fatal("could not patch the tempo value")
		}
		if _, err := kernelimage.Decode(patched, 48_000, 128); err == nil {
			t.Fatalf("decode accepted tempo %v", bad)
		}
	}
}
