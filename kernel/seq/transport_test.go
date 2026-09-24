package seq

import (
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
)

func TestTransportStopPlayAndSeek(t *testing.T) {
	transport, err := NewTransport(48_000, 120_000)
	if err != nil {
		t.Fatal(err)
	}
	transport.Play()
	start, end := transport.Advance(24_000)
	if start != 0 || end != PPQ {
		t.Fatalf("first beat: %d..%d", start, end)
	}
	transport.Stop()
	start, end = transport.Advance(48_000)
	if start != PPQ || end != PPQ {
		t.Fatalf("stopped tick moved: %d..%d", start, end)
	}
	transport.Play()
	start, end = transport.Advance(24_000)
	if start != PPQ || end != 2*PPQ {
		t.Fatalf("resume moved incorrectly: %d..%d", start, end)
	}
	if err := transport.SeekTick(4 * TicksPerBar); err != nil {
		t.Fatal(err)
	}
	start, end = transport.Advance(24_000)
	if start != 4*TicksPerBar || end != 4*TicksPerBar+PPQ {
		t.Fatalf("seek: %d..%d", start, end)
	}
}

func TestTempoChangeAtBarBoundary(t *testing.T) {
	transport, _ := NewTransport(48_000, 120_000)
	transport.Play()
	transport.Advance(24_000)
	if err := transport.QueueTempo(240_000); err != nil {
		t.Fatal(err)
	}
	if tempo, tick := transport.PendingTempo(); tempo != 240_000 || tick != TicksPerBar {
		t.Fatalf("wrong pending tempo %d at %d", tempo, tick)
	}
	start, end := transport.Advance(72_000)
	if start != PPQ || end != TicksPerBar || transport.BPMMilli() != 240_000 {
		t.Fatalf("boundary: %d..%d tempo=%d", start, end, transport.BPMMilli())
	}
	_, end = transport.Advance(12_000)
	if end != TicksPerBar+PPQ {
		t.Fatalf("new tempo beat ended at %d", end)
	}
}

func TestTempoAtAlreadyEnteredBoundaryDoesNotRewind(t *testing.T) {
	transport, _ := NewTransport(48_000, 120_000)
	transport.Play()
	transport.Advance(96_001)
	if transport.Tick() != TicksPerBar {
		t.Fatalf("expected bar boundary tick, got %d", transport.Tick())
	}
	if err := transport.QueueTempo(240_000); err != nil {
		t.Fatal(err)
	}
	_, end := transport.Advance(12)
	if end != TicksPerBar {
		t.Fatalf("tempo change rewound one sample and reached tick %d", end)
	}
	_, end = transport.Advance(1)
	if end != TicksPerBar+1 {
		t.Fatalf("new tempo did not advance from block start: %d", end)
	}
}

func TestQuantizeBoundaries(t *testing.T) {
	for _, tc := range []struct {
		q          cmd.Quantize
		tick, want int64
		length     uint8
	}{
		{0, 961, 961, 16},
		{1, 961, 1920, 16},
		{2, 3840, 3840, 16},
		{3, 3841, 7680, 16},
		{5, 3841, 7680, 16},
		{6, 3841, 7680, 16},
	} {
		got, err := QuantizeTick(tc.tick, tc.q, tc.length)
		if err != nil || got != tc.want {
			t.Fatalf("q=%d tick=%d: got %d, %v; want %d", tc.q, tc.tick, got, err, tc.want)
		}
	}
	if _, err := QuantizeTick(1, 4, 16); err == nil {
		t.Fatal("accepted reserved quantize")
	}
	if _, err := QuantizeTick(1, 3, 0); err == nil {
		t.Fatal("accepted empty pattern")
	}
}

func TestTransportAdvanceDoesNotAllocate(t *testing.T) {
	transport, _ := NewTransport(48_000, 120_000)
	transport.Play()
	if n := testing.AllocsPerRun(1000, func() { transport.Advance(128) }); n != 0 {
		t.Fatalf("transport allocated %v times", n)
	}
}
