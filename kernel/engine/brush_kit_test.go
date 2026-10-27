package engine

import (
	"math"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/kernel/voice/modeledkit"
)

func TestBrushPiecesFollowLiveNotesThroughTheEngine(t *testing.T) {
	for _, rate := range []int{44100, 48000, 96000} {
		newEngine := func() *Engine {
			cfg := Config{SampleRate: rate, MaxBlock: 128, Tracks: 1, MaxVoices: 2, BPMMilli: 120000, Seed: 73}
			cfg.Track[0] = TrackConfig{Kind: VoiceDrums, Kit: new([drum.LaneCount]KitLaneBinding)}
			cfg.Track[0].Kit[drum.SD] = KitLaneBinding{Kind: KitLaneModeled, Model: modeledkit.BrushTap, ModelParams: modeledkit.DefaultParams(), ModelLevelDB: -6}
			cfg.Track[0].Kit[drum.CP] = KitLaneBinding{Kind: KitLaneModeled, Model: modeledkit.BrushSweep, ModelParams: modeledkit.DefaultParams(), ModelLevelDB: -6}
			e, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			return e
		}
		// run renders a span in 128-frame blocks and returns the peak of the last block.
		run := func(e *Engine, frames int) (peak float32) {
			var l, r [128]float32
			for done := 0; done < frames; done += len(l) {
				e.Render(l[:], r[:])
				peak = 0
				for i := range l {
					peak = max(peak, float32(math.Abs(float64(l[i]))), float32(math.Abs(float64(r[i]))))
				}
			}
			return peak
		}
		noteOn := func(lane drum.Lane) cmd.Command {
			return cmd.Command{Op: cmd.OpNoteOn, Track: 0, Index: uint16(lane), Arg0: 100 << 8}
		}

		// A held key keeps the sweep going; releasing it ends the sweep within the 5 ms choke.
		gated, ungated := newEngine(), newEngine()
		gated.Push(noteOn(drum.CP))
		ungated.Push(noteOn(drum.CP))
		if run(gated, rate/2) < .005 || run(ungated, rate/2) < .005 {
			t.Fatalf("%d Hz: the sweep was silent while its key was held", rate)
		}
		gated.Push(cmd.Command{Op: cmd.OpNoteOff, Track: 0, Index: uint16(drum.CP)})
		run(gated, 1536)
		run(ungated, 1536)
		if peak := run(gated, 128); peak != 0 {
			t.Errorf("%d Hz: the sweep sounded %g after its key was released", rate, peak)
		}
		if peak := run(ungated, 128); peak < .005 {
			t.Errorf("%d Hz: the sweep ended on its own inside its hold", rate)
		}

		// A tap has no gate to wait for: it rings out by itself, and Reset clears it.
		tap := newEngine()
		tap.Push(noteOn(drum.SD))
		if run(tap, 256) < .005 {
			t.Fatalf("%d Hz: the tap was silent", rate)
		}
		if peak := run(tap, rate); peak != 0 {
			t.Errorf("%d Hz: the tap still sounded %g a second later", rate, peak)
		}
		var message cmd.Message
		for _, e := range []*Engine{gated, ungated, tap} {
			for e.Poll(&message) {
				if message.Kind == cmd.Fault {
					t.Fatalf("%d Hz: fault %+v", rate, message)
				}
			}
		}
		ungated.Reset()
		if peak := run(ungated, 128); peak != 0 {
			t.Errorf("%d Hz: Reset left the sweep sounding %g", rate, peak)
		}
	}
}
