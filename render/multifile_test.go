package render

import (
	"bytes"
	"io"
	"math"
	"path/filepath"
	"strconv"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/mix"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func multiFileExample(t testing.TB) (*notation.Score, *notation.Score, *project.Project) {
	t.Helper()
	files, err := project.ReadSources(filepath.Join("..", "examples", "multifile", "main.cicada"), nil)
	if err != nil {
		t.Fatal(err)
	}
	score, ds := files.Parse()
	if score == nil || len(ds) != 0 {
		t.Fatalf("multi-file score: %+v", ds)
	}
	source := []byte("cicada 2\n")
	for _, file := range files.Files {
		source = append(source, file.Source...)
		source = append(source, '\n')
	}
	single, ds := notation.Parse(source)
	if single == nil || len(ds) != 0 {
		t.Fatalf("concatenated score: %+v", ds)
	}
	p, ds := project.FromScore(score)
	if p == nil || len(ds) != 0 {
		t.Fatalf("project: %+v", ds)
	}
	return score, single, p
}

func TestMultiFileNativeAndOfflineByteParity(t *testing.T) {
	verifySourceNativeAndOfflineByteParity(t, multiFileExample, "multifile")
}

func verifySourceNativeAndOfflineByteParity(t *testing.T, example func(testing.TB) (*notation.Score, *notation.Score, *project.Project), label string) {
	score, single, p := example(t)
	q, ds := project.FromScore(single)
	if q == nil || len(ds) != 0 {
		t.Fatalf("single project: %+v", ds)
	}
	for _, rate := range []int{44100, 48000} {
		t.Run(strconv.Itoa(rate), func(t *testing.T) {
			cfg, err := project.CompileEngine(p, rate, 128)
			if err != nil {
				t.Fatal(err)
			}
			other, err := project.CompileEngine(q, rate, 128)
			if err != nil {
				t.Fatal(err)
			}
			a, err := engine.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			b, err := engine.New(other)
			if err != nil {
				t.Fatal(err)
			}
			a.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
			b.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
			frames := int(math.Round(float64(2*4*60*rate*1000) / float64(cfg.BPMMilli)))
			var al, ar, bl, br [128]float32
			for frame := 0; frame < frames; frame += 128 {
				a.Render(al[:], ar[:])
				b.Render(bl[:], br[:])
				for i := range al {
					if math.Float32bits(al[i]) != math.Float32bits(bl[i]) || math.Float32bits(ar[i]) != math.Float32bits(br[i]) {
						t.Fatalf("native bytes differ at frame %d", frame+i)
					}
				}
				var message cmd.Message
				for a.Poll(&message) {
				}
				for b.Poll(&message) {
				}
			}
			var multi, concat bytes.Buffer
			options := Options{SampleRate: rate, Bits: 32, Bars: 2}
			if _, err := WAV(score, options, &multi); err != nil {
				t.Fatal(err)
			}
			if _, err := WAV(single, options, &concat); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(multi.Bytes(), concat.Bytes()) {
				t.Fatal("offline WAV bytes differ")
			}
			assertSceneEngineMatchesWAV(t, score, multi.Bytes(), rate, 0, frames)
			t.Logf("METRIC %s rate=%d native_inline=byte-identical offline_inline=byte-identical frames=%d", label, rate, frames)
		})
	}
}

func TestMultiFileRenderAllocationFree(t *testing.T) {
	verifySourceRenderAllocationFree(t, multiFileExample, "multifile")
}

func verifySourceRenderAllocationFree(t *testing.T, example func(testing.TB) (*notation.Score, *notation.Score, *project.Project), label string) {
	score, _, p := example(t)
	for _, rate := range []int{44100, 48000} {
		cfg, err := project.CompileEngine(p, rate, 128)
		if err != nil {
			t.Fatal(err)
		}
		native, err := engine.New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		native.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
		var left, right [128]float32
		allocs := testing.AllocsPerRun(1000, func() {
			native.Render(left[:], right[:])
			var message cmd.Message
			for native.Poll(&message) {
			}
		})
		if allocs != 0 {
			t.Fatalf("native callback allocs: %g", allocs)
		}
		tracks, err := compileTracks(score, p, rate)
		if err != nil {
			t.Fatal(err)
		}
		if err := applyScene(tracks, &score.Scenes[0], true); err != nil {
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
		buffer := make([]byte, 128*8)
		report := Report{SampleRate: rate}
		events := make([]scheduled, len(tracks))
		for i := range tracks {
			events[i] = scheduled{track: i, event: seq.Event{Kind: seq.NoteOn, Note: uint8(45 + i*12), Velocity: 100}}
		}
		var renderErr error
		allocs = testing.AllocsPerRun(1000, func() {
			renderErr = renderBlock(io.Discard, tracks, nil, nil, nil, 0, 0, 0, 1, busMixerState{}, limiter, nil, &encoder, events, 0, 128, buffer, &report)
		})
		if renderErr != nil || allocs != 0 {
			t.Fatalf("offline render block: allocs=%g err=%v", allocs, renderErr)
		}
		t.Logf("METRIC %s rate=%d native_callback_allocs=0 offline_block_allocs=0", label, rate)
	}
}

var multiFileVoiceSample float32

func BenchmarkMultiFileVoiceNext(b *testing.B) {
	benchmarkSourceVoiceNext(b, multiFileExample)
}

func benchmarkSourceVoiceNext(b *testing.B, example func(testing.TB) (*notation.Score, *notation.Score, *project.Project)) {
	score, _, p := example(b)
	tracks, err := compileTracks(score, p, 48000)
	if err != nil {
		b.Fatal(err)
	}
	for _, track := range tracks {
		b.Run(track.name, func(b *testing.B) {
			voice := track.voice
			voice.NoteOn(45, 100, true, false)
			var sample float32
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if i%4800 == 0 {
					voice.NoteOn(45, 100, true, false)
				}
				sample = voice.Next()
			}
			multiFileVoiceSample = sample
		})
	}
}
