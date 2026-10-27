package liveplay

import (
	"fmt"
	"math"
	"testing"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
)

// The follower observes the currently playing track after each callback period.
// Its targets include other automated amp lanes and the latest live override.
func TestMonitorAmpTargetsFollowsPlayingTrackWithoutCallbackAllocations(t *testing.T) {
	for _, period := range []int{64, 128, 256} {
		t.Run(fmt.Sprint(period), func(t *testing.T) {
			cfg := previewParameterConfig(t, engine.VoiceGraph)
			cfg.Automation = []cmd.Command{
				{Op: cmd.OpSetParam, Track: 0, Index: uint16(kernel.ParamAmpTone), Arg0: math.Float32bits(.8), Tick: 4},
				{Op: cmd.OpSetParam, Track: 0, Index: uint16(kernel.ParamAmpOutput), Arg0: math.Float32bits(-6), Tick: 8},
			}
			e, err := engine.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			p, err := New(Score{Engine: e, SampleRate: 48000, BPMMilli: 120000, Tracks: []TrackSlots{{ID: "bass"}}}, 48000)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			target, err := p.ResolvePreviewTrack("bass")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = p.SetResolvedPreview(target, kernel.ParamAmpDrive, .7); err != nil {
				t.Fatal(err)
			}
			pcm := make([]byte, period*8)
			toneSeen, outputSeen := 0, 0
			for i := 0; i < 8; i++ {
				if _, err = p.Read(pcm); err != nil {
					t.Fatal(err)
				}
				v, ok := p.MonitorAmpTargets(target)
				if !ok {
					t.Fatal("lost amp binding")
				}
				if toneSeen == 0 && float32(v.Tone) == .8 {
					toneSeen = (i + 1) * period
				}
				if outputSeen == 0 && float32(v.Output) == -6 {
					outputSeen = (i + 1) * period
				}
			}
			// At 48 kHz / 120 BPM, ticks 4 and 8 fall at frames 100 and 200.
			// The follower publishes each change at the next period boundary.
			if toneSeen < 100 || toneSeen > 100+period || outputSeen < 200 || outputSeen > 200+period {
				t.Fatalf("period %d: tone at %d, output at %d", period, toneSeen, outputSeen)
			}
			v, ok := p.MonitorAmpTargets(target)
			if !ok || float32(v.Drive) != .7 || float32(v.Tone) != .8 || float32(v.Output) != -6 || v.Bypass || v.Cabinet != 0 {
				t.Fatalf("targets=%+v valid=%v", v, ok)
			}
			p.loudness.close()
			if n := testing.AllocsPerRun(100, func() {
				if _, err := p.Read(pcm); err != nil {
					panic(err)
				}
				if _, ok := p.MonitorAmpTargets(target); !ok {
					panic("lost amp")
				}
			}); n != 0 {
				t.Fatalf("callback allocations %g", n)
			}
			if _, ok := p.MonitorAmpTargets(PreviewTarget{}); ok {
				t.Fatal("invalid binding accepted")
			}
			replacement := previewParameterScore(t, engine.VoiceGraph)
			replacement.Tracks[0].ID = "replacement"
			if err := p.Offer(replacement); err != nil {
				t.Fatal(err)
			}
			renderPreview(t, p, 100096)
			if _, ok := p.MonitorAmpTargets(target); ok {
				t.Fatal("removed track binding survived replacement")
			}
		})
	}
}

func TestMonitorAmpTargetsRejectsRemovedOrAmbiguousAmp(t *testing.T) {
	for _, count := range []int{0, 2} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			p, err := New(previewParameterScore(t, engine.VoiceGraph), 48000)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			target, err := p.ResolvePreviewTrack("bass")
			if err != nil {
				t.Fatal(err)
			}
			cfg := previewParameterConfig(t, engine.VoiceGraph)
			if count == 0 {
				cfg.Track[0].InsertChain = nil
			} else {
				cfg.Track[0].InsertChain = append(cfg.Track[0].InsertChain, cfg.Track[0].InsertChain[0])
			}
			next, err := engine.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if err = p.Offer(Score{Engine: next, SampleRate: 48000, BPMMilli: 120000, Tracks: []TrackSlots{{ID: "bass"}}}); err != nil {
				t.Fatal(err)
			}
			renderPreview(t, p, 100096)
			if _, ok := p.MonitorAmpTargets(target); ok {
				t.Fatalf("%d amp stages accepted", count)
			}
		})
	}
}

func TestMonitorAmpTargetsFollowsSameTrackAcrossReordering(t *testing.T) {
	makeScore := func(first, second string, firstDrive, secondDrive float64) Score {
		cfg := previewParameterConfig(t, engine.VoiceGraph)
		cfg.Tracks, cfg.MaxVoices = 2, 2
		cfg.Track[1] = cfg.Track[0]
		amp1 := *cfg.Track[0].InsertChain[0].AmpCab
		amp2 := amp1
		amp1.Drive, amp2.Drive = firstDrive, secondDrive
		cfg.Track[0].InsertChain = []engine.TrackEffectConfig{{Kind: engine.TrackEffectAmpCab, AmpCab: &amp1}}
		cfg.Track[1].InsertChain = []engine.TrackEffectConfig{{Kind: engine.TrackEffectAmpCab, AmpCab: &amp2}}
		e, err := engine.New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		return Score{Engine: e, SampleRate: 48000, BPMMilli: 120000, Tracks: []TrackSlots{{ID: first}, {ID: second}}}
	}
	p, err := New(makeScore("bass", "lead", .2, .9), 48000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	target, err := p.ResolvePreviewTrack("bass")
	if err != nil {
		t.Fatal(err)
	}
	if err = p.Offer(makeScore("lead", "bass", .9, .2)); err != nil {
		t.Fatal(err)
	}
	renderPreview(t, p, 100096)
	got, ok := p.MonitorAmpTargets(target)
	if !ok || got.Drive != .2 {
		t.Fatalf("reordered target = %+v, valid=%v", got, ok)
	}
}
