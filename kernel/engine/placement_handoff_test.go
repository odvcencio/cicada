package engine

import (
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/seq"
)

func handoffLoopConfig(t *testing.T) Config {
	t.Helper()
	pack := func(s seq.Step) uint32 {
		v, err := seq.PackStep(s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	cfg := Config{SampleRate: 48000, MaxBlock: 128, Tracks: 1, MaxVoices: 1, BPMMilli: 120000, LoopSong: true, Seed: 7}
	cfg.Track[0].Kind = VoiceAcid
	cfg.Patterns = []PatternBank{{}}
	pattern := seq.Pattern{Len: 16, GatePercent: 80, Seed: cfg.Seed}
	for i := range 16 {
		pattern.Steps[i] = pack(seq.Step{Ratchet: 1, Probability: 100})
	}
	pattern.Steps[0] = pack(seq.Step{Note: 48, Gate: true, Ratchet: 1, Probability: 100})
	pattern.Steps[15] = pack(seq.Step{Note: 55, Gate: true, Slide: true, Ratchet: 1, Probability: 100})
	cfg.Patterns[0].Slots[0] = pattern
	return cfg
}

// A looping arrangement that wraps from its last placement end to a placement
// at tick 0 on the same track hands off like a looping song of the same scene.
func TestPlacementLoopWrapHandsOffLikeSongLoop(t *testing.T) {
	song := handoffLoopConfig(t)
	song.Scenes = []Scene{{Track: [MaxTracks]SceneBinding{{Mode: SceneSlot}}}}
	song.Song = []SongEntry{{Scene: 0, Bars: 1}, {Scene: 0, Bars: 1}}
	placed := handoffLoopConfig(t)
	bar := int64(seq.TicksPerBar)
	placed.Schedule = []ScheduleEvent{
		{Kind: SchedulePattern, Tick: 0, EndTick: bar, ID: 1},
		{Kind: SchedulePatternEnd, Tick: bar, EndTick: bar, ID: 1},
		{Kind: SchedulePattern, Tick: bar, EndTick: 2 * bar, ID: 2},
		{Kind: SchedulePatternEnd, Tick: 2 * bar, EndTick: 2 * bar, ID: 2},
	}
	a, err := New(song)
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(placed)
	if err != nil {
		t.Fatal(err)
	}
	a.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	b.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	var al, ar, bl, br [128]float32
	var message cmd.Message
	for block := 0; block < 3*2*int(bar)*48000/(960*2)/128+1; block++ {
		a.Render(al[:], ar[:])
		b.Render(bl[:], br[:])
		if al != bl || ar != br {
			t.Fatalf("loop wrap differs from song loop at block %d", block)
		}
		for a.Poll(&message) {
		}
		for b.Poll(&message) {
		}
	}
}
