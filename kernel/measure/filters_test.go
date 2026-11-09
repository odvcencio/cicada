package measure

import (
	"fmt"
	"math"
	"math/cmplx"

	"m31labs.dev/cicada/kernel/fx/pro"
	"m31labs.dev/cicada/kernel/graph"
)

const (
	responseSettle = 8192
	responseFrames = 16384
)

func db(x float64) float64 { return 20 * math.Log10(x) }

// ---- graph filters ----

// filterProgram feeds a sine at hz, scaled to amp, into one filter node.
func filterProgram(op graph.Op, hz, amp, cutoff, resonance float64) graph.Program {
	var p graph.Program
	p.Nodes[0] = graph.Node{Op: graph.Constant, Value: float32(hz)}
	p.Nodes[1] = graph.Node{Op: graph.Sine, A: 0}
	p.Nodes[2] = graph.Node{Op: graph.Constant, Value: float32(amp)}
	p.Nodes[3] = graph.Node{Op: graph.Multiply, A: 1, B: 2}
	p.Nodes[4] = graph.Node{Op: graph.Constant, Value: float32(cutoff)}
	p.Nodes[5] = graph.Node{Op: graph.Constant, Value: float32(resonance)}
	p.Nodes[6] = graph.Node{Op: op, A: 3, B: 4, C: 5}
	p.Len, p.Output = 7, 6
	return p
}

func graphGain(op graph.Op, cutoff, resonance float64) func(hz float64) (float64, error) {
	return func(hz float64) (float64, error) {
		const amp = .01 // small enough that the ladder's input tanh stays linear
		v, err := graph.NewVoice(filterProgram(op, hz, amp, cutoff, resonance), sampleRate)
		if err != nil {
			return 0, err
		}
		v.NoteOn(69, 127, false)
		return toneGainDB(hz, amp, responseSettle, responseFrames, func(float32) float32 { return v.Next() }), nil
	}
}

// onePole is |G / (1 - (1-G) z^-1)| at hz.
func onePole(g, hz float64) float64 {
	z := cmplx.Rect(1, -2*math.Pi*hz/sampleRate)
	return cmplx.Abs(complex(g, 0) / (1 - complex(1-g, 0)*z))
}

func graphFilterResponses() []responseCase {
	// Ladder and diode: four trapezoidal one-poles, each the bilinear image of
	// 1/(s+1) with its corner prewarped to the cutoff.
	cascade := func(cutoff float64) func(float64) float64 {
		g := math.Tan(math.Pi * cutoff / sampleRate)
		return func(hz float64) float64 {
			w := math.Tan(math.Pi*hz/sampleRate) / g
			return 4 * db(1/math.Hypot(1, w))
		}
	}
	// With resonance the loop closes around the four stages: L^4 / (1 + k L^4),
	// where L is one stage and k the feedback. This pins the within-sample
	// feedback solve, including where the peak sits.
	resonant := func(cutoff, k float64) func(float64) float64 {
		g := math.Tan(math.Pi * cutoff / sampleRate)
		return func(hz float64) float64 {
			stage := 1 / complex(1, math.Tan(math.Pi*hz/sampleRate)/g)
			loop := stage * stage * stage * stage
			return db(cmplx.Abs(loop / (1 + complex(k, 0)*loop)))
		}
	}
	lowpass := func(cutoff float64) func(float64) float64 {
		g := 1 - math.Exp(-2*math.Pi*cutoff/sampleRate)
		return func(hz float64) float64 { return db(onePole(g, hz)) }
	}
	highpass := func(cutoff float64) func(float64) float64 {
		g := 1 - math.Exp(-2*math.Pi*cutoff/sampleRate)
		return func(hz float64) float64 {
			z := cmplx.Rect(1, -2*math.Pi*hz/sampleRate)
			low := complex(g, 0) / (1 - complex(1-g, 0)*z)
			return db(cmplx.Abs(1 - low))
		}
	}
	// SVF lowpass: the bilinear image of 1/(s^2 + k s + 1) at the internal 2x rate.
	svf := func(cutoff, resonance float64) func(float64) float64 {
		k := 2 - 1.9*resonance
		g := math.Tan(math.Pi * cutoff / (2 * sampleRate))
		return func(hz float64) float64 {
			w := math.Tan(math.Pi*hz/(2*sampleRate)) / g
			return db(1 / math.Hypot(1-w*w, k*w))
		}
	}
	pts1 := []float64{250, 500, 1000, 2000, 4000, 8000}
	pts4 := []float64{1000, 2000, 4000, 8000, 16000}
	ptsPeak := []float64{250, 500, 800, 900, 950, 1000, 1100, 1400, 2000, 4000}
	var out []responseCase
	for _, c := range []struct {
		name     string
		op       graph.Op
		feedback float64 // loop gain at resonance 1
	}{{"ladder", graph.Ladder, 3.2}, {"diode", graph.Diode, 2.8}} {
		for _, fc := range []float64{1000, 5000} {
			out = append(out, responseCase{
				Name: fmt.Sprintf("%s_fc%.0f_res0", c.name, fc), Hz: pts1, NominalHz: fc,
				Expected: cascade(fc), Measure: graphGain(c.op, fc, 0),
			})
		}
		out = append(out, responseCase{
			Name: c.name + "_fc1000_res0.9", Hz: ptsPeak,
			Expected: resonant(1000, .9*c.feedback), Measure: graphGain(c.op, 1000, .9),
		})
	}
	out = append(out,
		responseCase{Name: "lowpass_fc1000", Hz: pts1, NominalHz: 1000, Expected: lowpass(1000), Measure: graphGain(graph.Lowpass, 1000, 0)},
		responseCase{Name: "highpass_fc1000", Hz: pts1, NominalHz: 1000, Expected: highpass(1000), Measure: graphGain(graph.Highpass, 1000, 0)},
		responseCase{Name: "svf_fc1000_res0", Hz: pts1, NominalHz: 1000, Expected: svf(1000, 0), Measure: graphGain(graph.SVF, 1000, 0)},
		responseCase{Name: "svf_fc4000_res0.5", Hz: pts4, Expected: svf(4000, .5), Measure: graphGain(graph.SVF, 4000, .5)},
	)
	return out
}

// ---- pro EQ against the RBJ audio EQ cookbook ----

type rbjCoefficients struct{ b0, b1, b2, a0, a1, a2 float64 }

// rbj builds the cookbook biquad (https://www.w3.org/TR/audio-eq-cookbook/)
// independently of kernel/fx/pro. Shelves use slope S = 1.
func rbj(b pro.Band) rbjCoefficients {
	w0 := 2 * math.Pi * b.FrequencyHz / sampleRate
	cos, sin := math.Cos(w0), math.Sin(w0)
	alpha := sin / (2 * b.Q)
	a := math.Pow(10, b.GainDB/40)
	switch b.Type {
	case pro.Peak:
		return rbjCoefficients{1 + alpha*a, -2 * cos, 1 - alpha*a, 1 + alpha/a, -2 * cos, 1 - alpha/a}
	case pro.LowShelf, pro.HighShelf:
		beta := 2 * math.Sqrt(a) * (sin / 2 * math.Sqrt((a+1/a)*(1/1.0-1)+2))
		if b.Type == pro.LowShelf {
			return rbjCoefficients{
				a * ((a + 1) - (a-1)*cos + beta), 2 * a * ((a - 1) - (a+1)*cos), a * ((a + 1) - (a-1)*cos - beta),
				(a + 1) + (a-1)*cos + beta, -2 * ((a - 1) + (a+1)*cos), (a + 1) + (a-1)*cos - beta}
		}
		return rbjCoefficients{
			a * ((a + 1) + (a-1)*cos + beta), -2 * a * ((a - 1) + (a+1)*cos), a * ((a + 1) + (a-1)*cos - beta),
			(a + 1) - (a-1)*cos + beta, 2 * ((a - 1) - (a+1)*cos), (a + 1) - (a-1)*cos - beta}
	case pro.Highpass:
		return rbjCoefficients{(1 + cos) / 2, -(1 + cos), (1 + cos) / 2, 1 + alpha, -2 * cos, 1 - alpha}
	default: // Lowpass
		return rbjCoefficients{(1 - cos) / 2, 1 - cos, (1 - cos) / 2, 1 + alpha, -2 * cos, 1 - alpha}
	}
}

func (c rbjCoefficients) magnitudeDB(hz float64) float64 {
	z1 := cmplx.Rect(1, -2*math.Pi*hz/sampleRate)
	z2 := z1 * z1
	num := complex(c.b0, 0) + complex(c.b1, 0)*z1 + complex(c.b2, 0)*z2
	den := complex(c.a0, 0) + complex(c.a1, 0)*z1 + complex(c.a2, 0)*z2
	return db(cmplx.Abs(num / den))
}

func eqResponses() []responseCase {
	points := []float64{30, 60, 100, 250, 500, 1000, 2000, 4000, 8000, 12000, 18000}
	mk := func(name string, bands [4]pro.Band) responseCase {
		return responseCase{
			Name: name, Hz: points,
			Expected: func(hz float64) float64 {
				total := 0.0
				for _, b := range bands {
					if b.Enabled {
						total += rbj(b).magnitudeDB(hz)
					}
				}
				return total
			},
			Measure: func(hz float64) (float64, error) {
				eq, err := pro.NewEQ(sampleRate, bands)
				if err != nil {
					return 0, err
				}
				return toneGainDB(hz, .1, responseSettle, responseFrames, func(x float32) float32 { y, _ := eq.Process(x, x); return y }), nil
			},
		}
	}
	return []responseCase{
		mk("mastering_curve", [4]pro.Band{
			{Enabled: true, Type: pro.Highpass, FrequencyHz: 25, Q: .7071067812},
			{Enabled: true, Type: pro.LowShelf, FrequencyHz: 90, Q: 1, GainDB: 1.5},
			{Enabled: true, Type: pro.Peak, FrequencyHz: 250, Q: .8, GainDB: -1.5},
			{Enabled: true, Type: pro.HighShelf, FrequencyHz: 6500, Q: 1, GainDB: 1},
		}),
		mk("peak_plus6_1k_q1_with_lowpass_10k", [4]pro.Band{
			{Enabled: true, Type: pro.Peak, FrequencyHz: 1000, Q: 1, GainDB: 6},
			{Enabled: true, Type: pro.Lowpass, FrequencyHz: 10000, Q: .7071067812},
			{Enabled: true, Type: pro.Peak, FrequencyHz: 4000, Q: 4, GainDB: -9},
			{Enabled: true, Type: pro.LowShelf, FrequencyHz: 200, Q: 1, GainDB: -6},
		}),
	}
}
