package measure

import (
	"math"
	"time"

	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/kernel/fx/ampcab"
	"m31labs.dev/cicada/kernel/fx/chorus"
	"m31labs.dev/cicada/kernel/fx/convolution"
	"m31labs.dev/cicada/kernel/fx/pro"
	"m31labs.dev/cicada/kernel/fx/tape"
)

const tempoMilliBPM = 120_000

const (
	settleLong   = 96_000  // 2 s, for delays and modulated effects
	settleReverb = 288_000 // 6 s: the 2.4 s reverb tail from the sine's onset falls below -140 dB
)

func fxCases() []fxCase {
	var out []fxCase
	add := func(c fxCase) { out = append(out, c) }

	add(fxCase{Name: "comp", Description: "Compressor (kernel/fx), default controls: -18 dB threshold, 4:1, auto makeup.", Settle: 24_000,
		New: func() (processor, error) {
			c, err := fx.NewCompressor(sampleRate)
			if err != nil {
				return nil, err
			}
			return c.Process, nil
		}})
	add(fxCase{Name: "deesser", Description: "De-esser (kernel/fx), default controls: 6 kHz split, -24 dB threshold.", Settle: 24_000,
		New: func() (processor, error) {
			d, err := fx.NewDeesser(sampleRate, fx.DefaultDeesserParams())
			if err != nil {
				return nil, err
			}
			return d.Process, nil
		}})
	add(fxCase{Name: "delay", Description: "Tempo delay (kernel/fx), default controls at 120 BPM, wet only, eighth notes.", Settle: settleLong,
		New: func() (processor, error) {
			d, err := fx.NewDelay(sampleRate, tempoMilliBPM)
			if err != nil {
				return nil, err
			}
			return d.Process, nil
		}})
	for _, shape := range []struct {
		name string
		s    fx.DriveShape
	}{{"soft", fx.Soft}, {"hard", fx.Hard}, {"fold", fx.Fold}, {"diode", fx.Diode}} {
		shape := shape
		add(fxCase{Name: "drive_" + shape.name, Description: "Drive (kernel/fx), " + shape.name + " shape, +12 dB input gain, 12 kHz tone, fully wet; includes its 2x oversampling.", Settle: 24_000,
			New: func() (processor, error) {
				d, err := fx.NewDrive(sampleRate)
				if err != nil {
					return nil, err
				}
				p := fx.DefaultDriveParams()
				p.Shape, p.GainDB = shape.s, 12
				if err := d.SetParams(p); err != nil {
					return nil, err
				}
				return d.Process, nil
			}})
	}
	add(fxCase{Name: "reverb", Description: "Feedback-delay-network reverb (kernel/fx), default controls, wet only. Its delay lines are modulated by design, so thdn_db includes that modulation.", Settle: settleReverb,
		New: func() (processor, error) {
			r, err := fx.NewReverb(sampleRate)
			if err != nil {
				return nil, err
			}
			return r.Process, nil
		}})
	add(fxCase{Name: "ampcab", Description: "Amp and cabinet (kernel/fx/ampcab), default controls: drive 0.35, cabinet 0, 4x-style oversampling inside.", Settle: 24_000,
		New: func() (processor, error) {
			a, err := ampcab.New(sampleRate, ampcab.DefaultParams())
			if err != nil {
				return nil, err
			}
			return a.Process, nil
		}})
	add(fxCase{Name: "chorus", Description: "Chorus (kernel/fx/chorus), default controls: 3 voices, 0.6 Hz, mix 0.5. Its fast 5.7-7 Hz modulation does not depend on the depth control, so thdn_db includes that designed modulation.", Settle: settleLong,
		New: func() (processor, error) {
			c, err := chorus.New(sampleRate, chorus.DefaultParams())
			if err != nil {
				return nil, err
			}
			return c.Process, nil
		}})
	add(fxCase{Name: "convolution", Description: "Partitioned convolution reverb (kernel/fx/convolution), 1 s synthetic decaying-noise impulse response, 128-frame partitions.", Settle: settleLong,
		New: func() (processor, error) {
			left, right := syntheticIR(1, 48_000), syntheticIR(2, 48_000)
			c, err := convolution.New(sampleRate, left, right, 128)
			if err != nil {
				return nil, err
			}
			return c.Process, nil
		}})
	add(fxCase{Name: "tape", Description: "Tape echo (kernel/fx/tape), default controls at 120 BPM except wow and flutter 0, so thdn_db measures the saturation and feedback path and not the designed pitch modulation.", Settle: settleLong,
		New: func() (processor, error) {
			t, err := tape.New(sampleRate, tempoMilliBPM)
			if err != nil {
				return nil, err
			}
			p := tape.DefaultParams()
			p.Wow, p.Flutter = 0, 0
			if err := t.SetParams(p); err != nil {
				return nil, err
			}
			return t.Process, nil
		}})

	add(fxCase{Name: "pro_eq", Description: "Four-band equalizer (kernel/fx/pro), mastering preset curve. Frequency response is compared with the RBJ cookbook.", Settle: 24_000,
		Responses: eqResponses(),
		New: func() (processor, error) {
			p, _ := pro.Preset("mastering")
			e, err := pro.NewEQ(sampleRate, p.EQ)
			if err != nil {
				return nil, err
			}
			return e.Process, nil
		}})
	add(fxCase{Name: "pro_limiter", Description: "Look-ahead true-peak limiter (kernel/fx/pro), default: -1 dBTP, 3 ms look-ahead, 100 ms release.", Settle: 24_000,
		New: func() (processor, error) {
			l, err := pro.NewLimiter(sampleRate, pro.DefaultLimiterParams())
			if err != nil {
				return nil, err
			}
			return l.Process, nil
		}})
	add(fxCase{Name: "pro_transient", Description: "Transient shaper (kernel/fx/pro), transient preset: attack +4 dB, sustain -2 dB.", Settle: 24_000,
		New: func() (processor, error) {
			p, _ := pro.Preset("transient")
			t, err := pro.NewTransient(sampleRate, p.Transient)
			if err != nil {
				return nil, err
			}
			return t.Process, nil
		}})
	add(fxCase{Name: "pro_width", Description: "Stereo width (kernel/fx/pro), width preset: amount 1.25, bass mono below 120 Hz. The test input is mono, so the side channel is silent and only the mid path is measured.", Settle: 24_000,
		New: func() (processor, error) {
			p, _ := pro.Preset("width")
			w, err := pro.NewWidth(sampleRate, p.Width)
			if err != nil {
				return nil, err
			}
			return w.Process, nil
		}})
	add(fxCase{Name: "pro_reverb", Description: "Early-reflection plus tail reverb (kernel/fx/pro), reverb preset: mix 0.22, 1.7 s decay.", Settle: settleReverb,
		New: func() (processor, error) {
			p, _ := pro.Preset("reverb")
			r, err := pro.NewReverb(sampleRate, p.Reverb)
			if err != nil {
				return nil, err
			}
			return r.Process, nil
		}})
	add(fxCase{Name: "pro_chain_mastering", Description: "Whole pro chain (kernel/fx/pro.New) with the mastering preset: EQ, compressor, limiter.", Settle: 24_000,
		New: func() (processor, error) {
			p, _ := pro.Preset("mastering")
			c, err := pro.New(sampleRate, p)
			if err != nil {
				return nil, err
			}
			return c.Process, nil
		}})
	add(fxCase{Name: "pro_normalize", Description: "Offline loudness normalization (kernel/fx/pro.NormalizeStereo) to -14 LUFS, ceiling -1 dBTP. CPU is per 128 frames of programme.",
		Custom: measureNormalize})
	return out
}

// syntheticIR is exponentially decaying noise from a fixed linear
// congruential generator, so the impulse response is the same on every run.
func syntheticIR(seed uint32, frames int) []float32 {
	out := make([]float32, frames)
	state := seed*2654435761 + 1
	for i := range out {
		state = state*1664525 + 1013904223
		noise := float64(int32(state)) / (1 << 31)
		out[i] = float32(.5 * noise * math.Exp(-6.9*float64(i)/float64(frames)))
	}
	return out
}

func measureNormalize(e *entry) error {
	sine := func(frames int, amplitude float64) ([]float32, []float32) {
		l, r := make([]float32, frames), make([]float32, frames)
		for i := range l {
			l[i] = float32(amplitude * math.Sin(2*math.Pi*thdFrequency*float64(i)/sampleRate))
			r[i] = l[i]
		}
		return l, r
	}
	// THD+N: a -23 dBFS peak sine, 2 s, gained to -14 LUFS.
	l, r := sine(2*sampleRate, math.Pow(10, -23.0/20))
	result, err := pro.NormalizeStereo(sampleRate, l, r, -14, -1)
	if err != nil {
		return err
	}
	x := make([]float64, thdFrames)
	for i := range x {
		x[i] = float64(l[sampleRate/2+i])
	}
	m := e.measured(metricTHDN, "dB", thdnDB(x), true)
	m.Detail = map[string]float64{"applied_gain_db": round(result.AppliedGainDB), "after_lufs": round(result.After.IntegratedLUFS)}

	e.notApplicable(metricResponse, "a constant gain; it has no frequency response")

	// Noise floor: 1 s of silence must come back as silence.
	sl, sr := make([]float32, sampleRate), make([]float32, sampleRate)
	if _, err := pro.NormalizeStereo(sampleRate, sl, sr, -14, -1); err != nil {
		return err
	}
	e.measured(metricNoise, "dBFS", rmsDBFS(sl, sr), true)

	// CPU: normalize 200 blocks' worth of programme (25600 frames, 0.53 s).
	ns := medianNS(func() time.Duration {
		l, r := sine(cpuBlocksPerRn*blockFrames, .05)
		start := time.Now()
		_, _ = pro.NormalizeStereo(sampleRate, l, r, -14, -1)
		return time.Since(start)
	})
	e.measured(metricCPU, "ns/block", ns, false)
	return nil
}
