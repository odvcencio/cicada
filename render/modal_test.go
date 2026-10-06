package render_test

import (
	"bytes"
	"encoding/binary"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/mix"
	"m31labs.dev/cicada/kernel/voice/modal"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/render"
	"math"
	"os"
	"testing"
)

func TestModeledNativeOfflineParityAndBlockIndependence(t *testing.T) {
	for _, name := range modal.Names {
		t.Run(name, func(t *testing.T) {
			source, err := os.ReadFile("../examples/modeled/" + name + ".cicada")
			if err != nil {
				t.Fatal(err)
			}
			score, ds := notation.Parse(source)
			if score == nil {
				t.Fatal(ds)
			}
			for _, rate := range []int{44100, 48000} {
				opts := render.Options{SampleRate: rate, Bits: 32, Bars: 1, Block: 128}
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
						t.Fatalf("%d Hz block %d differs", rate, block)
					}
				}
				p, ds := project.FromScore(score)
				if p == nil {
					t.Fatal(ds)
				}
				cfg, err := project.CompileEngine(p, rate, 128)
				if err != nil {
					t.Fatal(err)
				}
				native, err := engine.New(cfg)
				if err != nil {
					t.Fatal(err)
				}
				native.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
				limiter, _ := mix.NewLimiter(rate)
				latency := limiter.LatencyFrames()
				var l, r [128]float32
				var peak float64
				nonzero := false
				for offset := 0; offset < int(report.Frames)+latency; offset += 128 {
					native.Render(l[:], r[:])
					for j := 0; j < 128; j++ {
						frame := offset + j - latency
						if frame < 0 || frame >= int(report.Frames)-128 {
							continue
						}
						for channel := 0; channel < 2; channel++ {
							sample := l[j]
							if channel == 1 {
								sample = r[j]
							}
							at := 44 + (frame*2+channel)*4
							want := math.Float32frombits(binary.LittleEndian.Uint32(baseline.Bytes()[at : at+4]))
							delta := math.Abs(float64(sample - want))
							peak = max(peak, delta)
							nonzero = nonzero || sample != 0
						}
					}
				}
				if !nonzero || peak > 1e-6 {
					t.Fatalf("%d Hz native/offline delta %g sounded=%v", rate, peak, nonzero)
				}
				t.Logf("%s rate=%d native_offline_peak_delta=%g block_bytes_identical=true", name, rate, peak)
			}
		})
	}
}
