package render

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"m31labs.dev/cicada/kernel/mix"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestGuitarOfflineNativeParity(t *testing.T) {
	source, err := os.ReadFile("../examples/expressive-guitar.cicada")
	if err != nil {
		t.Fatal(err)
	}
	for _, rate := range []int{44100, 48000} {
		t.Run(strconv.Itoa(rate), func(t *testing.T) {
			var reference []byte
			for _, block := range []int{1, 128, 4096} {
				score, wav, report := renderSceneScore(t, string(source), Options{SampleRate: rate, Bits: 32, Block: block})
				assertSceneEngineMatchesWAV(t, score, wav, rate, 0, int(report.Frames))
				if report.OutputPeak == 0 || report.ClippedSamples != 0 {
					t.Fatalf("silent or clipped guitar: %+v", report)
				}
				if reference != nil && !bytes.Equal(reference, wav) {
					t.Fatalf("offline PCM changed at block %d", block)
				}
				reference = wav
			}
		})
	}
}

func TestGuitarOfflineBlockAllocationFree(t *testing.T) {
	source, err := os.ReadFile("../examples/expressive-guitar.cicada")
	if err != nil {
		t.Fatal(err)
	}
	score, ds := notation.Parse(source)
	if score == nil {
		t.Fatal(ds)
	}
	p, ds := project.FromScore(score)
	if p == nil {
		t.Fatal(ds)
	}
	for _, rate := range []int{44100, 48000} {
		tracks, err := compileTracks(score, p, rate)
		if err != nil {
			t.Fatal(err)
		}
		parameters, err := compileSceneParameters(p, tracks, rate, nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		limiter, err := mix.NewLimiter(rate)
		if err != nil {
			t.Fatal(err)
		}
		encoder, err := newWAVEncoder(32, false, 7, 1)
		if err != nil {
			t.Fatal(err)
		}
		var buffer [128 * 8]byte
		var report Report
		events := []scheduled{{event: seq.Event{Kind: seq.NoteOn, Note: 40, Velocity: 100, NoteID: 1}}}
		allocs := testing.AllocsPerRun(100, func() {
			for _, scene := range p.Scenes {
				if err := parameters.apply(scene.ID); err != nil {
					panic(err)
				}
				if err := renderBlock(io.Discard, tracks, nil, nil, nil, 0, 0, 0, 1, busMixerState{}, limiter, nil, &encoder, events, 0, 128, buffer[:], &report); err != nil {
					panic(err)
				}
			}
		})
		if allocs != 0 {
			t.Fatalf("offline block at %d Hz: %g allocations/run", rate, allocs)
		}
		t.Logf("METRIC: offline guitar block allocations/run | %g | %d Hz, scenes and 128-frame PCM encoding", allocs, rate)
	}
}

func TestGuitarExportsIgnoreOtherTracksNotePatterns(t *testing.T) {
	source := `tempo 120
key c major
track strings guitar { experimental=on octave=0 }
track bass acid { octave=2 }
pattern pluck notes { c1 . }
pattern low notes { 1,, . }
scene verse { strings=pluck bass=low }
song { verse }
`
	score, wav, report := renderSceneScore(t, source, Options{SampleRate: 48000, Bits: 32, Block: 128})
	assertSceneEngineMatchesWAV(t, score, wav, 48000, 0, int(report.Frames))
	if _, err := Stems(score, Options{SampleRate: 48000, Bits: 32, Block: 128}, filepath.Join(t.TempDir(), "stems")); err != nil {
		t.Fatal(err)
	}
}
