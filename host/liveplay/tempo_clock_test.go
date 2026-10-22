package liveplay

import (
	"fmt"
	"io"
	"testing"
	"time"

	"m31labs.dev/cicada/host/midi"
	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/musictime"
)

func TestRenderedTempoForecastMatchesTheAudioClock(t *testing.T) {
	for _, mode := range []string{"song", "arrangement"} {
		for _, rate := range []int{44100, 48000, 96000} {
			for _, tempos := range [][2]int64{{120000, 60000}, {60000, 240000}, {137333, 83333}, {20000, 300000}, {300000, 20000}} {
				t.Run(fmt.Sprintf("%s/%d/%d-to-%d", mode, rate, tempos[0], tempos[1]), func(t *testing.T) {
					cfg := engine.Config{SampleRate: rate, MaxBlock: blockFrames, Tracks: 1, MaxVoices: 1, BPMMilli: tempos[0], LoopSong: true}
					cfg.Track[0].Kind = engine.VoiceAcid
					if mode == "song" {
						cfg.Scenes = []engine.Scene{{}, {Settings: []engine.SceneSetting{{Track: 0xff, ID: kernel.ParamGlobalTempo, Value: float32(tempos[1]) / 1000}}}}
						cfg.Song = []engine.SongEntry{{Scene: 0, Bars: 1}, {Scene: 1, Bars: 2}}
					} else {
						cfg.Patterns = []engine.PatternBank{{}}
						cfg.Patterns[0].Slots[0] = seq.Pattern{Len: 16, GatePercent: 50}
						cfg.Schedule = []engine.ScheduleEvent{
							{Kind: engine.SchedulePattern, EndTick: 11520, ID: 1},
							{Kind: engine.ScheduleTempo, Tick: 3840, EndTick: 3840, ID: uint32(tempos[1])},
							{Kind: engine.SchedulePatternEnd, Tick: 11520, EndTick: 11520, ID: 1},
						}
					}
					created, err := engine.New(cfg)
					if err != nil {
						t.Fatal(err)
					}
					p, err := New(Score{Engine: created, SampleRate: rate, BPMMilli: tempos[0]}, rate)
					if err != nil {
						t.Fatal(err)
					}
					defer p.Close()
					start := int64(3840) - tempos[0]*seq.PPQ/600000 // About 100 ms before the boundary.
					if err := p.PlayRange(PlaybackRange{StartTick: start, EndTick: 7680, Loop: true}); err != nil {
						t.Fatal(err)
					}
					if _, err := io.CopyN(io.Discard, p, 64*8); err != nil {
						t.Fatal(err)
					}
					pub := NewPublisher()
					p.SetPublisher(pub)
					p.PublishTelemetry()
					f, step, ok := pub.TransportClock()
					tick := (int64(f.Bar)-1)*3840 + (int64(f.Beat)-1)*960 + int64(f.Tick)
					if !ok || !step.Valid(tick, int64(f.TempoMilli)) || step.BPMMilli != tempos[1] {
						t.Fatalf("forecast missing or detached: %+v / %+v", f, step)
					}
					old, _ := seq.NewClock(rate, tempos[0])
					old.AnchorTick = start
					after, err := old.Reanchor(3840, tempos[1])
					if err != nil {
						t.Fatal(err)
					}
					var timeline midi.Timeline
					const origin = int64(time.Second)
					timeline.Reset(origin)
					timeline.ObserveClock(origin, tick, int64(f.TempoMilli), step)
					eventAt := origin + int64(200*time.Millisecond)
					want := after.TickAtSample(f.SampleFrame + int64(rate)/5)
					got := timeline.PositionAt(eventAt, true)
					if !got.Valid || got.Tick-want < -1 || got.Tick-want > 1 {
						t.Fatalf("input tick %+v differs from rendered clock %d", got, want)
					}
					if _, err := io.CopyN(io.Discard, p, int64(rate/5)*8); err != nil {
						t.Fatal(err)
					}
					p.PublishTelemetry()
					next, _, _ := pub.TransportClock()
					actual := (int64(next.Bar)-1)*3840 + (int64(next.Beat)-1)*960 + int64(next.Tick)
					if actual != after.TickAtSample(next.SampleFrame) || next.TempoMilli != uint32(tempos[1]) {
						t.Fatal("renderer and independent stepped clock differ", next, actual, after.TickAtSample(next.SampleFrame))
					}
				})
			}
		}
	}
}

func TestPublisherTempoMetadataIsOneCohort(t *testing.T) {
	pub := NewPublisher()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := int64(1); i <= 1000; i++ {
			pub.PublishTransportClock(TransportFrame{SampleFrame: i}, musictime.ObservedTempoStep{Tick: i})
		}
	}()
	for i := 0; i < 1000; i++ {
		frame, step, ok := pub.TransportClock()
		if ok && frame.SampleFrame != step.Tick {
			t.Fatal("mixed transport metadata")
		}
		frames, next := pub.LatestClock()
		if frames[FrameTransport] != nil {
			f, err := DecodeTransportFrame(frames[FrameTransport])
			if err != nil || f.SampleFrame != next.Tick {
				t.Fatal("mixed projection metadata")
			}
		}
	}
	<-done
	pub.PublishTransport(TransportFrame{SampleFrame: 2000})
	if _, step, _ := pub.TransportClock(); step != (musictime.ObservedTempoStep{}) {
		t.Fatal("legacy publication retained old forecast")
	}
}
