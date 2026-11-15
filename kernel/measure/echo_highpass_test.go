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
// feedback 0.8 and damp at 16 kHz (wow, flutter and saturation 0 on tape), at
// 44.1, 48 and 96 kHz. The level of a repeat is its energy in one echo
// period.
//
// The high-pass filters what enters the echo line, so the first repeat has
// passed it once and every later repeat once more. Two numbers per case:
//   - first repeat: dB the first repeat lies below the burst. Off it is 0;
//     with a cutoff it is the one-pole high-pass response at the burst's
//     frequency, 20*log10(f/sqrt(f^2+fc^2)).
//   - per repeat: dB each later repeat loses against the one before. Off it
//     is the feedback alone (1.9 dB at 0.8); with a cutoff the feedback plus
//     the same one-pole response.
//
// The gate requires the high-pass's part of both, at 80 Hz, to be within 1.5
// dB of that response, and the 2 kHz burst to lose at most 1 dB more than the
// response at 2 kHz: the repeats thin out to their highs.
func TestEchoHighpassRepeatsLoseTheirLows(t *testing.T) {
	const periodMs, repeats = 250, 4
	type echo interface {
		Process(left, right float32) (float32, float32)
	}
	makers := map[string]func(rate int, cutoff float64) (echo, error){
		"delay": func(rate int, cutoff float64) (echo, error) {
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
		"tape": func(rate int, cutoff float64) (echo, error) {
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
	// losses returns the dB the first repeat lies below the burst, and the dB
	// each later repeat loses against the one before.
	losses := func(e echo, rate int, hz float64) (first float64, later []float64) {
		period := rate * periodMs / 1000
		burst := period / 2
		var input float64
		levels := make([]float64, repeats+1)
		for i := 0; i < period*(repeats+1); i++ {
			var in float32
			if i < burst {
				window := .5 - .5*math.Cos(2*math.Pi*float64(i)/float64(burst))
				in = float32(.1 * window * math.Sin(2*math.Pi*hz*float64(i)/float64(rate)))
				input += float64(in) * float64(in)
			}
			out, _ := e.Process(in, in)
			levels[i/period] += float64(out) * float64(out)
		}
		for k := 2; k <= repeats; k++ {
			later = append(later, 10*math.Log10(levels[k-1]/levels[k]))
		}
		return 10 * math.Log10(input/levels[1]), later
	}
	response := func(f, fc float64) float64 {
		if fc == 0 {
			return 0
		}
		return -20 * math.Log10(f/math.Hypot(f, fc))
	}
	var report strings.Builder
	report.WriteString("dB below the burst (first repeat) and lost per repeat (repeats 2 to 4), 250 ms, feedback 0.8\n")
	for _, rate := range []int{44_100, 48_000, 96_000} {
		for _, name := range []string{"delay", "tape"} {
			var offPerRepeat float64
			for _, cutoff := range []float64{0, 100, 300, 1_000} {
				e, err := makers[name](rate, cutoff)
				if err != nil {
					t.Fatal(err)
				}
				lowFirst, low := losses(e, rate, 80)
				e, _ = makers[name](rate, cutoff)
				highFirst, high := losses(e, rate, 2_000)
				fmt.Fprintf(&report, "  %5d Hz %-5s highpass %-6s 80 Hz first %5.2f, per repeat %5.2f %5.2f %5.2f   2 kHz first %5.2f, per repeat %5.2f %5.2f %5.2f\n",
					rate, name, cutoffName(cutoff), lowFirst, low[0], low[1], low[2], highFirst, high[0], high[1], high[2])
				if cutoff == 0 {
					offPerRepeat = low[1]
					if math.Abs(lowFirst) > .1 || math.Abs(highFirst) > .1 {
						t.Errorf("%d Hz %s off: the first repeat is %.2f dB (80 Hz) and %.2f dB (2 kHz) below the burst, want 0", rate, name, lowFirst, highFirst)
					}
					continue
				}
				want := response(80, cutoff)
				if math.Abs(lowFirst-want) > 1.5 {
					t.Errorf("%d Hz %s highpass %g Hz: the first 80 Hz repeat is %.2f dB below the burst, want %.2f dB within 1.5 dB", rate, name, cutoff, lowFirst, want)
				}
				for _, loss := range low {
					if extra := loss - offPerRepeat; math.Abs(extra-want) > 1.5 {
						t.Errorf("%d Hz %s highpass %g Hz: the 80 Hz repeats lose %.2f dB more than off, want %.2f dB within 1.5 dB", rate, name, cutoff, extra, want)
					}
				}
				if limit := response(2_000, cutoff) + 1; highFirst > limit {
					t.Errorf("%d Hz %s highpass %g Hz: the first 2 kHz repeat is %.2f dB below the burst, more than %.2f dB", rate, name, cutoff, highFirst, limit)
				}
				for _, loss := range high {
					if loss-offPerRepeat > response(2_000, cutoff)+1 {
						t.Errorf("%d Hz %s highpass %g Hz: the 2 kHz repeats lose %.2f dB per repeat, too much for a %g Hz cutoff", rate, name, cutoff, loss, cutoff)
					}
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
