package engine

import (
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/voice/modal"
	"testing"
)

func TestModalLiveRenderAllocationFreeAndReset(t *testing.T) {
	for profile := modal.Profile(0); profile < modal.ProfileCount; profile++ {
		cfg := Config{SampleRate: 48000, MaxBlock: 128, Tracks: 1, MaxVoices: 4, BPMMilli: 120000, Track: [16]TrackConfig{{Kind: VoiceModal, Modal: profile}}}
		e, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		var l, r [128]float32
		allocations := testing.AllocsPerRun(30, func() {
			e.Reset()
			e.Push(cmd.Command{Op: cmd.OpNoteOn, Track: 0, Arg0: 60 | 100<<8})
			for i := 0; i < 8; i++ {
				e.Render(l[:], r[:])
			}
			e.Push(cmd.Command{Op: cmd.OpNoteOn, Track: 0, Arg0: 64 | 100<<8 | 1<<17})
			e.Render(l[:], r[:])
			e.Push(cmd.Command{Op: cmd.OpNoteOff, Track: 0})
			e.Render(l[:], r[:])
		})
		if allocations != 0 {
			t.Fatalf("profile %d allocs %g", profile, allocations)
		}
		e.Reset()
		e.Render(l[:], r[:])
		for i := range l {
			if l[i] != 0 || r[i] != 0 {
				t.Fatal("reset not silent")
			}
		}
		var message cmd.Message
		for e.Poll(&message) {
			if message.Kind == cmd.Fault {
				t.Fatal(message)
			}
		}
	}
}
