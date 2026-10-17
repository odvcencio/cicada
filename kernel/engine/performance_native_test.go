//go:build !tinygo && !js

package engine

import (
	"fmt"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/fx"
	"runtime"
	"testing"
	"time"
)

func performanceEngine(t testing.TB, tracks, frames, lanes int) *Engine {
	t.Helper()
	cfg := Config{SampleRate: 48000, MaxBlock: frames, Tracks: tracks, MaxVoices: tracks, BPMMilli: 120000, ParallelWaitFloor: 30 * time.Second}
	commands := make([]cmd.Command, tracks)
	for track := 0; track < tracks; track++ {
		cfg.Track[track].Kind = VoiceAcid
		p := fx.DefaultDriveParams()
		p.GainDB = 12
		cfg.Track[track].InsertDrive = &p
		commands[track] = cmd.Command{Op: cmd.OpNoteOn, Track: uint8(track), Arg0: uint32(40+track%36) | 100<<8}
	}
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.meterRate = 0
	if err = e.EnableParallel(lanes); err != nil {
		t.Fatal(err)
	}
	if !e.PushBatch(commands) {
		t.Fatal("live notes rejected")
	}
	return e
}

func BenchmarkNativeFullRender(b *testing.B) {
	for _, tracks := range []int{16, 32, 64} {
		for _, frames := range []int{64, 128, 256} {
			for _, lanes := range []int{1, 4} {
				b.Run(fmt.Sprintf("tracks%d/frames%d/lanes%d", tracks, frames, lanes), func(b *testing.B) {
					e := performanceEngine(b, tracks, frames, lanes)
					defer e.Close()
					l, r := make([]float32, frames), make([]float32, frames)
					e.Render(l, r)
					b.ReportAllocs()
					b.ResetTimer()
					for range b.N {
						e.Render(l, r)
						if e.faulted {
							b.Fatal("render fault")
						}
					}
				})
			}
		}
	}
}

func TestNativeParallelFullRenderExactPCM(t *testing.T) {
	if runtime.GOMAXPROCS(0) < 2 {
		t.Skip("requires multiple scheduler lanes")
	}
	for _, frames := range []int{64, 128, 256} {
		a, b := performanceEngine(t, 64, frames, 1), performanceEngine(t, 64, frames, 4)
		defer a.Close()
		defer b.Close()
		// Race instrumentation is deliberately slower than device deadlines.
		b.parallel.waitFloor = 30 * time.Second
		al, ar, bl, br := make([]float32, frames), make([]float32, frames), make([]float32, frames), make([]float32, frames)
		for block := 0; block < 10; block++ {
			a.Render(al, ar)
			b.Render(bl, br)
			for i := range al {
				if al[i] != bl[i] || ar[i] != br[i] {
					t.Fatalf("frames%d block%d frame%d PCM differs", frames, block, i)
				}
			}
			if a.faulted || b.faulted {
				t.Fatal("engine fault")
			}
		}
	}
}
