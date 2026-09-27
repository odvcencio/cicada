package kernelimage_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/graph"
	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func firstAcidConfig(t *testing.T) engine.Config {
	t.Helper()
	source, err := os.ReadFile(filepath.Join("..", "..", "examples", "first-acid.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	score, diagnostics := notation.Parse(source)
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			t.Fatalf("first-acid parse: %+v", diagnostic)
		}
	}
	p, diagnostics := project.FromScore(score)
	if p == nil {
		t.Fatalf("first-acid project: %+v", diagnostics)
	}
	cfg, err := project.CompileEngine(p, 48_000, 128)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestFirstAcidProjectImageRoundTrip(t *testing.T) {
	cfg := firstAcidConfig(t)
	encoded, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > kernelimage.MaxImageBytes {
		t.Fatalf("image grew to %d bytes", len(encoded))
	}
	decoded, err := kernelimage.Decode(encoded, 48_000, 128)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg, decoded) {
		t.Fatal("compiled project changed in binary image round trip")
	}
	if _, err := engine.New(decoded); err != nil {
		t.Fatalf("decoded first-acid project cannot play: %v", err)
	}
}

func TestSceneSettingsProjectImageRoundTrip(t *testing.T) {
	source := []byte("fx delay { feedback = 0.2 }\ntrack bass acid { send_a = 0.2 }\npattern riff acid steps=1 { 1 }\nscene drop { bass=riff bass.cutoff=900Hz delay.feedback=0.4 delay.time=1/8 }\nsong { drop }\n")
	score, diagnostics := notation.Parse(source)
	if score == nil {
		t.Fatalf("scene settings parse: %+v", diagnostics)
	}
	p, diagnostics := project.FromScore(score)
	if p == nil {
		t.Fatalf("scene settings project: %+v", diagnostics)
	}
	cfg, err := project.CompileEngine(p, 48_000, 128)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := kernelimage.Decode(encoded, 48_000, 128)
	if err != nil || !reflect.DeepEqual(cfg, decoded) {
		t.Fatalf("scene settings changed in image round trip: %v", err)
	}
	if len(decoded.Scenes) != 1 || len(decoded.Scenes[0].Settings) != 3 || decoded.Scenes[0].Settings[0].Value != 900 || decoded.Scenes[0].Settings[1].Track != 0xff || decoded.Scenes[0].Settings[2].Division.String() != "1/8" {
		t.Fatalf("scene settings missing from decoded image: %+v", decoded.Scenes)
	}
	if _, err := engine.New(decoded); err != nil {
		t.Fatalf("decoded synced scene delay cannot play: %v", err)
	}
}

func TestVersion8ProjectImageWithoutSettingsStillDecodes(t *testing.T) {
	cfg := firstAcidConfig(t)
	for i := range cfg.Scenes {
		cfg.Scenes[i].Settings = nil
	}
	encoded, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	legacy := append([]byte(nil), encoded...)
	if len(cfg.Scenes) > 0 {
		sceneStart := len(legacy) - len(cfg.Song)*4 - len(cfg.Scenes)*18
		for i := len(cfg.Scenes) - 1; i >= 0; i-- {
			countOffset := sceneStart + i*18 + 16
			copy(legacy[countOffset:], legacy[countOffset+2:])
			legacy = legacy[:len(legacy)-2]
		}
	}
	legacy[4], legacy[5] = 8, 0
	decoded, err := kernelimage.Decode(legacy, 48_000, 128)
	if err != nil {
		t.Fatalf("version 8 project image was rejected: %v", err)
	}
	if !reflect.DeepEqual(cfg, decoded) {
		t.Fatal("version 8 image changed project data")
	}
}

func TestVersion9SceneSettingsDecodeWithoutDivisionField(t *testing.T) {
	cfg := firstAcidConfig(t)
	cfg.Scenes = []engine.Scene{{Settings: []engine.SceneSetting{{Track: 0, ID: kernel.ParamAcidCutoff, Value: 900}}}}
	cfg.Song = []engine.SongEntry{{Scene: 0, Bars: 1}}
	encoded, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	legacy := append([]byte(nil), encoded...)
	sceneStart := len(legacy) - len(cfg.Song)*4 - (16 + 2 + 1 + 2 + 4 + 1)
	divisionOffset := sceneStart + 16 + 2 + 1 + 2 + 4
	copy(legacy[divisionOffset:], legacy[divisionOffset+1:])
	legacy = legacy[:len(legacy)-1]
	legacy[4], legacy[5] = 9, 0
	decoded, err := kernelimage.Decode(legacy, 48_000, 128)
	if err != nil {
		t.Fatalf("version 9 image with scene settings was rejected: %v", err)
	}
	if !reflect.DeepEqual(cfg, decoded) {
		t.Fatal("version 9 scene settings changed in the image round trip")
	}
}

func TestAuthoredKitImageRoundTrip(t *testing.T) {
	cfg := firstAcidConfig(t)
	var track int
	for cfg.Track[track].Kind != engine.VoiceDrums {
		track++
		if track == cfg.Tracks {
			t.Fatal("first-acid fixture has no drum track")
		}
	}
	kit := new([drum.LaneCount]engine.KitLaneBinding)
	kit[drum.BD] = engine.KitLaneBinding{Kind: engine.KitLaneBuiltin, Recipe: drum.SD}
	kit[drum.CH] = engine.KitLaneBinding{Kind: engine.KitLaneGraph, Program: graph.Program{
		Len: 1, Nodes: [graph.MaxNodes]graph.Node{{Op: graph.Gate}},
	}}
	cfg.Track[track].Kit = kit
	encoded, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := kernelimage.Decode(encoded, 48_000, 128)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg, decoded) {
		t.Fatal("authored kit changed across project image")
	}
	if _, err := engine.New(decoded); err != nil {
		t.Fatalf("decoded authored kit cannot play: %v", err)
	}
	kit[drum.BD].Recipe = drum.LaneCount
	if _, err := kernelimage.Encode(cfg); err == nil {
		t.Fatal("invalid kit recipe was encoded")
	}
}

func TestDriveInsertImageRoundTrip(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "examples", "fx", "drive-insert.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	score, diagnostics := notation.Parse(source)
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			t.Fatalf("source: %+v", diagnostic)
		}
	}
	p, diagnostics := project.FromScore(score)
	if p == nil {
		t.Fatalf("project: %+v", diagnostics)
	}
	cfg, err := project.CompileEngine(p, 48_000, 128)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := kernelimage.Decode(encoded, 48_000, 128)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg, decoded) || decoded.Track[0].InsertDrive == nil {
		t.Fatal("drive insert was lost in the project image")
	}
	if _, err := engine.New(decoded); err != nil {
		t.Fatal(err)
	}
	cfg.Track[0].InsertDrive.Mix = 2
	if _, err := kernelimage.Encode(cfg); err == nil {
		t.Fatal("invalid drive mix was encoded")
	}
}

func TestDelaySendImageRoundTrip(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "examples", "fx", "delay-send.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	score, diagnostics := notation.Parse(source)
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			t.Fatalf("source: %+v", diagnostic)
		}
	}
	p, diagnostics := project.FromScore(score)
	if p == nil {
		t.Fatalf("project: %+v", diagnostics)
	}
	cfg, err := project.CompileEngine(p, 48_000, 128)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DelayA == nil || cfg.Track[0].SendA == 0 {
		t.Fatal("delay route was not compiled")
	}
	encoded, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := kernelimage.Decode(encoded, 48_000, 128)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg, decoded) {
		t.Fatal("delay send changed in the project image")
	}
	if _, err := engine.New(decoded); err != nil {
		t.Fatal(err)
	}
}

func TestReverbSendImageRoundTrip(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "examples", "fx-bus.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	score, diagnostics := notation.Parse(source)
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			t.Fatalf("source: %+v", diagnostic)
		}
	}
	p, diagnostics := project.FromScore(score)
	if p == nil {
		t.Fatalf("project: %+v", diagnostics)
	}
	cfg, err := project.CompileEngine(p, 48_000, 128)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := kernelimage.Decode(encoded, 48_000, 128)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg, decoded) || decoded.ReverbB == nil || decoded.Track[0].SendB == 0 {
		t.Fatal("reverb send changed in the project image")
	}
	if _, err := engine.New(decoded); err != nil {
		t.Fatal(err)
	}
}

func TestCompressorBusImageRoundTrip(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "examples", "fx", "compressor-bus.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	score, diagnostics := notation.Parse(source)
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			t.Fatalf("source: %+v", diagnostic)
		}
	}
	p, diagnostics := project.FromScore(score)
	if p == nil {
		t.Fatalf("project: %+v", diagnostics)
	}
	cfg, err := project.CompileEngine(p, 48_000, 128)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := kernelimage.Decode(encoded, 48_000, 128)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg, decoded) || decoded.CompMusic == nil || decoded.CompSidechainTrack != 2 {
		t.Fatal("compressor changed in the project image")
	}
	if _, err := engine.New(decoded); err != nil {
		t.Fatal(err)
	}
}

func TestSFXBusImageRoundTrip(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "examples", "sfx-bus.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	score, diagnostics := notation.Parse(source)
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			t.Fatalf("source: %+v", diagnostic)
		}
	}
	p, diagnostics := project.FromScore(score)
	if p == nil {
		t.Fatalf("project: %+v", diagnostics)
	}
	cfg, err := project.CompileEngine(p, 48_000, 128)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := kernelimage.Decode(encoded, 48_000, 128)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg, decoded) || !decoded.Track[1].BusSFX || decoded.CompSidechainTrack != engine.SFXSidechain {
		t.Fatal("SFX route changed in the project image")
	}
	if _, err := engine.New(decoded); err != nil {
		t.Fatal(err)
	}
}

func TestProjectImageRejectsCorruption(t *testing.T) {
	encoded, err := kernelimage.Encode(firstAcidConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, length := range []int{0, 31, 32, len(encoded) - 1} {
		if _, err := kernelimage.Decode(encoded[:length], 48_000, 128); err == nil {
			t.Fatalf("truncated image of %d bytes was accepted", length)
		}
	}
	if _, err := kernelimage.Decode(encoded, 44_100, 128); err == nil {
		t.Fatal("sample-rate mismatch was accepted")
	}
	corrupt := append([]byte(nil), encoded...)
	corrupt[0] = 'X'
	if _, err := kernelimage.Decode(corrupt, 48_000, 128); err == nil {
		t.Fatal("bad magic was accepted")
	}
	for _, oldVersion := range []byte{1, 2, 3, 4, 5, 6, 7} {
		corrupt = append([]byte(nil), encoded...)
		corrupt[4] = oldVersion
		if _, err := kernelimage.Decode(corrupt, 48_000, 128); err == nil {
			t.Fatalf("old image version %d was accepted with a new track layout", oldVersion)
		}
	}
	corrupt = append(append([]byte(nil), encoded...), 0)
	if _, err := kernelimage.Decode(corrupt, 48_000, 128); err == nil {
		t.Fatal("trailing bytes were accepted")
	}
}

func TestProjectImageRejectsValuesThatOverflowWireFields(t *testing.T) {
	wireOverflow := uint64(1) << 32
	for _, change := range []struct {
		name string
		edit func(*engine.Config)
	}{
		{"sample rate", func(cfg *engine.Config) { cfg.SampleRate += int(wireOverflow) }},
		{"tempo", func(cfg *engine.Config) { cfg.BPMMilli += 1 << 32 }},
		{"negative tempo", func(cfg *engine.Config) { cfg.BPMMilli = -1<<32 + cfg.BPMMilli }},
	} {
		t.Run(change.name, func(t *testing.T) {
			if change.name == "sample rate" && strconv.IntSize < 64 {
				t.Skip("int cannot represent a sample rate above the wire range")
			}
			cfg := firstAcidConfig(t)
			change.edit(&cfg)
			if _, err := kernelimage.Encode(cfg); err == nil {
				t.Fatal("out-of-range configuration was encoded as a different value")
			}
		})
	}
}
