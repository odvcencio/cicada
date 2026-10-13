package engine

import (
	"testing"
	"time"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/seq"
)

// The engine admits a batch only while pending plus unread commands leave room
// for it (AvailableCommands), so 224 pending, 224 unread and a batch of 64 is the
// fullest state a host can reach: 512 slots in all.
//
// editQueueEngine returns an engine whose pending queue holds `pending` future
// step edits and whose command ring holds `ring` unread ones, all for track 0
// slot 0, plus a batch of `batch` valid edits for that slot.
func editQueueEngine(tb testing.TB, pending, ring, batch, slots int) (*Engine, []cmd.Command) {
	tb.Helper()
	cfg := Config{SampleRate: 48_000, MaxBlock: 128, Tracks: 1, MaxVoices: 4, BPMMilli: 120_000, Seed: 1}
	cfg.Track[0].Kind = VoiceAcid
	cfg.Patterns = []PatternBank{{}}
	for slot := 0; slot < 16; slot++ {
		cfg.Patterns[0].Slots[slot] = seq.Pattern{Len: 16, GatePercent: 55, Seed: 1}
	}
	e, err := New(cfg)
	if err != nil {
		tb.Fatal(err)
	}
	e.meterRate = 0
	edit := func(i int) cmd.Command {
		return cmd.Command{Op: cmd.OpSetStep, Track: 0, Index: uint16(i % 16), Arg1: uint32(i % slots), Arg0: packedNoteValue(uint8(48 + i%24)), Tick: int64(1_000_000 + i)}
	}
	var l, r [128]float32
	for queued := 0; queued < pending; {
		n := min(pending-queued, 128)
		for i := 0; i < n; i++ {
			if !e.Push(edit(queued + i)) {
				tb.Fatal("push rejected")
			}
		}
		e.Render(l[:], r[:]) // moves them into the pending queue
		queued += n
	}
	for i := 0; i < ring; i++ {
		if !e.Push(edit(pending + i)) {
			tb.Fatal("ring push rejected")
		}
	}
	cs := make([]cmd.Command, batch)
	for i := range cs {
		cs[i] = edit(pending + ring + i)
	}
	if e.pendingLen != pending || int(e.commandWrite-e.commandRead) != ring {
		tb.Fatalf("queues hold %d pending and %d unread, want %d and %d", e.pendingLen, e.commandWrite-e.commandRead, pending, ring)
	}
	return e, cs
}

func BenchmarkPatternEditsValidWorstCase(b *testing.B) {
	e, cs := editQueueEngine(b, 224, 224, 64, 1)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !e.PatternEditsValid(cs) {
			b.Fatal("worst-case batch was refused")
		}
	}
}

// The same queues spread over all sixteen slots: the batch touches sixteen keys,
// and each key scans the whole queue.
func BenchmarkPatternEditsValidSixteenSlots(b *testing.B) {
	e, cs := editQueueEngine(b, 224, 224, 64, 16)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !e.PatternEditsValid(cs) {
			b.Fatal("batch spread over sixteen slots was refused")
		}
	}
}

func BenchmarkPatternEditsValidTypical(b *testing.B) {
	e, cs := editQueueEngine(b, 0, 0, 8, 1)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !e.PatternEditsValid(cs) {
			b.Fatal("typical batch was refused")
		}
	}
}

// The dry run shares the render thread with 0.67 ms blocks, so a full queue and
// a 64-edit batch must stay a small fraction of one. The bound has headroom for
// a loaded machine; the smallest of several runs is compared.
func TestPatternEditsValidTimingBudget(t *testing.T) {
	const budget = 100 * time.Microsecond
	for name, slots := range map[string]int{"one slot": 1, "sixteen slots": 16} {
		e, cs := editQueueEngine(t, 224, 224, 64, slots)
		best := time.Hour
		for run := 0; run < 7; run++ {
			start := time.Now()
			for i := 0; i < 20; i++ {
				if !e.PatternEditsValid(cs) {
					t.Fatalf("%s: worst-case batch was refused", name)
				}
			}
			best = min(best, time.Since(start)/20)
		}
		if best > budget {
			t.Fatalf("%s: worst-case dry run takes %v per batch, budget %v", name, best, budget)
		}
		t.Logf("%s: worst-case dry run %v per batch", name, best)
	}
}
