package engine

import (
	"runtime"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
)

func TestSampleAndClipFirstCallbackDoesNotAllocate(t *testing.T) {
	cfg := mixedOwnerConfig()
	cfg.Tracks = 3
	cfg.MaxVoices = 8
	cfg.Patterns = append(cfg.Patterns, PatternBank{})
	cfg.Track[2] = TrackConfig{Kind: VoiceAudio, GainSet: true}
	pcm := make([]float32, 512)
	for i := range pcm {
		pcm[i] = float32(i%31) / 1024
	}
	cfg.Assets = []AudioAsset{{Left: pcm, SampleRate: 44100}}
	cfg.Clips = []ClipConfig{{EndFrame: int64(len(cfg.Assets[0].Left))}}
	cfg.Schedule = append([]ScheduleEvent{{Kind: ScheduleClip, Track: 2, Tick: 0, EndTick: 960, ID: 3}}, cfg.Schedule...)
	cfg.Schedule = append(cfg.Schedule, ScheduleEvent{Kind: ScheduleClipEnd, Track: 2, Tick: 960, EndTick: 960, ID: 3})
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255}) {
		t.Fatal("play rejected")
	}
	var left, right [128]float32
	// MemStats counts allocations from every goroutine. Use one processor,
	// as AllocsPerRun does, so GC workers cannot overlap the first callback,
	// and finish setup's GC work first. The voices stay cold: Render has not
	// run yet.
	previousProcs := runtime.GOMAXPROCS(1)
	t.Cleanup(func() { runtime.GOMAXPROCS(previousProcs) })
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	e.Render(left[:], right[:])
	runtime.ReadMemStats(&after)
	if after.Mallocs != before.Mallocs {
		t.Fatalf("first graph/sampler/clip callback allocated %d times", after.Mallocs-before.Mallocs)
	}
	var message cmd.Message
	for e.Poll(&message) {
		if message.Kind == cmd.Fault {
			t.Fatal(message)
		}
	}
	if e.faulted || e.voices[1].sampler.ActiveVoices() == 0 || e.clipVoices[0].active == false {
		t.Fatal("first callback did not start the sampler and clip")
	}
}
