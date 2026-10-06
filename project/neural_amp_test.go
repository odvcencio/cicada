package project

import (
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
	"os"
	"testing"
)

func TestNeuralAmpEngineRenderAllocationFree(t *testing.T) {
	source, err := os.ReadFile("../examples/neural-amp.cicada")
	if err != nil {
		t.Fatal(err)
	}
	score, ds := notation.Parse(source)
	if score == nil || len(ds) != 0 {
		t.Fatalf("parse: %+v", ds)
	}
	project, ds := FromScore(score)
	if project == nil {
		t.Fatalf("project: %+v", ds)
	}
	cfg, err := CompileEngine(project, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	renderer, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !renderer.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff}) {
		t.Fatal("play rejected")
	}
	var left, right [128]float32
	for i := 0; i < 64; i++ {
		renderer.Render(left[:], right[:])
	}
	if n := testing.AllocsPerRun(100, func() { renderer.Render(left[:], right[:]) }); n != 0 {
		t.Fatalf("neural demo render allocated %g objects", n)
	}
	var sounded bool
	for _, x := range left {
		sounded = sounded || x != 0
	}
	if !sounded {
		t.Fatal("neural demo rendered silence")
	}
}
