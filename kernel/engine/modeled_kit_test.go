package engine

import (
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/kernel/voice/modeledkit"
)

func TestFullModeledKitLiveBudgetAndAllocations(t *testing.T) {
	for _, rate := range []int{44100, 48000, 96000} {
		cfg := Config{SampleRate: rate, MaxBlock: 128, Tracks: 2, MaxVoices: int(modeledkit.ProfileCount), BPMMilli: 120000, Seed: 73}
		for track := 0; track < 2; track++ {
			cfg.Track[track] = TrackConfig{Kind: VoiceDrums, Kit: new([drum.LaneCount]KitLaneBinding)}
		}
		for p := modeledkit.Profile(0); p < modeledkit.ProfileCount; p++ {
			cfg.Track[int(p)/int(drum.LaneCount)].Kit[int(p)%int(drum.LaneCount)] = KitLaneBinding{Kind: KitLaneModeled, Model: p, ModelParams: modeledkit.DefaultParams(), ModelLevelDB: -12}
		}
		e, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		var l, r [128]float32
		allocations := testing.AllocsPerRun(20, func() {
			e.Reset()
			for p := modeledkit.Profile(0); p < modeledkit.ProfileCount; p++ {
				e.Push(cmd.Command{Op: cmd.OpNoteOn, Track: uint8(p) / uint8(drum.LaneCount), Index: uint16(p) % uint16(drum.LaneCount), Arg0: 110 << 8})
			}
			e.Render(l[:], r[:])
			for p := modeledkit.Profile(0); p < modeledkit.ProfileCount; p++ {
				e.Push(cmd.Command{Op: cmd.OpNoteOff, Track: uint8(p) / uint8(drum.LaneCount), Index: uint16(p) % uint16(drum.LaneCount)})
			}
			for i := 0; i < 16; i++ {
				e.Render(l[:], r[:])
			}
		})
		if allocations != 0 {
			t.Fatalf("%d Hz render allocations %g", rate, allocations)
		}
		var message cmd.Message
		for e.Poll(&message) {
			if message.Kind == cmd.Fault {
				t.Fatal(message)
			}
		}
		e.Reset()
		e.Render(l[:], r[:])
		for i := range l {
			if l[i] != 0 || r[i] != 0 {
				t.Fatal("reset retained a tail")
			}
		}
		cfg.MaxVoices--
		if _, err := New(cfg); err == nil {
			t.Fatal("kit escaped allocated voice budget")
		}
	}
}
