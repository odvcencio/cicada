package engine

import (
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/fx"
)

func TestEffectSendRenderAllocationFree(t *testing.T) {
	cfg := testConfig()
	delay := fx.DefaultDelayParams()
	reverb := fx.DefaultReverbParams()
	cfg.DelayA, cfg.ReverbB = &delay, &reverb
	cfg.Track[0].SendA, cfg.Track[0].SendB = .4, .4
	cfg.Track[1].SendB = .3
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !e.Push(cmd.Command{Op: cmd.OpNoteOn, Track: 0, Arg0: 45 | 100<<8}) {
		t.Fatal("note rejected")
	}
	var left, right [128]float32
	if count := testing.AllocsPerRun(100, func() { e.Render(left[:], right[:]) }); count != 0 {
		t.Fatalf("engine with both effect returns allocated %g times per block", count)
	}
	if e.faulted {
		t.Fatal("effect returns faulted the engine")
	}
}
