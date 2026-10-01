package engine

import (
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/seq"
	"testing"
)

func clipScheduleConfig() Config {
	pcm := make([]float32, 48000)
	for i := range pcm {
		pcm[i] = float32(i%127) / 1024
	}
	cfg := Config{SampleRate: 48000, MaxBlock: 4096, Tracks: 1, MaxVoices: 32, BPMMilli: 137123}
	cfg.Track[0] = TrackConfig{Kind: VoiceAudio, GainSet: true}
	cfg.Assets = []AudioAsset{{Left: pcm, SampleRate: 48000}}
	cfg.Clips = []ClipConfig{{EndFrame: int64(len(pcm))}}
	cfg.Schedule = []ScheduleEvent{
		{Tick: 240, EndTick: 1920, Kind: ScheduleClip, Index: 0, ID: 1},
		{Tick: 480, EndTick: 2160, Kind: ScheduleClip, Index: 0, ID: 2},
		{Tick: 1920, EndTick: 1920, Kind: ScheduleClipEnd, ID: 1},
		{Tick: 2160, EndTick: 2160, Kind: ScheduleClipEnd, ID: 2},
	}
	return cfg
}

func TestClipScheduleBoundariesOverlapSeekAndBlockIndependence(t *testing.T) {
	cfg := clipScheduleConfig()
	a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	a.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	b.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	var al, ar, bl, br [4096]float32
	var message cmd.Message
	for block := 0; block < 32; block++ {
		a.Render(al[:], ar[:])
		for off := 0; off < len(bl); off += 128 {
			b.Render(bl[off:off+128], br[off:off+128])
			for b.Poll(&message) {
				if message.Kind == cmd.Fault {
					t.Fatal(message)
				}
			}
		}
		if al != bl || ar != br || a.faulted || b.faulted {
			t.Fatalf("clip output depends on block size at %d", block)
		}
		for a.Poll(&message) {
		}
	}
	// Seek uses elapsed musical time rather than the transport's reanchored
	// sample clock (which clamps earlier ticks to the new anchor).
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.Push(cmd.Command{Op: cmd.OpSeek, Track: 0xff, Arg1: 960})
	c.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	var l, r [1]float32
	c.Render(l[:], r[:])
	clock, _ := seq.NewClock(48000, cfg.BPMMilli)
	for i, start := range []int64{240, 480} {
		want := clock.SampleAtTick(960) - clock.SampleAtTick(start) + 1
		if !c.clipVoices[i].active || c.clipVoices[i].sourceFrame != float64(want) {
			t.Fatalf("clip %d source frame=%g want=%d", i, c.clipVoices[i].sourceFrame, want)
		}
	}
}

func TestClipScheduleRenderSeekAndTapsAllocationFree(t *testing.T) {
	cfg := clipScheduleConfig()
	cfg.LoopSong = true
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var left, right [4096]float32
	var taps [4096]TapFrame
	var message cmd.Message
	allocs := testing.AllocsPerRun(100, func() {
		e.Push(cmd.Command{Op: cmd.OpSeek, Track: 0xff, Arg1: 1920})
		e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
		e.RenderWithTaps(left[:], right[:], taps[:])
		for e.Poll(&message) {
		}
	})
	if allocs != 0 || e.faulted {
		t.Fatalf("allocations=%g fault=%v", allocs, e.faulted)
	}
}

func TestSceneClipStartsOnceAndSeekRestoresKeep(t *testing.T) {
	cfg := clipScheduleConfig()
	cfg.BPMMilli = 120000
	cfg.Schedule = nil
	cfg.Scenes = []Scene{{Track: [16]SceneBinding{{Mode: SceneClip}}}, {}}
	cfg.Song = []SongEntry{{Scene: 0, Bars: 4}, {Scene: 1, Bars: 1}}
	// An asset long enough to remain active across scene entry.
	cfg.Assets[0].Left = make([]float32, 480000)
	cfg.Clips[0].EndFrame = 480000
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.Push(cmd.Command{Op: cmd.OpSeek, Track: 0xff, Arg0: 4, Arg1: 960})
	e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	var l, r [1]float32
	e.Render(l[:], r[:])
	if !e.clipVoices[0].active || e.clipVoices[0].sourceFrame != 408001 {
		t.Fatalf("kept clip seek restarted source: %+v", e.clipVoices[0])
	}
	for i := 1; i < len(e.clipVoices); i++ {
		if e.clipVoices[i].active {
			t.Fatal("scene duplicated clip")
		}
	}
}

func BenchmarkScheduleSeek10000(b *testing.B) {
	cfg := clipScheduleConfig()
	cfg.Schedule = nil
	cfg.LoopSong = true
	for i := 0; i < 10000; i++ {
		tick := int64(i) * 480
		id := uint32(i + 1)
		cfg.Schedule = append(cfg.Schedule, ScheduleEvent{Tick: tick, EndTick: tick + 240, Kind: ScheduleClip, ID: id}, ScheduleEvent{Tick: tick + 240, EndTick: tick + 240, Kind: ScheduleClipEnd, ID: id})
	}
	e, err := New(cfg)
	if err != nil {
		b.Fatal(err)
	}
	var l, r [128]float32
	var message cmd.Message
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e.Push(cmd.Command{Op: cmd.OpSeek, Track: 0xff, Arg1: 4799640})
		e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
		e.Render(l[:], r[:])
		for e.Poll(&message) {
		}
	}
}
