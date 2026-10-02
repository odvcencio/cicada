package liveplay

import (
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/project"
)

func TestMultiFileNativeCallbackAllocationFree(t *testing.T) {
	score, ds, err := project.LoadScore(filepath.Join("..", "..", "examples", "multifile", "main.cicada"), nil)
	if err != nil || score == nil || len(ds) != 0 {
		t.Fatalf("score: %v %+v", err, ds)
	}
	p, ds := project.FromScore(score)
	if p == nil {
		t.Fatalf("project: %+v", ds)
	}
	for _, rate := range []int{44100, 48000} {
		cfg, err := project.CompileEngine(p, rate, blockFrames)
		if err != nil {
			t.Fatal(err)
		}
		audio, err := engine.New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		player, err := New(Score{Engine: audio, SampleRate: rate, BPMMilli: int64(p.TempoMilli), Name: "multifile"}, rate)
		if err != nil {
			t.Fatal(err)
		}
		defer player.Close()
		var output [blockFrames * 8]byte
		var readErr error
		allocations := testing.AllocsPerRun(1000, func() { _, readErr = player.Read(output[:]) })
		if readErr != nil || allocations != 0 {
			t.Fatalf("native callback allocations=%g err=%v", allocations, readErr)
		}
		t.Logf("METRIC multifile rate=%d native_host_callback_allocs=0", rate)
	}
}
