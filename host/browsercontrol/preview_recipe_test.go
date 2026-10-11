package browsercontrol

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/demopolicy"
	"m31labs.dev/cicada/host/liveplay"
	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func previewRecipeProject(t *testing.T, lane, recipe string) (*project.Project, engine.Config) {
	t.Helper()
	source := fmt.Sprintf("cicada 2\ntempo 120\nkit acoustic { %s=builtin.%s }\ntrack bass acoustic {}\npattern beat drums steps=2 { %s:x. }\nscene main { bass=beat }\nsong { main }\n", lane, recipe, lane)
	score, diagnostics := notation.Parse([]byte(source))
	for _, d := range diagnostics {
		if d.Severity == "error" {
			t.Fatal(d)
		}
	}
	p, diagnostics := project.FromScore(score)
	for _, d := range diagnostics {
		if d.Severity == "error" {
			t.Fatal(d)
		}
	}
	cfg, err := project.CompileEngine(p, 48_000, 256)
	if err != nil {
		t.Fatal(err)
	}
	return p, cfg
}

func previewRecipeBrowser(t *testing.T, p *project.Project, cfg engine.Config) (*Controller, *engine.Engine, *int) {
	t.Helper()
	e, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	sent := new(int)
	c, err := New(p, cfg, demopolicy.PublicDemo(), func(data []byte) error {
		for at := 0; at < len(data); at += cmd.CommandSize {
			command, err := cmd.DecodeCommand(data[at:at+cmd.CommandSize], uint8(cfg.Tracks))
			if err != nil {
				return err
			}
			if !e.Push(command) {
				return fmt.Errorf("engine queue full")
			}
			*sent += 1
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Play(); err != nil {
		t.Fatal(err)
	}
	previewRecipeRunning(t, e)
	return c, e, sent
}

func previewRecipeRunning(t *testing.T, e *engine.Engine) {
	t.Helper()
	var left, right [256]float32
	e.Render(left[:], right[:])
	var message cmd.Message
	for e.Poll(&message) {
		if message.Kind == cmd.Fault {
			t.Fatalf("parameter command faulted playback: %d", message.A)
		}
	}
	if !e.Playing() {
		t.Fatal("playback stopped")
	}
}

func previewRecipeApplied(t *testing.T, e *engine.Engine, id kernel.ParamID, want float32) {
	t.Helper()
	// This test goroutine owns rendering, so it may inspect the engine targets.
	validator := e.PreviewParamValidator(0)
	value, ok := validator.CommittedValue(id)
	if !ok || math.Float32bits(float32(value)) != math.Float32bits(want) {
		t.Fatalf("%s engine target=%g/%v, want %g", kernel.Params[id].Name, value, ok, want)
	}
}

func TestPreviewBrowserRemappedKitRejectsRecipeInvalidControl(t *testing.T) {
	p, cfg := previewRecipeProject(t, "bd", "sd")
	c, e, sent := previewRecipeBrowser(t, p, cfg)
	value := float64(55)
	before := *sent
	err := c.SetParam("bass.bd_tune", &value)
	// Inspect the engine even if admission incorrectly succeeded.
	previewRecipeRunning(t, e)
	if err == nil || *sent != before {
		t.Fatalf("invalid remapped control: error=%v, commands=%d", err, *sent-before)
	}
}

func previewRecipeKernelAccepts(t *testing.T, cfg engine.Config, id kernel.ParamID, value float32) bool {
	t.Helper()
	command := cmd.Command{Op: cmd.OpSetParam, Track: 0, Index: uint16(id), Arg0: math.Float32bits(value)}
	if command.Validate(uint8(cfg.Tracks)) != nil {
		return false
	}
	e, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255}) || !e.Push(command) {
		t.Fatal("oracle command publication failed")
	}
	var left, right [256]float32
	e.Render(left[:], right[:])
	var message cmd.Message
	for e.Poll(&message) {
		if message.Kind == cmd.Fault {
			if message.A != engine.FaultParam {
				t.Fatalf("unrelated oracle fault: %d", message.A)
			}
			return false
		}
	}
	if !e.Playing() {
		t.Fatal("oracle stopped without a parameter fault")
	}
	return true
}

func TestPreviewRemappedKitLaneRecipeMatrix(t *testing.T) {
	accepted, rejected, pairs, mappings := 0, 0, 0, 0
	for _, lane := range drum.Names {
		for _, recipe := range drum.Names {
			p, cfg := previewRecipeProject(t, lane, recipe)
			mappings++
			for _, spec := range kernel.Params {
				if !spec.Live || !strings.HasPrefix(spec.Name, "drum."+lane+".") {
					continue
				}
				pairs++
				values := []float32{spec.Default, spec.Min, spec.Max,
					math.Nextafter32(spec.Min, float32(math.Inf(-1))),
					math.Nextafter32(spec.Max, float32(math.Inf(1))),
					math.Nextafter32(spec.Min, spec.Default), math.Nextafter32(spec.Max, spec.Default),
					float32(math.Inf(-1)), float32(math.NaN())}
				for vi, value := range values {
					want := previewRecipeKernelAccepts(t, cfg, spec.ID, value)
					c, browserEngine, sent := previewRecipeBrowser(t, p, cfg)
					before := *sent
					number := float64(value)
					input := &number
					if math.IsInf(number, -1) {
						input = nil
					}
					browserErr := c.SetParam("bass."+spec.Path, input)
					if (browserErr == nil) != want || *sent-before != btoi(want) {
						t.Fatalf("%s->%s %s case %d (%g): browser error=%v commands=%d, engine accepts=%v", lane, recipe, spec.Name, vi, value, browserErr, *sent-before, want)
					}
					previewRecipeRunning(t, browserEngine)
					if want {
						previewRecipeApplied(t, browserEngine, spec.ID, value)
					}
					nativeEngine, err := engine.New(cfg)
					if err != nil {
						t.Fatal(err)
					}
					native, err := liveplay.New(liveplay.Score{Engine: nativeEngine, SampleRate: 48_000, BPMMilli: cfg.BPMMilli, Tracks: []liveplay.TrackSlots{{ID: "bass", Kind: "drums"}}}, 48_000)
					if err != nil {
						t.Fatal(err)
					}
					_, nativeErr := native.SetTrackPreview("bass", spec.ID, value)
					if (nativeErr == nil) != want {
						t.Fatalf("%s->%s %s case %d: native error=%v, engine accepts=%v", lane, recipe, spec.Name, vi, nativeErr, want)
					}
					var pcm [256 * 8]byte
					_, renderErr := native.Read(pcm[:])
					actual, active := native.OverrideValue(0, spec.ID)
					running := nativeEngine.Playing()
					if want {
						previewRecipeApplied(t, nativeEngine, spec.ID, value)
					}
					native.Close()
					if renderErr != nil || !running || active != want || want && math.Float32bits(actual) != math.Float32bits(value) {
						t.Fatalf("%s->%s %s: native render=%v playing=%v override=%g/%v", lane, recipe, spec.Name, renderErr, running, actual, active)
					}
					if want {
						accepted++
					} else {
						rejected++
					}
				}
			}
		}
	}
	t.Logf("%d builtin lane/recipe mappings (including identity), %d mapping/parameter pairs, %d value cases per path: accepted %d, rejected %d; %d native/browser cases total", mappings, pairs, accepted+rejected, accepted, rejected, 2*(accepted+rejected))
}

func btoi(value bool) int {
	if value {
		return 1
	}
	return 0
}
