package render_test

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/mix"
	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/kernel/voice/modeledkit"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/render"
)

func TestBrushKitNativeOfflineParityAndBlockIndependence(t *testing.T) {
	const source = `cicada 1
tempo 120
seed 21
kit brushes { bd=model.kick sd=model.brush_tap cp=model.brush_sweep }
track percussion brushes { level=-12db sd_decay=0.8 cp_decay=0.5 cp_tune=1.2 cp_position=0.5 }
pattern ballad drums steps=16 {
 bd: x7... .... .... x6...
 sd: .... x5... .... ...x4
 cp: x8... .... x6... ....
}
scene main { percussion=ballad }
song { main*2 }
`
	score, diagnostics := notation.Parse([]byte(source))
	if score == nil || len(diagnostics) != 0 {
		t.Fatalf("parse: %+v", diagnostics)
	}
	p, diagnostics := project.FromScore(score)
	if p == nil || len(diagnostics) != 0 {
		t.Fatalf("project: %+v", diagnostics)
	}
	for _, rate := range []int{44_100, 48_000} {
		opts := render.Options{SampleRate: rate, Bits: 32, Bars: 2, Block: 128}
		var baseline bytes.Buffer
		report, err := render.WAV(score, opts, &baseline)
		if err != nil {
			t.Fatal(err)
		}
		for _, block := range []int{1, 4096} {
			opts.Block = block
			var audio bytes.Buffer
			if _, err := render.WAV(score, opts, &audio); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(baseline.Bytes(), audio.Bytes()) {
				t.Fatalf("%d Hz block %d changed the brush kit render", rate, block)
			}
		}
		cfg, err := project.CompileEngine(p, rate, 128)
		if err != nil {
			t.Fatal(err)
		}
		kit := cfg.Track[0].Kit
		if kit[drum.SD].Model != modeledkit.BrushTap || kit[drum.CP].Model != modeledkit.BrushSweep || kit[drum.BD].Model != modeledkit.Kick {
			t.Fatalf("the kit bound %v, %v and %v", kit[drum.BD].Model, kit[drum.SD].Model, kit[drum.CP].Model)
		}
		if got, want := kit[drum.CP].ModelParams, (modeledkit.Params{Tune: 1.2, Decay: .5, Position: .5, Humanize: .015}); got != want {
			t.Fatalf("controls did not reach the sweep: %+v", got)
		}
		native, err := engine.New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if !native.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff}) {
			t.Fatal("native play command was rejected")
		}
		limiter, err := mix.NewLimiter(rate)
		if err != nil {
			t.Fatal(err)
		}
		latency := limiter.LatencyFrames()
		var left, right [128]float32
		var peakDelta float64
		nonzero := false
		for offset := 0; offset < int(report.Frames)+latency; offset += 128 {
			native.Render(left[:], right[:])
			for j := range left {
				frame := offset + j - latency
				if frame < 0 || frame >= int(report.Frames)-128 {
					continue
				}
				for channel, sample := range [...]float32{left[j], right[j]} {
					at := 44 + (frame*2+channel)*4
					want := math.Float32frombits(binary.LittleEndian.Uint32(baseline.Bytes()[at : at+4]))
					peakDelta = max(peakDelta, math.Abs(float64(sample-want)))
					nonzero = nonzero || sample != 0
				}
			}
			var message cmd.Message
			for native.Poll(&message) {
				if message.Kind == cmd.Fault {
					t.Fatalf("native brush kit fault: %+v", message)
				}
			}
		}
		if !nonzero || peakDelta > 1e-6 {
			t.Fatalf("%d Hz native/offline delta=%g nonzero=%v", rate, peakDelta, nonzero)
		}
		t.Logf("rate=%d native_offline_peak_delta=%g block_bytes_identical=true", rate, peakDelta)
	}
}

// A score row carries no gate end, so a sweep in a score runs for its own
// hold, which the lane's decay control scales.
func TestBrushSweepInAScoreEndsAfterItsDecayScaledHold(t *testing.T) {
	const template = `cicada 1
tempo 120
seed 21
kit brushes { cp=model.brush_sweep }
track percussion brushes { level=-12db cp_decay=DECAY }
pattern one drums steps=16 { cp: x9... .... .... .... }
scene main { percussion=one }
song { main*2 }
`
	windowEnergy := func(decay string, from, to float64) float64 {
		t.Helper()
		score, diagnostics := notation.Parse([]byte(strings.Replace(template, "DECAY", decay, 1)))
		if score == nil || len(diagnostics) != 0 {
			t.Fatalf("parse: %+v", diagnostics)
		}
		var audio bytes.Buffer
		if _, err := render.WAV(score, render.Options{SampleRate: 48_000, Bits: 32, Bars: 2}, &audio); err != nil {
			t.Fatal(err)
		}
		var sum float64
		for frame := int(from * 48_000); frame < int(to*48_000); frame++ {
			for channel := 0; channel < 2; channel++ {
				at := 44 + (frame*2+channel)*4
				sample := float64(math.Float32frombits(binary.LittleEndian.Uint32(audio.Bytes()[at : at+4])))
				sum += sample * sample
			}
		}
		return sum
	}
	// The bar is 2 s long and the sweep starts at its first step.
	sounding := windowEnergy("0.5", .2, .6)
	after := windowEnergy("0.5", 1.3, 1.9)
	held := windowEnergy("1", 1.3, 1.5)
	t.Logf("sweep energy: decay 0.5 at 0.2-0.6 s %g, at 1.3-1.9 s %g; decay 1 at 1.3-1.5 s %g", sounding, after, held)
	if sounding <= 0 || after > sounding*1e-9 {
		t.Fatalf("a sweep at decay 0.5 did not end by 1.3 s: %g then %g", sounding, after)
	}
	if held < sounding*.25 {
		t.Fatalf("a sweep at decay 1 did not hold to 1.5 s: %g against %g at the start", held, sounding)
	}
}

// brushDocExamples returns each fenced score in docs/audio/modeled-kit.md that
// binds a brush piece, so the documented examples are the ones rendered here.
func brushDocExamples(t *testing.T) []string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	doc, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "docs", "audio", "modeled-kit.md"))
	if err != nil {
		t.Fatal(err)
	}
	var examples []string
	// Odd parts of a split on the fence marker are fenced blocks, and the first
	// line of each is its language.
	for i, block := range strings.Split(string(doc), "```") {
		if i%2 == 0 {
			continue
		}
		_, source, _ := strings.Cut(block, "\n")
		if strings.HasPrefix(source, "cicada ") && strings.Contains(source, "model.brush_") {
			examples = append(examples, source)
		}
	}
	if len(examples) == 0 {
		t.Fatal("docs/audio/modeled-kit.md has no brush example")
	}
	return examples
}

func TestBrushDocExamplesRenderAndKeepTheirBlockIndependence(t *testing.T) {
	for i, source := range brushDocExamples(t) {
		score, diagnostics := notation.Parse([]byte(source))
		if score == nil || len(diagnostics) != 0 {
			t.Fatalf("example %d: parse: %+v", i, diagnostics)
		}
		var baseline bytes.Buffer
		report, err := render.WAV(score, render.Options{SampleRate: 48_000, Bits: 32}, &baseline)
		if err != nil {
			t.Fatal(err)
		}
		var again bytes.Buffer
		if _, err := render.WAV(score, render.Options{SampleRate: 48_000, Bits: 32, Block: 1}, &again); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(baseline.Bytes(), again.Bytes()) {
			t.Fatalf("example %d: block size changed the documented brush groove", i)
		}
		if report.Peak <= 0 || report.Peak > 1 {
			t.Fatalf("example %d rendered with peak %g", i, report.Peak)
		}
		t.Logf("doc example %d: %d frames, peak %g", i, report.Frames, report.Peak)
	}
}
