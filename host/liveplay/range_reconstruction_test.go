package liveplay

import (
	"math"
	"testing"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/seq"
)

func rangeGainCheck(t *testing.T, p *Player, want float64) {
	t.Helper()
	checkScenePatchOrdering(t, p, want)
}

// previewRenderScore plays scene "later" (-9 dB) in bar two and leaves the
// other bars at -6 dB. A listening range that loops over bars one and two
// restarts at -6 dB: the engine reconstructs it, so the committed snapshot
// must too, or cancelling a preview would restore the stale -9 dB.
func TestListeningLoopRestartReconstructsCommittedValues(t *testing.T) {
	p, err := New(previewRenderScore(t, -6), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.PlayRange(PlaybackRange{StartTick: 0, EndTick: 2 * seq.TicksPerBar, Loop: true}); err != nil {
		t.Fatal(err)
	}
	lastBlockPeak(t, p, 2*previewBarBlocks+8) // Past the loop restart.
	if p.RangeCycles() != 1 {
		t.Fatalf("range cycles %d, want one restart", p.RangeCycles())
	}
	rangeGainCheck(t, p, -6)
}

func TestListeningRangeSeekReconstructsCommittedValues(t *testing.T) {
	p, err := New(previewRenderScore(t, -6), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.PlayRange(PlaybackRange{StartTick: seq.TicksPerBar, EndTick: 2 * seq.TicksPerBar}); err != nil {
		t.Fatal(err)
	}
	lastBlockPeak(t, p, 8)
	if value, ok := p.CommittedValue(0, kernel.ParamMixGain); !ok || value != -9 {
		t.Fatalf("range inside the second scene committed %g/%v, want -9", value, ok)
	}
	if err := p.PlayRange(PlaybackRange{StartTick: 0, EndTick: seq.TicksPerBar}); err != nil {
		t.Fatal(err)
	}
	lastBlockPeak(t, p, 8)
	rangeGainCheck(t, p, -6)
}

// A prepared score offered with a listening range lands at the loop restart.
// Its edit event must carry the offered score's revision so hosts can settle
// the matching publication, and its committed values come from the new engine.
func TestListeningRangeReplacementEmitsRevisionAndReconstructs(t *testing.T) {
	p, err := New(previewRenderScore(t, -6), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.PlayRange(PlaybackRange{StartTick: 0, EndTick: seq.TicksPerBar / 2, Loop: true}); err != nil {
		t.Fatal(err)
	}
	lastBlockPeak(t, p, 4)
	next := previewRenderScore(t, -3)
	next.Revision = "p7"
	next.Range = &PlaybackRange{StartTick: seq.TicksPerBar, EndTick: seq.TicksPerBar + seq.TicksPerBar/2, Loop: true}
	if err := p.Offer(next); err != nil {
		t.Fatal(err)
	}
	lastBlockPeak(t, p, previewBarBlocks/2) // Reaches the old range end; no bar boundary comes first.
	var landed Event
	for drained := false; !drained; {
		select {
		case event := <-p.Events():
			if event.Kind == "edit" {
				landed = event
			}
		default:
			drained = true
		}
	}
	if landed.Revision != "p7" {
		t.Fatalf("range replacement event revision %q, want p7", landed.Revision)
	}
	// The replacement restarts inside its second scene.
	if value, ok := p.CommittedValue(0, kernel.ParamMixGain); !ok || math.Abs(float64(value)+9) > 1e-6 {
		t.Fatalf("replacement committed %g/%v, want -9", value, ok)
	}
	rangeGainCheck(t, p, -9)
}

// The song's own loop wrap launches scene one again without a seek. The engine
// keeps the earlier scene's value and so must the committed snapshot.
func TestNaturalSongWrapKeepsEngineAndCommittedValuesTogether(t *testing.T) {
	p, err := New(previewRenderScore(t, -6), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	lastBlockPeak(t, p, 3*previewBarBlocks+64)
	rangeGainCheck(t, p, -9)
}
