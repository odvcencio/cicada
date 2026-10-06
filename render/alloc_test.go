package render

import (
	"io"
	"runtime"
	"testing"

	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/kernel/mix"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func prepareFirstBlockAllocationMeasurement(t *testing.T) {
	t.Helper()
	// MemStats counts allocations from every goroutine. Use one processor,
	// as AllocsPerRun does, so GC workers cannot overlap the first block.
	previousProcs := runtime.GOMAXPROCS(1)
	t.Cleanup(func() { runtime.GOMAXPROCS(previousProcs) })
	// Finish setup's GC work and allocate its semaphore wait state before
	// measuring. Keep the voices cold: renderBlock has not run yet.
	runtime.GC()
}

// The full WAV call constructs and sorts events. This gate covers its audio
// processing and encoding loop after setup, without altering either path.
func TestOfflineAudioLoopAllocationFree(t *testing.T) {
	const source = `tempo 120
key a minor
seed 4242
instrument glassbass {
  octave = 2
	param cutoff = 680Hz
  voice mono {
    let shape = env(gate, 330ms)
    out = ladder(mix(saw(pitch), square(pitch / 2), 0.32), cutoff * exp2(shape * 3.2), 0.58) * shape
  }
}
kit pulse { bd = builtin.bd }
fx drive drive { shape = soft gain = 9dB tone = 9kHz mix = 0.7 }
fx delay delay {}
fx reverb reverb {}
fx tone eq { type = highpass frequency = 25Hz }
fx glue comp { threshold = -18dB }
fx punch transient { attack = 2dB }
fx stereo width { amount = 1.1 }
fx ceiling limiter { ceiling = -1dBTP }
master { insert = tone -> glue -> punch -> stereo -> ceiling }
track acid acid { level = -24dB insert = drive send delay = 0.3 send reverb = 0.35 }
track graph glassbass { level = -24dB insert = drive send delay = 0.3 send reverb = 0.35 }
track drums pulse { level = -24dB insert = drive send delay = 0.3 send reverb = 0.35 }
pattern riff { 1 5 1 3 }
pattern beat drums { bd: X.x. }
scene first { acid = riff graph = riff drums = beat }
song { first*2 }
`
	for _, rate := range []int{44100, 48000} {
		for _, block := range []int{128, 256} {
			score, diagnostics := notation.ParseEdition([]byte(source), 2)
			for _, d := range diagnostics {
				if d.Severity == "error" {
					t.Fatal(d)
				}
			}
			p, diagnostics := project.FromScore(score)
			if p == nil {
				t.Fatal(diagnostics)
			}
			tracks, err := compileTracks(score, p, rate)
			if err != nil {
				t.Fatal(err)
			}
			delay, err := fx.NewDelay(rate, score.TempoMilli)
			if err != nil {
				t.Fatal(err)
			}
			reverb, err := fx.NewReverb(rate)
			if err != nil {
				t.Fatal(err)
			}
			parameters, err := compileSceneParameters(p, tracks, rate, delay, reverb, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err = parameters.apply("first"); err != nil {
				t.Fatal(err)
			}
			limiter, err := mix.NewLimiter(rate)
			if err != nil {
				t.Fatal(err)
			}
			encoder, err := newWAVEncoder(32, false, score.Seed, 1)
			if err != nil {
				t.Fatal(err)
			}
			buffer := make([]byte, block*encoder.frameBytes())
			events := []scheduled{
				{track: 0, event: seq.Event{Kind: seq.NoteOn, Note: 45, Velocity: 100}},
				{track: 1, event: seq.Event{Kind: seq.NoteOn, Note: 45, Velocity: 100}},
				{track: 2, event: seq.Event{Kind: seq.NoteOn, Velocity: 100}},
			}
			master, err := project.PrepareMaster(p, rate)
			if err != nil {
				t.Fatal(err)
			}
			var report Report
			allocs := testing.AllocsPerRun(100, func() {
				if err := renderBlock(io.Discard, tracks, delay, reverb, nil, 0, 0, 0, 1, busMixerState{masterProcessor: master, outputGain: 1}, limiter, nil, &encoder, events, 0, block, buffer, &report); err != nil {
					panic(err)
				}
			})
			if allocs != 0 {
				t.Fatalf("offline audio loop at %d Hz / %d frames: %g allocations", rate, block, allocs)
			}
			t.Logf("METRIC ALLOC path=offline_audio_loop rate_hz=%d block_frames=%d tracks=3 drive=true sends=true master=true allocs_block=0", rate, block)
		}
	}
}
