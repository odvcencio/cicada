package project

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/notation"
)

func liveTestProject(t *testing.T) *Project {
	t.Helper()
	source := []byte(`cicada 2
track bass acid {}
track drums drums {}
track lead acid {}
track pad acid {}
live {
 land = bar
 phrase = 8bars
 macro intensity = 0.3 smooth 400ms
 layers intensity { lead >= 0.70 drums >= 0.25 bass >= 0.45 attack 1bar release 3bars }
}
pattern p { 1 . }
pattern beat drums { bd: x... }
scene main { bass = p drums = beat lead = p pad = p }
song { main*8 }
`)
	score, ds := notation.Parse(source)
	if hasProjectErrors(ds) {
		t.Fatalf("parse: %+v", ds)
	}
	p, ds := FromScore(score)
	if p == nil || hasProjectErrors(ds) {
		t.Fatalf("compile: %+v", ds)
	}
	return p
}

func TestLiveCommandsMasksAndThresholds(t *testing.T) {
	p := liveTestProject(t)
	commands := LiveCommands(p)
	want := []cmd.Command{
		{Op: cmd.OpDefineMacro, Track: 255, Arg0: math.Float32bits(0.3)},
		{Op: cmd.OpSetLayers, Track: 255, Arg0: 64 | 115<<8 | 179<<16, Arg1: 3},
		{Op: cmd.OpSetLayerMasks, Track: 255, Arg0: 8 | 10<<16, Arg1: 11 | 15<<16},
		{Op: cmd.OpSetPhraseBars, Track: 255, Arg0: 8},
	}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands: %+v, want %+v", commands, want)
	}
	for _, c := range commands {
		if err := c.Validate(4); err != nil {
			t.Fatalf("invalid command %+v: %v", c, err)
		}
	}
	for _, tc := range []struct {
		name                                   string
		values                                 []float64
		thresholds, mask0, mask1, mask2, mask3 uint32
	}{
		{"shared", []float64{0.5, 0.5, 0.75}, 128 | 191<<8, 8, 14, 15, 15},
		{"zero", []float64{0, 0.45, 1}, 115 | 255<<8, 12, 14, 15, 15},
		{"packed equal", []float64{0.45, 0.4501, 0.7}, 115 | 179<<8, 8, 14, 15, 15},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for i, v := range tc.values {
				p.Live.Layers[0].Rules[i].Threshold = v
			}
			got := LiveCommands(p)
			if got[1].Arg0 != tc.thresholds || got[2].Arg0 != tc.mask0|tc.mask1<<16 || got[2].Arg1 != tc.mask2|tc.mask3<<16 {
				t.Fatalf("commands: %+v", got)
			}
			for _, c := range got {
				if err := c.Validate(4); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestLiveSurfaceNamesAndSmoothing(t *testing.T) {
	p := liveTestProject(t)
	p.Live.Macros = append(p.Live.Macros, LiveMacro{Name: "motion", Default: 1, SmoothMS: 1500}, LiveMacro{Name: "dry", Default: 0})
	surface := LiveSurfaceOf(p)
	if surface.Land != "bar" || surface.PhraseBars != 8 {
		t.Fatalf("surface: %+v", surface)
	}
	for i, want := range []struct {
		name   string
		value  float64
		frames uint32
	}{{"intensity", 0.3, 19200}, {"motion", 1, 72000}, {"dry", 0, 0}} {
		m := surface.Macros[i]
		if m.Name != want.name || m.Default != want.value || m.SmoothingFrames(48000) != want.frames {
			t.Fatalf("macro %d: %+v", i, m)
		}
		c := LiveCommands(p)[i]
		if c.Index != uint16(i) || c.Op != cmd.OpDefineMacro || c.Arg1 != 0 {
			t.Fatalf("initial command: %+v", c)
		}
	}
	if surface.Macros[0].SmoothingFrames(44100) != 17640 || surface.Macros[0].SmoothingFrames(0) != 0 || (LiveMacro{SmoothMS: 0.1}).SmoothingFrames(44100) != 4 {
		t.Fatal("sample rate conversion")
	}
	surface.Macros[0].Name = "changed"
	if p.Live.Macros[0].Name != "intensity" {
		t.Fatal("surface aliases project")
	}
}

func TestLiveProjectRoundTrip(t *testing.T) {
	p := liveTestProject(t)
	data, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	if !SemanticEqual(p, decoded) || !reflect.DeepEqual(LiveCommands(p), LiveCommands(decoded)) {
		t.Fatal("JSON changed live contract")
	}
	source, err := ToSource(decoded)
	if err != nil {
		t.Fatal(err)
	}
	score, ds := notation.Parse(source)
	if hasProjectErrors(ds) || score.Live == nil {
		t.Fatalf("source: %s\n%+v", source, ds)
	}
	p.Live.Macros[0].Default = 2
	if err := ValidateProject(p); err == nil {
		t.Fatal("semantic validator accepted out-of-range macro")
	}
}

func TestLiveBlockIsOptional(t *testing.T) {
	count := 0
	for _, root := range []string{"../examples", "../testdata/edition1/examples"} {
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || filepath.Ext(path) != ".cicada" || filepath.Base(path) == "live-intensity.cicada" {
				return nil
			}
			source, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			edition := 2
			if root != "../examples" {
				edition = 1
			}
			score, ds := notation.ParseEdition(source, edition)
			if hasProjectErrors(ds) {
				t.Fatalf("%s: %+v", path, ds)
			}
			p, ds := FromScore(score)
			if p == nil || hasProjectErrors(ds) {
				t.Fatalf("%s: %+v", path, ds)
			}
			if p.Live != nil || len(LiveCommands(p)) != 0 || len(LiveSurfaceOf(p).Macros) != 0 {
				t.Fatalf("%s acquired live controls", path)
			}
			data, err := CanonicalJSON(p)
			if err != nil {
				return err
			}
			// Scores without live must omit it and retain their JSON across decoding.
			var wire map[string]json.RawMessage
			if err := json.Unmarshal(data, &wire); err != nil {
				return err
			}
			if _, exists := wire["live"]; exists {
				t.Fatalf("%s emits live JSON", path)
			}
			decoded, err := DecodeJSON(data)
			if err != nil {
				return err
			}
			again, err := CanonicalJSON(decoded)
			if err != nil {
				return err
			}
			if !bytes.Equal(data, again) || !reflect.DeepEqual(LiveCommands(p), LiveCommands(decoded)) {
				t.Fatalf("%s changed commands or JSON", path)
			}
			cfg, err := CompileEngine(p, 48000, 128)
			if err != nil {
				return err
			}
			decodedCfg, err := CompileEngine(decoded, 48000, 128)
			if err != nil {
				return err
			}
			// Config contains program pointers; DeepEqual compares their compiled contents.
			if !reflect.DeepEqual(cfg, decodedCfg) {
				t.Fatalf("%s changed engine configuration", path)
			}
			count++
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if count == 0 {
		t.Fatal("no existing examples checked")
	}
	t.Logf("%d existing examples retain JSON and engine configuration without live commands", count)
}
