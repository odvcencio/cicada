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

// bestDryRun is the smallest of several timings of one PatternEditsValid call.
func bestDryRun(t *testing.T, pending, ring, batch, slots int) time.Duration {
	t.Helper()
	e, cs := editQueueEngine(t, pending, ring, batch, slots)
	reps := 4_000 / (1 + (pending+ring)/8) // keep each timed run near a millisecond
	reps = max(reps, 4)
	best := time.Hour
	for run := 0; run < 9; run++ {
		start := time.Now()
		for i := 0; i < reps; i++ {
			if !e.PatternEditsValid(cs) {
				t.Fatal("batch was refused")
			}
		}
		best = min(best, time.Since(start)/time.Duration(reps))
	}
	return best
}

// The dry run shares the render thread with 0.67 ms blocks. Wall-clock limits do
// not hold across machines, so this compares costs measured in the same run: a
// full queue and a 64-edit batch must cost a bounded multiple of a small batch,
// and spreading the batch over sixteen slots a small multiple of one slot. The
// earlier quadratic scan cost about 175 times the typical batch; the current one
// about 10 times.
func TestPatternEditsValidScalesWithQueueNotItsProduct(t *testing.T) {
	typical := bestDryRun(t, 0, 0, 8, 1)
	one := bestDryRun(t, 224, 224, 64, 1)
	sixteen := bestDryRun(t, 224, 224, 64, 16)
	t.Logf("typical %v, worst case %v (%.1fx), sixteen slots %v (%.1fx the worst case)", typical, one, float64(one)/float64(typical), sixteen, float64(sixteen)/float64(one))
	if one > 35*typical {
		t.Fatalf("worst case %v is %.0fx the typical %v; limit 35x (the quadratic scan was about 175x)", one, float64(one)/float64(typical), typical)
	}
	if sixteen > 4*one {
		t.Fatalf("sixteen slots cost %v, %.1fx the one-slot worst case %v; limit 4x", sixteen, float64(sixteen)/float64(one), one)
	}
}
