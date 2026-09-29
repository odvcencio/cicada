package kernelimage_test

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/graph"
	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func firstAcidConfig(t *testing.T) engine.Config {
	t.Helper()
	source, err := os.ReadFile(filepath.Join("..", "..", "testdata", "edition1", "examples", "first-acid.cicada"))
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

func removeImageRange(data []byte, start, count int) []byte {
	return append(append([]byte(nil), data[:start]...), data[start+count:]...)
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
	cfg := engine.Config{
		SampleRate: 48_000, MaxBlock: 128, Tracks: 1, MaxVoices: 1, BPMMilli: 120_000,
		Patterns: []engine.PatternBank{{}},
		Scenes:   []engine.Scene{{}, {}},
		Song:     []engine.SongEntry{{Scene: 0, Bars: 2}, {Scene: 1, Bars: 3}},
	}
	cfg.Track[0].Kind = engine.VoiceAcid
	cfg.Scenes[1].Track[0].Mode = engine.SceneOff
	encoded, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	legacy := append([]byte(nil), encoded...)
	// Versions before 11 omit the per-send taps and track solo flags.
	legacy = removeImageRange(legacy, 32+3+2+37, 3)
	// Versions before 12 omit built-in bus and master flags.
	legacy = removeImageRange(legacy, 32+3, 2)
	if len(cfg.Scenes) > 0 {
		sceneStart := len(legacy) - len(cfg.Song)*4 - len(cfg.Scenes)*18
		for i := len(cfg.Scenes) - 1; i >= 0; i-- {
			countOffset := sceneStart + i*18 + 16
			legacy = removeImageRange(legacy, countOffset, 2)
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

func TestVersion9GraphGlideWithoutSceneSettingsStillDecodes(t *testing.T) {
	cfg := engine.Config{
		SampleRate: 48_000, MaxBlock: 128, Tracks: 1, MaxVoices: 1, BPMMilli: 120_000,
		Patterns: []engine.PatternBank{{}}, Scenes: []engine.Scene{}, Song: []engine.SongEntry{},
	}
	cfg.Track[0].Kind = engine.VoiceGraph
	cfg.Track[0].Graph = graph.Program{
		Len: 2, Output: 1, GlideMS: 60,
		Nodes: [graph.MaxNodes]graph.Node{{Op: graph.Pitch}, {Op: graph.Sine, A: 0}},
	}
	encoded, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	legacy := append([]byte(nil), encoded...)
	// Version 9 carries graph glide but predates P2's track and bus flags.
	legacy = removeImageRange(legacy, 32+3+2+37, 3)
	legacy = removeImageRange(legacy, 32+3, 2)
	binary.LittleEndian.PutUint16(legacy[4:6], 9)
	decoded, err := kernelimage.Decode(legacy, 48_000, 128)
	if err != nil {
		t.Fatalf("version 9 graph glide image was rejected: %v", err)
	}
	if !reflect.DeepEqual(cfg, decoded) {
		t.Fatal("version 9 graph glide or scene data changed")
	}
	if _, err := engine.New(decoded); err != nil {
		t.Fatalf("decoded version 9 graph glide cannot play: %v", err)
	}
}

func TestBuiltInBusMixerImageRoundTrip(t *testing.T) {
	cfg := firstAcidConfig(t)
	cfg.MusicBusMute, cfg.MusicBusSolo = true, true
	cfg.SFXBusMute, cfg.SFXBusSolo, cfg.MasterMute, cfg.MasterSolo = true, true, true, true
	encoded, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := kernelimage.Decode(encoded, 48_000, 128)
	if err != nil || !reflect.DeepEqual(cfg, decoded) {
		t.Fatalf("built-in bus mixer changed across project image: %v", err)
	}
}

func TestVersion12ImageDefaultsMasterSoloOff(t *testing.T) {
	cfg := engine.Config{Tracks: 1, MaxVoices: 1, SampleRate: 48_000, MaxBlock: 128, BPMMilli: 120_000}
	cfg.MasterSolo = true
	encoded, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	legacy := append([]byte(nil), encoded...)
	legacy[4], legacy[5] = 12, 0
	legacy[35] &^= 32
	decoded, err := kernelimage.Decode(legacy, 48_000, 128)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.MasterSolo {
		t.Fatal("version 12 image acquired a master solo flag")
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
	source, err := os.ReadFile(filepath.Join("..", "..", "testdata", "edition1", "examples", "fx", "drive-insert.cicada"))
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
	source, err := os.ReadFile(filepath.Join("..", "..", "testdata", "edition1", "examples", "fx", "delay-send.cicada"))
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
	source, err := os.ReadFile(filepath.Join("..", "..", "testdata", "edition1", "examples", "fx-bus.cicada"))
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
	source, err := os.ReadFile(filepath.Join("..", "..", "testdata", "edition1", "examples", "fx", "compressor-bus.cicada"))
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
	source, err := os.ReadFile(filepath.Join("..", "..", "testdata", "edition1", "examples", "sfx-bus.cicada"))
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

func TestProjectImageDecodesVersionEightGraphWithoutGlideField(t *testing.T) {
	program := graph.Program{
		Len: 2, Output: 1, GlideMS: 60,
		Nodes: [graph.MaxNodes]graph.Node{{Op: graph.Pitch}, {Op: graph.Sine, A: 0}},
	}
	cfg := engine.Config{
		SampleRate: 48_000, MaxBlock: 128, Tracks: 1, MaxVoices: 1, BPMMilli: 120_000,
	}
	cfg.Track[0].Kind = engine.VoiceGraph
	cfg.Track[0].Graph = program
	encoded, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	nodeWire := make([]byte, 0, int(program.Len)*8)
	for i := uint8(0); i < program.Len; i++ {
		node := program.Nodes[i]
		nodeWire = append(nodeWire, byte(node.Op), node.A, node.B, node.C)
		var value [4]byte
		binary.LittleEndian.PutUint32(value[:], math.Float32bits(node.Value))
		nodeWire = append(nodeWire, value[:]...)
	}
	nodeAt := bytes.Index(encoded, nodeWire)
	if nodeAt < 8 || bytes.Index(encoded[nodeAt+1:], nodeWire) >= 0 {
		t.Fatal("could not uniquely locate graph nodes in current image")
	}
	if got := math.Float64frombits(binary.LittleEndian.Uint64(encoded[nodeAt-8 : nodeAt])); got != 60 {
		t.Fatalf("encoded glide time is %g ms, want 60 ms", got)
	}
	legacy := make([]byte, 0, len(encoded)-8)
	legacy = append(legacy, encoded[:nodeAt-8]...)
	legacy = append(legacy, encoded[nodeAt:]...)
	legacy = removeImageRange(legacy, 32+3+2+37, 3)
	legacy = removeImageRange(legacy, 32+3, 2)
	binary.LittleEndian.PutUint16(legacy[4:6], 8)

	decoded, err := kernelimage.Decode(legacy, 48_000, 128)
	if err != nil {
		t.Fatalf("decode version-eight graph image: %v", err)
	}
	if decoded.Track[0].Graph.GlideMS != 0 {
		t.Fatalf("version-eight image glide time is %g ms, want legacy zero", decoded.Track[0].Graph.GlideMS)
	}
	if _, err := engine.New(decoded); err != nil {
		t.Fatalf("decoded version-eight graph image cannot play: %v", err)
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
