package engine_test

import (
	"os"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestGuitarCallbackAllocationFree(t *testing.T) {
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
	for _, rate := range []int{44100, 48000} {
		cfg, err := project.CompileEngine(p, rate, 4096)
		if err != nil {
			t.Fatal(err)
		}
		cfg.LoopSong = true
		e, err := engine.New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		var left, right [4096]float32
		if !e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff}) {
			t.Fatal("play rejected")
		}
		allocs := testing.AllocsPerRun(100, func() {
			e.Render(left[:], right[:])
			var m cmd.Message
			for e.Poll(&m) {
				if m.Kind == cmd.Fault {
					panic("guitar callback fault")
				}
			}
		})
		if allocs != 0 {
			t.Fatalf("native callback at %d Hz: %g allocations/run", rate, allocs)
		}
		t.Logf("METRIC: native guitar callback allocations/run | %g | %d Hz, 4096 frames", allocs, rate)
		cfg.Track[0].Experimental = false
		if _, err := engine.New(cfg); err == nil {
			t.Fatal("engine accepted guitar without opt-in")
		}
		cfg.Track[0].Experimental = true
		cfg.Tracks = 2
		cfg.Track[1] = cfg.Track[0]
		cfg.Patterns = append(cfg.Patterns, cfg.Patterns[0])
		cfg.MaxVoices = 1
		if _, err := engine.New(cfg); err == nil {
			t.Fatal("engine ignored the voice budget")
		}
	}
}
