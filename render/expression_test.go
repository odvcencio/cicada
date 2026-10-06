package render

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/mix"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func expressionScore(t *testing.T) *notation.Score {
	t.Helper()
	source, err := os.ReadFile("../examples/per-note-expression.cicada")
	if err != nil {
		t.Fatal(err)
	}
	score, ds := notation.Parse(source)
	if len(ds) != 0 {
		t.Fatalf("expression parse: %+v", ds)
	}
	return score
}

func expressionEnginePCM24(t *testing.T, score *notation.Score, rate, frames int) []byte {
	t.Helper()
	p, ds := project.FromScore(score)
	if p == nil {
		t.Fatalf("expression project: %+v", ds)
	}
	cfg, err := project.CompileEngine(p, rate, 128)
	if err != nil {
		t.Fatal(err)
	}
	player, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !player.Push(cmd.Command{Op: cmd.OpPlay, Track: 255}) {
		t.Fatal("play rejected")
	}
	limiter, _ := mix.NewLimiter(rate)
	latency := limiter.LatencyFrames()
	pcm := make([]byte, frames*6)
	encoder, _ := newWAVEncoder(24, false, score.Seed, 1)
	var report Report
	var left, right [128]float32
	for at := 0; at < frames+latency; {
		n := min(128, frames+latency-at)
		player.Render(left[:n], right[:n])
		for i := range left[:n] {
			frame := at + i - latency
			if frame >= 0 {
				encoder.writeFrame(pcm[frame*6:], left[i], right[i], 1, &report)
			}
		}
		var message cmd.Message
		for player.Poll(&message) {
			if message.Kind == cmd.Fault {
				t.Fatalf("expression engine fault: %+v", message)
			}
		}
		at += n
	}
	return pcm
}

func TestExpressionOfflineNativePCM24Parity(t *testing.T) {
	score := expressionScore(t)
	dither := false
	for _, rate := range []int{44100, 48000, 96000} {
		t.Run(fmt.Sprint(rate), func(t *testing.T) {
			var reference []byte
			for _, block := range []int{1, 128, 4096} {
				var output bytes.Buffer
				report, err := WAV(score, Options{SampleRate: rate, Bits: 24, Bars: 2, Block: block, Dither: &dither}, &output)
				if err != nil {
					t.Fatal(err)
				}
				pcm := output.Bytes()[44 : 44+int(report.Frames)*6]
				if reference == nil {
					reference = expressionEnginePCM24(t, score, rate, int(report.Frames))
				}
				if !bytes.Equal(pcm, reference) {
					for i := range pcm {
						if pcm[i] != reference[i] {
							t.Fatalf("offline/native expression PCM24 differs at block %d frame %d byte %d: %02x != %02x", block, i/6, i%6, pcm[i], reference[i])
						}
					}
				}
			}
			t.Logf("METRIC: expression offline/native PCM24 rates=%d blocks=1,128,4096 mismatches=0", rate)
		})
	}
}

func TestExpressionOfflineRenderAllocations(t *testing.T) {
	score := expressionScore(t)
	p, ds := project.FromScore(score)
	if p == nil {
		t.Fatal(ds)
	}
	for _, rate := range []int{44100, 48000} {
		t.Run(fmt.Sprint(rate), func(t *testing.T) {
			tracks, err := compileTracks(score, p, rate)
			if err != nil {
				t.Fatal(err)
			}
			if err := applyScene(tracks, findScene(score, "main"), false); err != nil {
				t.Fatal(err)
			}
			limiter, err := mix.NewLimiter(rate)
			if err != nil {
				t.Fatal(err)
			}
			encoder, err := newWAVEncoder(24, false, score.Seed, 1)
			if err != nil {
				t.Fatal(err)
			}
			// Both voices receive an onset, then tied bend/vibrato updates in
			// this same block. Construction stays outside the measured callback.
			events := []scheduled{
				{track: 0, generation: tracks[0].generation, event: seq.Event{Kind: seq.NoteOn, NoteID: 1, Note: 60, Velocity: 100}},
				{track: 1, generation: tracks[1].generation, event: seq.Event{Kind: seq.NoteOn, NoteID: 1, Note: 36, Velocity: 100}},
				{track: 0, generation: tracks[0].generation, event: seq.Event{Kind: seq.NoteExpression, Sample: 32, StepIndex: 2}},
				{track: 1, generation: tracks[1].generation, event: seq.Event{Kind: seq.NoteExpression, Sample: 32, StepIndex: 2}},
				{track: 0, generation: tracks[0].generation, event: seq.Event{Kind: seq.NoteExpression, Sample: 64, StepIndex: 3}},
				{track: 1, generation: tracks[1].generation, event: seq.Event{Kind: seq.NoteExpression, Sample: 64, StepIndex: 3}},
			}
			var buffer [128 * 6]byte
			var report Report
			allocs := testing.AllocsPerRun(100, func() {
				if err := renderBlock(io.Discard, tracks, nil, nil, nil, 0, 0, 0, 1, busMixerState{}, limiter, nil, &encoder, events, 0, 128, buffer[:], &report); err != nil {
					panic(err)
				}
			})
			if allocs != 0 {
				t.Fatalf("offline acid/graph note and tie expression at %d Hz allocated %g objects", rate, allocs)
			}
			t.Logf("METRIC ALLOC path=offline_expression rate_hz=%d voices=acid,graph onset=true tie=true allocs_block=0", rate)
		})
	}
}

func TestChordExpressionOfflineNativePCM24Parity(t *testing.T) {
	source := strings.ReplaceAll(polyphonicScore, "pattern a notes { [d4 f4 a4]^?70 - . [c4 e4 g4] }", "pattern a notes {\n[d4 f4 a4] - . [c4 e4 g4]\nbend: 0ct 125ct . -50ct\nvibrato: 20ct 40ct . 0ct\npressure: 0.25 0.5 . 0.75\ntimbre: 0.4 0.6 . 0.5\n}")
	score, ds := notation.Parse([]byte(source))
	if len(ds) != 0 {
		t.Fatalf("chord expression parse: %+v", ds)
	}
	dither := false
	for _, rate := range []int{44100, 48000} {
		var reference []byte
		for _, block := range []int{17, 128, 4096} {
			var output bytes.Buffer
			report, err := WAV(score, Options{SampleRate: rate, Bits: 24, Block: block, Dither: &dither}, &output)
			if err != nil {
				t.Fatal(err)
			}
			pcm := output.Bytes()[44 : 44+int(report.Frames)*6]
			if reference == nil {
				reference = expressionEnginePCM24(t, score, rate, int(report.Frames))
			}
			if !bytes.Equal(pcm, reference) {
				t.Fatalf("chord expression offline/native PCM24 differs at rate %d block %d", rate, block)
			}
		}
	}
}
