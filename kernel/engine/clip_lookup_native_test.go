//go:build !tinygo && !js

package engine

import (
	"fmt"
	"m31labs.dev/cicada/kernel/cmd"
	"math"
	"sync"
	"testing"
	"time"

	"m31labs.dev/cicada/kernel/voice/sample"
)

func clipLookupFixture(t testing.TB, tracks, capacity, active int) *Engine {
	return clipLookupFixtureAtRate(t, tracks, capacity, active, 48000, 48000)
}
func clipLookupFixtureAtRate(t testing.TB, tracks, capacity, active, outputRate, sourceRate int) *Engine {
	t.Helper()
	pcm := make([]float32, 512)
	for i := range pcm {
		pcm[i] = float32(i%127) / 4096
	}
	cfg := Config{SampleRate: outputRate, MaxBlock: 128, Tracks: tracks, MaxVoices: capacity, BPMMilli: 120000, Assets: []AudioAsset{{Left: pcm, SampleRate: sourceRate}}, Clips: []ClipConfig{{EndFrame: int64(len(pcm))}}}
	for track := 0; track < tracks; track++ {
		cfg.Track[track].Kind = VoiceAudio
	}
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	// Looping resident test voices keep lookup measurements independent of
	// finite file length, without resetting voices inside the timed interval.
	voice, err := sample.New(outputRate, sample.Region{Left: pcm, SampleRate: sourceRate, RootKey: 60, End: len(pcm), Loop: true, LoopEnd: len(pcm)})
	if err != nil {
		t.Fatal(err)
	}
	e.clipTemplates[0] = *voice
	e.clipSpecs[0].EndFrame = 1 << 30
	for i := 0; i < active; i++ {
		track := i % tracks
		if active == 1 {
			track = tracks - 1
		}
		e.startClip(track, 0, i, 0)
	}
	e.rawPlaying = true
	return e
}

func BenchmarkClipTrackLookup(b *testing.B) {
	for _, tc := range []struct {
		name                     string
		tracks, capacity, active int
	}{
		{"dense64", 64, 64, 64}, {"sparseCapacity64", 64, 256, 64}, {"sparseOne", 64, 256, 1}, {"overlap128", 64, 256, 128},
	} {
		b.Run(tc.name, func(b *testing.B) {
			e := clipLookupFixture(b, tc.tracks, tc.capacity, tc.active)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				for track := 0; track < tc.tracks; track++ {
					e.nextClipStereo(track)
				}
			}
		})
	}
}

func TestClipLookupFixtureMatchesAllocatedCapacity(t *testing.T) {
	for _, n := range []int{32, 64, 256} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			e := clipLookupFixture(t, 16, n, 16)
			if e.clipLimit != n {
				t.Fatalf("clipLimit%d want%d", e.clipLimit, n)
			}
		})
	}
}

// This reference retains the released capacity scan, independently of the new
// membership index, to catch summation order or slot reuse changes.
func scanClipReference(e *Engine, track int) (float32, float32) {
	if !e.rawPlaying {
		return 0, 0
	}
	var left, right float64
	for i := 0; i < e.clipLimit; i++ {
		v := &e.clipVoices[i]
		if v.track != track || !v.active {
			continue
		}
		c := e.clipSpecs[v.clip]
		gain := float64(1)
		if c.FadeInFrames > 0 {
			gain = min(gain, v.sourceFrame/float64(c.FadeInFrames))
		}
		if c.FadeOutFrames > 0 {
			gain = min(gain, (float64(c.EndFrame-c.StartFrame)-v.sourceFrame)/float64(c.FadeOutFrames))
		}
		gain = max(0, gain)
		l, r := v.voice.NextStereo()
		left += float64(l) * gain
		right += float64(r) * gain
		v.sourceFrame += v.ratio
		v.active = v.voice.Active() && v.sourceFrame < float64(c.EndFrame-c.StartFrame)
		if !v.active {
			e.finishClipVoice(v)
		}
	}
	return float32(left), float32(right)
}
func TestClipSlotIndexMatchesReleasedPCMThroughEndsReuseStopAndReset(t *testing.T) {
	indexed, reference := clipLookupFixture(t, 64, 256, 256), clipLookupFixture(t, 64, 256, 256)
	for _, e := range []*Engine{indexed, reference} {
		e.clipSpecs[0].EndFrame = 131
		e.clipSpecs[0].FadeInFrames = 13
		e.clipSpecs[0].FadeOutFrames = 17
	}
	for frame := 0; frame < 320; frame++ {
		for _, e := range []*Engine{indexed, reference} {
			switch frame {
			case 20:
				e.applyScheduleEvent(ScheduleEvent{Kind: ScheduleClipEnd, ID: 0}, 0)
				e.startClip(63, 0, 1000, 3)
			case 43:
				e.stopClips(17)
				e.startClip(3, 0, 1001, 9)
			case 65:
				e.resetClips()
				e.rawPlaying = true
				e.startClip(63, 0, 1002, 5)
				e.startClip(0, 0, 1003, 3)
			case 99:
				e.stopClips(-1)
				e.startClip(21, 0, 1004, 0)
			}
		}
		for track := 0; track < 64; track++ {
			l, r := indexed.nextClipStereo(track)
			wantL, wantR := scanClipReference(reference, track)
			if math.Float32bits(l) != math.Float32bits(wantL) || math.Float32bits(r) != math.Float32bits(wantR) {
				t.Fatalf("frame%d track%d indexed(%g,%g) scan(%g,%g)", frame, track, l, r, wantL, wantR)
			}
		}
		for slot, v := range indexed.clipVoices[:indexed.clipLimit] {
			for track := 0; track < 64; track++ {
				present := indexed.clipSlots[track][slot/64]&(uint64(1)<<(slot%64)) != 0
				if present != (v.active && v.track == track) {
					t.Fatalf("frame%d slot%d owner%d active%v wrong membership in track%d", frame, slot, v.track, v.active, track)
				}
			}
		}
	}
}
func TestClipSlotIndexSRCAndFadePCMMatchesReleasedScan(t *testing.T) {
	for _, rates := range [][2]int{{44100, 48000}, {48000, 96000}, {96000, 44100}} {
		t.Run(fmt.Sprintf("%dfrom%d", rates[0], rates[1]), func(t *testing.T) {
			indexed, reference := clipLookupFixtureAtRate(t, 16, 256, 128, rates[0], rates[1]), clipLookupFixtureAtRate(t, 16, 256, 128, rates[0], rates[1])
			for _, e := range []*Engine{indexed, reference} {
				e.clipSpecs[0].EndFrame = 337
				e.clipSpecs[0].FadeInFrames = 19
				e.clipSpecs[0].FadeOutFrames = 31
			}
			for frame := 0; frame < 800; frame++ {
				for track := 0; track < 16; track++ {
					l, r := indexed.nextClipStereo(track)
					wl, wr := scanClipReference(reference, track)
					if math.Float32bits(l) != math.Float32bits(wl) || math.Float32bits(r) != math.Float32bits(wr) {
						t.Fatalf("frame%d track%d indexed(%g,%g) scan(%g,%g)", frame, track, l, r, wl, wr)
					}
				}
			}
		})
	}
}

func TestClipSlotIndexParallelOwnersPruneNaturalAndScheduledEnds(t *testing.T) {
	e := clipLookupFixture(t, 64, 256, 256)
	e.clipSpecs[0].EndFrame = 73
	var owners sync.WaitGroup
	for lane := 0; lane < 4; lane++ {
		owners.Add(1)
		go func(lane int) {
			defer owners.Done()
			for frame := 0; frame < 100; frame++ {
				for track := lane; track < 64; track += 4 {
					e.nextClipStereo(track)
				}
			}
		}(lane)
	}
	owners.Wait()
	for track, mask := range e.clipSlots {
		for _, word := range mask {
			if word != 0 {
				t.Fatalf("ended track%d retained slots", track)
			}
		}
	}
	// A control-boundary reassignment must remove old, unpruned membership
	// before the newly owning track can run in parallel with the old owner.
	e.startClip(1, 0, 400, 0)
	e.applyScheduleEvent(ScheduleEvent{Kind: ScheduleClipEnd, ID: 400}, 0)
	e.startClip(63, 0, 401, 0)
	for lane := 0; lane < 4; lane++ {
		owners.Add(1)
		go func(lane int) {
			defer owners.Done()
			for frame := 0; frame < 100; frame++ {
				for track := lane; track < 64; track += 4 {
					e.nextClipStereo(track)
				}
			}
		}(lane)
	}
	owners.Wait()
}
func TestClipSlotIndexWholeRenderLoopsAndSeekWithoutAllocating(t *testing.T) {
	cfg := clipScheduleConfig()
	cfg.Tracks = 64
	cfg.MaxVoices = 256
	for track := 0; track < cfg.Tracks; track++ {
		cfg.Track[track].Kind = VoiceAudio
	}
	cfg.Clips[0].EndFrame = 101
	cfg.Schedule = nil
	cfg.LoopSong = true
	// A 1 s floor still faults a lane when the machine runs far more processes
	// than CPUs; the test asserts zero faults and zero allocations, not lane
	// latency, so the floor is only an upper bound.
	cfg.ParallelWaitFloor = 30 * time.Second
	for slot := 0; slot < 256; slot++ {
		cfg.Schedule = append(cfg.Schedule, ScheduleEvent{Tick: 0, EndTick: 240, Kind: ScheduleClip, Track: uint8(slot % 64), Index: 0, ID: uint32(slot + 1)})
	}
	for slot := 0; slot < 256; slot++ {
		cfg.Schedule = append(cfg.Schedule, ScheduleEvent{Tick: 240, EndTick: 240, Kind: ScheduleClipEnd, Track: uint8(slot % 64), ID: uint32(slot + 1)})
	}
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if err := e.EnableParallel(4); err != nil {
		t.Fatal(err)
	}
	e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255})
	var l, r [128]float32
	for block := 0; block < 180; block++ {
		if block == 80 {
			e.Push(cmd.Command{Op: cmd.OpSeek, Track: 255, Arg1: 11})
		}
		e.Render(l[:], r[:])
		var message cmd.Message
		for e.Poll(&message) {
			if message.Kind == cmd.Fault {
				t.Fatal(message)
			}
		}
	}
	if allocs := testing.AllocsPerRun(100, func() {
		e.Render(l[:], r[:])
		var message cmd.Message
		for e.Poll(&message) {
		}
	}); allocs != 0 {
		t.Fatalf("render lookup allocated %g", allocs)
	}
}
