package engine

import (
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/seq"
)

func testConfig() Config {
	cfg := Config{SampleRate: 48_000, MaxBlock: 128, Tracks: 2, MaxVoices: 7, BPMMilli: 120_000, Seed: 4242}
	cfg.Track[0].Kind = VoiceAcid
	cfg.Track[1].Kind = VoiceDrums
	return cfg
}

func TestLiveAcidAndDrumsRenderDeterministically(t *testing.T) {
	first, err := New(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	commands := []cmd.Command{
		{Op: cmd.OpNoteOn, Track: 0, Arg0: 45 | 100<<8},
		{Op: cmd.OpNoteOn, Track: 1, Index: 0, Arg0: 36 | 110<<8 | 1<<16},
	}
	for _, c := range commands {
		if !first.Push(c) || !second.Push(c) {
			t.Fatal("command rejected")
		}
	}
	var aL, aR, bL, bR [128]float32
	energy := float64(0)
	for block := 0; block < 20; block++ {
		first.Render(aL[:], aR[:])
		second.Render(bL[:], bR[:])
		for i := range aL {
			if aL[i] != bL[i] || aR[i] != bR[i] {
				t.Fatalf("render differs at block %d frame %d", block, i)
			}
			energy += float64(aL[i]*aL[i] + aR[i]*aR[i])
		}
	}
	if energy < 1e-7 {
		t.Fatal("live engine was silent")
	}
}

func TestFutureTickCommandIsSampleScheduled(t *testing.T) {
	cfg := testConfig()
	cfg.Tracks = 1
	cfg.MaxVoices = 1
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff}) || !e.Push(cmd.Command{Op: cmd.OpNoteOn, Track: 0, Arg0: 45 | 100<<8, Tick: seq.PPQ}) {
		t.Fatal("command rejected")
	}
	var left, right [128]float32
	for block := 0; block < 150; block++ {
		e.Render(left[:], right[:])
		for _, sample := range left {
			if sample != 0 {
				t.Fatal("future note sounded before its tick")
			}
		}
	}
	energy := float32(0)
	for block := 0; block < 60; block++ {
		e.Render(left[:], right[:])
		for _, sample := range left {
			energy += sample * sample
		}
	}
	if energy == 0 {
		t.Fatal("future note never sounded")
	}
}

func TestUnsupportedCommandFaultsAndResetRecovers(t *testing.T) {
	cfg := testConfig()
	cfg.Tracks = 1
	cfg.MaxVoices = 1
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !e.Push(cmd.Command{Op: cmd.OpSetPatternLen, Track: 0, Index: 4, Arg1: 0}) {
		t.Fatal("valid wire command rejected")
	}
	var left, right [128]float32
	e.Render(left[:], right[:])
	var message cmd.Message
	if !e.Poll(&message) || message.Kind != cmd.Fault {
		t.Fatalf("missing unsupported-command fault: %+v", message)
	}
	e.Reset()
	if !e.Push(cmd.Command{Op: cmd.OpNoteOn, Track: 0, Arg0: 45 | 100<<8}) {
		t.Fatal("reset engine rejected note")
	}
	energy := float32(0)
	for block := 0; block < 4; block++ {
		e.Render(left[:], right[:])
		for _, sample := range left {
			energy += sample * sample
		}
	}
	if energy == 0 {
		t.Fatal("reset engine remained silent")
	}
}

func TestRenderDoesNotAllocate(t *testing.T) {
	e, err := New(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	e.meterRate = 0
	if !e.Push(cmd.Command{Op: cmd.OpNoteOn, Track: 0, Arg0: 45 | 100<<8}) {
		t.Fatal("note rejected")
	}
	var left, right [128]float32
	allocs := testing.AllocsPerRun(1000, func() { e.Render(left[:], right[:]) })
	if allocs != 0 {
		t.Fatalf("Engine.Render allocated %.2f objects", allocs)
	}
}

func BenchmarkRender128(b *testing.B) {
	e, err := New(testConfig())
	if err != nil {
		b.Fatal(err)
	}
	e.meterRate = 0
	if !e.Push(cmd.Command{Op: cmd.OpNoteOn, Track: 0, Arg0: 45 | 100<<8}) || !e.Push(cmd.Command{Op: cmd.OpNoteOn, Track: 1, Index: 0, Arg0: 36 | 110<<8}) {
		b.Fatal("note rejected")
	}
	var left, right [128]float32
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		e.Render(left[:], right[:])
	}
}
