package measure

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/kernel/fx/tape"
)

// TestEchoHighpassRepeatsLoseTheirLows measures what the in-loop highpass of
// the delay and tape echo returns does to each repeat. A Hann-windowed burst
// of 80 Hz, and separately one of 2 kHz, goes into a 250 ms echo with
// feedback 0.8 and damp at 16 kHz (wow, flutter and saturation 0 on tape).
// The level of every repeat is its energy in one echo period; the result is
// the dB each repeat loses against the one before, for the low and the high
// burst. Off, both fall by the feedback alone (1.9 dB). With a cutoff, the low
// burst loses that plus the one-pole high-pass's attenuation at 80 Hz on every
// pass, while the high burst keeps nearly all of its level: the repeats thin
// out. The gate checks that the extra low-band loss per repeat is within 1.5 dB
// of the one-pole response, 20*log10(f/sqrt(f^2+fc^2)), at each cutoff.
func TestEchoHighpassRepeatsLoseTheirLows(t *testing.T) {
	const rate, periodMs, repeats = 48_000, 250, 4
	period := rate * periodMs / 1000
	type echo interface {
		Process(left, right float32) (float32, float32)
	}
	makers := map[string]func(cutoff float64) (echo, error){
		"delay": func(cutoff float64) (echo, error) {
			d, err := fx.NewDelay(rate, 120_000)
			if err != nil {
				return nil, err
			}
			if err := d.SetParams(fx.DelayParams{Division: fx.FreeDelay, TimeMs: periodMs, Feedback: .8, DampHz: 16_000, Width: 1, Mix: 1, HighpassHz: cutoff}); err != nil {
				return nil, err
			}
			d.Reset()
			return d, nil
		},
		"tape": func(cutoff float64) (echo, error) {
			e, err := tape.New(rate, 120_000)
			if err != nil {
				return nil, err
			}
			if err := e.SetParams(tape.Params{Division: fx.FreeDelay, TimeMs: periodMs, Feedback: .8, DampHz: 16_000, HighpassHz: cutoff}); err != nil {
				return nil, err
			}
			e.Reset()
			return e, nil
		},
	}
	// lossPerRepeat returns the dB each repeat after the first loses.
	lossPerRepeat := func(e echo, hz float64) []float64 {
		burst := period / 2
		levels := make([]float64, repeats+1)
		for i := 0; i < period*(repeats+1); i++ {
			var in float32
			if i < burst {
				window := .5 - .5*math.Cos(2*math.Pi*float64(i)/float64(burst))
				in = float32(.1 * window * math.Sin(2*math.Pi*hz*float64(i)/rate))
			}
			out, _ := e.Process(in, in)
			levels[i/period] += float64(out) * float64(out)
		}
		var loss []float64
		for k := 2; k <= repeats; k++ {
			loss = append(loss, 10*math.Log10(levels[k-1]/levels[k]))
		}
		return loss
	}
	var report strings.Builder
	report.WriteString("dB lost per repeat (repeats 2 to 4), 250 ms, feedback 0.8\n")
	for _, name := range []string{"delay", "tape"} {
		var offLow float64
		for _, cutoff := range []float64{0, 100, 300, 1_000} {
			e, err := makers[name](cutoff)
			if err != nil {
				t.Fatal(err)
			}
			low := lossPerRepeat(e, 80)
			e, _ = makers[name](cutoff)
			high := lossPerRepeat(e, 2_000)
			fmt.Fprintf(&report, "  %-5s highpass %-6s 80 Hz %5.2f %5.2f %5.2f   2 kHz %5.2f %5.2f %5.2f\n", name, cutoffName(cutoff), low[0], low[1], low[2], high[0], high[1], high[2])
			if cutoff == 0 {
				offLow = low[1]
				continue
			}
			want := -20 * math.Log10(80/math.Hypot(80, cutoff))
			for _, loss := range low {
				if extra := loss - offLow; math.Abs(extra-want) > 1.5 {
					t.Errorf("%s highpass %g Hz: the 80 Hz repeats lose %.2f dB more than off, want %.2f dB within 1.5 dB", name, cutoff, extra, want)
				}
			}
			for _, loss := range high {
				if loss-offLow > -20*math.Log10(2_000/math.Hypot(2_000, cutoff))+1 {
					t.Errorf("%s highpass %g Hz: the 2 kHz repeats lose %.2f dB per repeat, too much for a %g Hz cutoff", name, cutoff, loss, cutoff)
				}
			}
		}
	}
	t.Log(report.String())
}

func cutoffName(hz float64) string {
	if hz == 0 {
		return "off"
	}
	return fmt.Sprintf("%gHz", hz)
}
