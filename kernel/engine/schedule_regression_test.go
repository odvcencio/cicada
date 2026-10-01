package engine

import (
	"reflect"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/seq"
)

func TestPlacementLoopKeepsExactTickOrigin(t *testing.T) {
	step, err := seq.PackStep(seq.Step{Note: 60, Velocity: 100, Gate: true, Ratchet: 1, Probability: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, block := range []int{1, 128, 4096} {
		cfg := Config{SampleRate: 48000, MaxBlock: block, Tracks: 1, MaxVoices: 1, BPMMilli: 120000, LoopSong: true}
		cfg.Track[0].Kind = VoiceAcid
		cfg.Patterns = []PatternBank{{}}
		cfg.Patterns[0].Slots[0] = seq.Pattern{Len: 1, GatePercent: 50, Steps: [64]uint32{step}}
		cfg.Schedule = []ScheduleEvent{{EndTick: 100, Kind: SchedulePattern, ID: 1}, {Tick: 100, EndTick: 100, Kind: SchedulePatternEnd, ID: 1}}
		e, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if !e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff}) {
			t.Fatal("play rejected")
		}
		clock, _ := seq.NewClock(cfg.SampleRate, cfg.BPMMilli)
		left, right := make([]float32, block), make([]float32, block)
		var got []int64
		var message cmd.Message
		for frame, end := int64(0), clock.SampleAtTick(501); frame < end; {
			n := int(min(int64(block), end-frame))
			e.Render(left[:n], right[:n])
			for e.Poll(&message) {
				if message.Kind == cmd.Fault {
					t.Fatal(message)
				}
				if message.Kind == cmd.NoteOn {
					got = append(got, message.Tick)
				}
			}
			frame += int64(n)
		}
		if want := []int64{0, 100, 200, 300, 400, 500}; !reflect.DeepEqual(got, want) {
			t.Errorf("block %d onsets=%v want=%v", block, got, want)
		}
	}
}

func TestSceneClipSeekMatchesOverlappingPlayback(t *testing.T) {
	for _, scenario := range []string{"overlap", "expired", "off", "previous-cycle"} {
		t.Run(scenario, func(t *testing.T) {
			cfg := clipScheduleConfig()
			cfg.BPMMilli, cfg.Schedule = 120000, nil
			pcm := make([]float32, 480000)
			for i := range pcm {
				pcm[i] = float32(i%127) / 1024
			}
			cfg.Assets[0].Left = pcm
			cfg.Clips = []ClipConfig{{EndFrame: int64(len(pcm)), FadeOutFrames: 48000}, {EndFrame: 48000}}
			cfg.Scenes = []Scene{{Track: [16]SceneBinding{{Mode: SceneClip}}}, {Track: [16]SceneBinding{{Mode: SceneClip}}}, {}}
			cfg.Song = []SongEntry{{Scene: 0, Bars: 1}, {Scene: 1, Bars: 1}, {Scene: 2, Bars: 1}}
			seek := int64(2*seq.TicksPerBar + 137)
			switch scenario {
			case "expired":
				cfg.Scenes[1].Track[0].Clip = 1
			case "off":
				cfg.Scenes[1].Track[0].Mode = SceneOff
			case "previous-cycle":
				cfg.LoopSong = true
				seek += 3 * seq.TicksPerBar
			}
			continuous, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			seeking, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			continuous.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
			var left, right [128]float32
			var message cmd.Message
			clock, _ := seq.NewClock(cfg.SampleRate, cfg.BPMMilli)
			for frame, end := int64(0), clock.SampleAtTick(seek); frame < end; {
				n := int(min(int64(len(left)), end-frame))
				continuous.Render(left[:n], right[:n])
				for continuous.Poll(&message) {
					if message.Kind == cmd.Fault {
						t.Fatal(message)
					}
				}
				frame += int64(n)
			}
			if !seeking.Push(cmd.Command{Op: cmd.OpSeek, Track: 0xff, Arg0: uint32(seek / seq.TicksPerBar), Arg1: uint32(seek % seq.TicksPerBar)}) || !seeking.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff}) {
				t.Fatal("seek/play rejected")
			}
			var want, got [128]TapFrame
			continuous.RenderWithTaps(left[:], right[:], want[:])
			seeking.RenderWithTaps(left[:], right[:], got[:])
			for seeking.Poll(&message) {
				if message.Kind == cmd.Fault {
					t.Fatal(message)
				}
			}
			for i := range want {
				if want[i].Tracks[0] != got[i].Tracks[0] {
					t.Fatalf("frame %d uninterrupted=%+v sought=%+v", i, want[i].Tracks[0], got[i].Tracks[0])
				}
			}
		})
	}
}

func TestPlacementLoopAndSceneClipSeekAllocationFree(t *testing.T) {
	for _, clips := range []bool{false, true} {
		cfg := clipScheduleConfig()
		cfg.BPMMilli, cfg.LoopSong = 120000, true
		if clips {
			cfg.Schedule = nil
			cfg.Assets[0].Left = make([]float32, 480000)
			cfg.Clips[0].EndFrame = 480000
			cfg.Scenes = []Scene{{Track: [16]SceneBinding{{Mode: SceneClip}}}, {Track: [16]SceneBinding{{Mode: SceneClip}}}, {}}
			cfg.Song = []SongEntry{{Bars: 1}, {Scene: 1, Bars: 1}, {Scene: 2, Bars: 1}}
		} else {
			cfg.Track[0].Kind = VoiceAcid
			step, err := seq.PackStep(seq.Step{Note: 60, Velocity: 100, Gate: true, Ratchet: 1, Probability: 100})
			if err != nil {
				t.Fatal(err)
			}
			cfg.Patterns = []PatternBank{{}}
			cfg.Patterns[0].Slots[0] = seq.Pattern{Len: 1, GatePercent: 50, Steps: [64]uint32{step}}
			cfg.Schedule = []ScheduleEvent{{EndTick: 100, Kind: SchedulePattern, ID: 1}, {Tick: 100, EndTick: 100, Kind: SchedulePatternEnd, ID: 1}}
		}
		e, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		var left, right [4096]float32
		var taps [4096]TapFrame
		var message cmd.Message
		allocs := testing.AllocsPerRun(100, func() {
			e.Push(cmd.Command{Op: cmd.OpSeek, Track: 0xff, Arg0: 5, Arg1: 137})
			e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
			e.RenderWithTaps(left[:], right[:], taps[:])
			for e.Poll(&message) {
			}
		})
		if allocs != 0 || e.faulted {
			t.Fatalf("clips=%v allocations=%g fault=%v", clips, allocs, e.faulted)
		}
		if clips {
			active := 0
			for _, voice := range e.clipVoices {
				if voice.active {
					active++
				}
			}
			if active != 3 {
				t.Fatalf("seek allocation test restored %d clips, want 3", active)
			}
		}
	}
}
