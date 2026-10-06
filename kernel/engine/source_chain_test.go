package engine

import (
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/seq"
	"testing"
)

func sourceChainConfig(t *testing.T) Config {
	cfg := testConfig()
	cfg.Tracks = 1
	cfg.MaxVoices = 1
	bank := PatternBank{Chain: []uint8{0, 1, 2}}
	for i, spec := range []struct {
		length uint8
		grid   uint16
		note   uint8
	}{{1, 0, 45}, {3, 320, 52}, {2, 480, 57}} {
		p := seq.Pattern{Len: spec.length, StepTicks: spec.grid, GatePercent: 55}
		for step := uint8(0); step < p.Len; step++ {
			p.Steps[step] = packedNote(t, spec.note)
		}
		bank.Slots[i] = p
	}
	cfg.Patterns = []PatternBank{bank}
	cfg.Scenes = []Scene{{}}
	cfg.Song = []SongEntry{{Scene: 0, Bars: 2}}
	return cfg
}

func TestSourceChainExactTickPhaseSeekAndAllocationFree(t *testing.T) {
	cfg := sourceChainConfig(t)
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.meterRate = 0
	e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255})
	var left, right [128]float32
	var notes []cmd.Message
	for block := 0; block < 520; block++ {
		e.Render(left[:], right[:])
		var m cmd.Message
		for e.Poll(&m) {
			if m.Kind == cmd.Fault {
				t.Fatalf("fault %+v", m)
			}
			if m.Kind == cmd.NoteOn {
				notes = append(notes, m)
			}
		}
	}
	for i, want := range []int64{0, 240, 560, 880, 1200, 1680, 2160, 2400} {
		if i >= len(notes) || notes[i].Tick != want {
			t.Fatalf("notes: %+v", notes)
		}
	}
	e.Push(cmd.Command{Op: cmd.OpSeek, Track: 255, Arg1: 560})
	e.Render(left[:], right[:])
	var messages []cmd.Message
	var m cmd.Message
	for e.Poll(&m) {
		if m.Kind == cmd.NoteOn {
			messages = append(messages, m)
		}
	}
	if len(messages) != 1 || messages[0].Tick != 560 {
		t.Fatalf("seek phase: %+v", messages)
	}
	if allocs := testing.AllocsPerRun(1000, func() {
		e.Render(left[:], right[:])
		for e.Poll(&m) {
		}
	}); allocs != 0 {
		t.Fatalf("%g allocations", allocs)
	}
}
