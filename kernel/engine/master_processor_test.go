package engine_test

import (
	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/fx/pro"
	"testing"
)

func TestPreparedMasterProcessorBypassAndAllocationFree(t *testing.T) {
	cfg := engine.Config{SampleRate: 48000, MaxBlock: 128, Tracks: 1, MaxVoices: 1, BPMMilli: 120000, Track: [16]engine.TrackConfig{{Kind: engine.VoiceAcid}}}
	dry, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	chain, err := pro.New(48000, pro.Params{})
	if err != nil {
		t.Fatal(err)
	}
	cfg.MasterProcessor = chain
	wet, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kernelimage.Encode(cfg); err == nil {
		t.Fatal("prepared processor silently omitted from image")
	}
	if wet.MasterProcessorLatencyFrames() != 0 {
		t.Fatal("bypass latency")
	}
	c := cmd.Command{Op: cmd.OpNoteOn, Track: 0, Arg0: 60 | 100<<8}
	dry.Push(c)
	wet.Push(c)
	var al, ar, bl, br [128]float32
	for i := 0; i < 16; i++ {
		dry.Render(al[:], ar[:])
		wet.Render(bl[:], br[:])
		if al != bl || ar != br {
			t.Fatal("bypass changes PCM")
		}
	}
	params, _ := pro.Preset("mastering")
	chain, err = pro.New(48000, params)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MasterProcessor = chain
	wet, err = engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if wet.MasterProcessorLatencyFrames() != chain.LatencyFrames() {
		t.Fatal("master latency")
	}
	allocs := testing.AllocsPerRun(100, func() {
		wet.Reset()
		wet.Push(c)
		for i := 0; i < 16; i++ {
			wet.Render(bl[:], br[:])
		}
	})
	if allocs != 0 {
		t.Fatalf("master allocs %g", allocs)
	}
	var message cmd.Message
	for wet.Poll(&message) {
		if message.Kind == cmd.Fault {
			t.Fatal(message)
		}
	}
}
