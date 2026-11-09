package measure

import (
	"fmt"
	"math"
	"sort"
	"time"
)

// block fills l and r (equal length) with the next frames.
type block func(l, r []float32)

func stereoBlock(next func() (float32, float32)) block {
	return func(l, r []float32) {
		for i := range l {
			l[i], r[i] = next()
		}
	}
}

func monoBlock(next func() float32) block {
	return func(l, r []float32) {
		for i := range l {
			l[i] = next()
			r[i] = l[i]
		}
	}
}

// ---- voices ----

type voiceCase struct {
	Name        string
	Description string
	// New builds a fresh instance with fixed seeds. A note below zero builds
	// an idle instance with nothing triggered.
	New func(note int) (block, error)
	// AliasNA, when set, is the reason aliasing is not applicable.
	AliasNA  string
	Notes    []int // aliasing notes; default C7 and C8
	Partials partialSet
	// Responses are filter measurements, if the voice exposes a filter.
	Responses  []responseCase
	ResponseNA string
	THDNNA     string
}

var defaultAliasNotes = []int{96, 108}

func measureVoice(c voiceCase) (*entry, error) {
	e := newEntry("voice/"+c.Name, "voice", c.Description)

	// Aliasing.
	if c.AliasNA != "" {
		e.notApplicable(metricAliasing, c.AliasNA)
	} else {
		notes := c.Notes
		if notes == nil {
			notes = defaultAliasNotes
		}
		detail := map[string]float64{}
		worst := dbFloor
		for _, note := range notes {
			render, err := c.New(note)
			if err != nil {
				return nil, fmt.Errorf("%s: note %d: %w", c.Name, note, err)
			}
			l, r := make([]float32, aliasSkip+aliasSize), make([]float32, aliasSkip+aliasSize)
			render(l, r)
			x := make([]float64, aliasSize)
			for i := range x {
				x[i] = float64(l[aliasSkip+i])
			}
			res, err := aliasing(x, note, c.Partials)
			if err != nil {
				return nil, fmt.Errorf("%s: note %d: %w", c.Name, note, err)
			}
			detail[fmt.Sprintf("note_%d_db", note)] = round(res.DB)
			detail[fmt.Sprintf("note_%d_f0_hz", note)] = round(res.F0)
			detail[fmt.Sprintf("note_%d_partials", note)] = float64(res.Partials)
			worst = math.Max(worst, res.DB)
		}
		m := e.measured(metricAliasing, "dB", worst, true)
		m.Detail = detail
	}

	e.notApplicable(metricTHDN, orDefault(c.THDNNA, "voices are measured for aliasing; THD+N is defined for effects with a sine input, and a voice has no audio input"))

	applyResponses(e, c.Responses, c.ResponseNA, "this voice exposes no filter that a test can drive with an input signal")

	// Noise floor: idle instance, 1 s.
	idle, err := c.New(-1)
	if err != nil {
		return nil, err
	}
	l, r := make([]float32, sampleRate), make([]float32, sampleRate)
	idle(l, r)
	e.measured(metricNoise, "dBFS", rmsDBFS(l, r), true)

	// CPU: a note held at middle C.
	cpu, err := cpuVoice(c)
	if err != nil {
		return nil, err
	}
	e.measured(metricCPU, "ns/block", cpu, false)
	return e, nil
}

func orDefault(s, d string) string {
	if s != "" {
		return s
	}
	return d
}

func cpuVoice(c voiceCase) (float64, error) {
	var failure error
	ns := medianNS(func() time.Duration {
		render, err := c.New(60)
		if err != nil {
			failure = err
			return 0
		}
		l, r := make([]float32, blockFrames), make([]float32, blockFrames)
		render(l, r) // warm the caches
		start := time.Now()
		for i := 0; i < cpuBlocksPerRn; i++ {
			render(l, r)
		}
		return time.Since(start)
	})
	return ns, failure
}

// ---- effects ----

type processor func(l, r float32) (float32, float32)

type fxCase struct {
	Name        string
	Description string
	New         func() (processor, error)
	// Settle is how many frames to run before analysis (reverbs, delays).
	Settle     int
	Responses  []responseCase
	ResponseNA string
	// THDNNA, if set, is why THD+N does not apply.
	THDNNA string
	// Custom replaces the generic measurements (offline operations).
	Custom func(e *entry) error
}

func fxBlock(p processor, in []float32) block {
	return func(l, r []float32) {
		for i := range l {
			l[i], r[i] = p(in[i], in[i])
		}
	}
}

func measureFX(c fxCase) (*entry, error) {
	e := newEntry("fx/"+c.Name, "effect", c.Description)
	e.notApplicable(metricAliasing, "an effect has no pitch of its own; distortion products it folds back are counted in thdn_db, because a folded harmonic of the 1 kHz test sine lands off the harmonic series and counts as noise")
	if c.Custom != nil {
		if err := c.Custom(e); err != nil {
			return nil, err
		}
		return e, nil
	}

	if c.THDNNA != "" {
		e.notApplicable(metricTHDN, c.THDNNA)
	} else {
		detail := map[string]float64{}
		worst := dbFloor
		for _, level := range thdLevels {
			p, err := c.New()
			if err != nil {
				return nil, err
			}
			total := c.Settle + thdFrames
			x := make([]float64, thdFrames)
			for i := 0; i < total; i++ {
				v := float32(level.Amplitude * math.Sin(2*math.Pi*thdFrequency*float64(i)/sampleRate))
				y, _ := p(v, v)
				if i >= c.Settle {
					x[i-c.Settle] = float64(y)
				}
			}
			// The fit absorbs the effect's gain and phase; the sine starts at
			// phase zero at sample 0, and Settle is a multiple of the period.
			db := thdnDB(x)
			detail[level.Label+"_db"] = round(db)
			worst = math.Max(worst, db)
		}
		m := e.measured(metricTHDN, "dB", worst, true)
		m.Detail = detail
	}

	applyResponses(e, c.Responses, c.ResponseNA, "not a filter with a published design curve")

	// Noise floor: 1 s of digital silence from a fresh instance.
	p, err := c.New()
	if err != nil {
		return nil, err
	}
	l, r := make([]float32, sampleRate), make([]float32, sampleRate)
	for i := range l {
		l[i], r[i] = p(0, 0)
	}
	e.measured(metricNoise, "dBFS", rmsDBFS(l, r), true)

	// CPU: steady -12 dBFS sine.
	in := make([]float32, blockFrames)
	for i := range in {
		in[i] = float32(math.Pow(10, -12.0/20) * math.Sin(2*math.Pi*thdFrequency*float64(i)/sampleRate))
	}
	var failure error
	ns := medianNS(func() time.Duration {
		p, err := c.New()
		if err != nil {
			failure = err
			return 0
		}
		render := fxBlock(p, in)
		out1, out2 := make([]float32, blockFrames), make([]float32, blockFrames)
		render(out1, out2)
		start := time.Now()
		for i := 0; i < cpuBlocksPerRn; i++ {
			render(out1, out2)
		}
		return time.Since(start)
	})
	if failure != nil {
		return nil, failure
	}
	e.measured(metricCPU, "ns/block", ns, false)
	return e, nil
}

// ---- frequency response ----

// responseCase measures one filter setting at a list of frequencies.
type responseCase struct {
	Name     string
	Hz       []float64
	Expected func(hz float64) float64 // dB
	// NominalHz, when set, is the cutoff the control asks for. The report
	// then lists where the design curve actually crosses -3 dB.
	NominalHz float64
	Measure   func(hz float64) (float64, error)
}

func applyResponses(e *entry, cases []responseCase, na, defaultNA string) {
	if len(cases) == 0 {
		e.notApplicable(metricResponse, orDefault(na, defaultNA))
		return
	}
	worst := 0.0
	var pts []point
	for _, c := range cases {
		for _, hz := range c.Hz {
			want := c.Expected(hz)
			got, err := c.Measure(hz)
			if err != nil {
				panic(fmt.Sprintf("%s %s at %.0f Hz: %v", e.Name, c.Name, hz, err))
			}
			diff := got - want
			pts = append(pts, point{Case: c.Name, Hz: hz, ExpectedDB: round(want), MeasuredDB: round(got), ErrorDB: round(diff)})
			worst = math.Max(worst, math.Abs(diff))
		}
	}
	sort.SliceStable(pts, func(i, j int) bool { return pts[i].Case < pts[j].Case })
	m := e.measured(metricResponse, "dB", worst, true)
	m.Points = pts
	for _, c := range cases {
		if c.NominalHz > 0 {
			if m.Detail == nil {
				m.Detail = map[string]float64{}
			}
			m.Detail[c.Name+"_nominal_cutoff_hz"] = c.NominalHz
			m.Detail[c.Name+"_minus3db_hz"] = round(minus3dB(c))
		}
	}
}

// toneGainDB drives a sine of amplitude amp at hz through step (called once
// per frame with the input, returning the output), discards settle frames,
// fits the output amplitude over n frames and returns the gain in dB.
// The fit absorbs phase, so the start frame does not matter.
func toneGainDB(hz, amp float64, settle, n int, step func(float32) float32) float64 {
	x := make([]float64, n)
	w := 2 * math.Pi * hz / sampleRate
	for i := 0; i < settle+n; i++ {
		y := step(float32(amp * math.Sin(w*float64(i))))
		if i >= settle {
			x[i-settle] = float64(y)
		}
	}
	// fitTone indexes from zero, so shift the phase reference by settle.
	amplitude, _ := fitTone(x, hz)
	return 20 * math.Log10(amplitude/amp)
}

// minus3dB finds where the design curve of a lowpass or highpass case falls
// 3 dB below its passband gain, by bisection from 10 Hz to 0.45 of the sample
// rate. The passband gain is the curve at 10 Hz (lowpass) or 0.45 fs (highpass).
func minus3dB(c responseCase) float64 {
	lo, hi := 10.0, .45*sampleRate
	lowpass := c.Expected(lo) > c.Expected(hi)
	reference := c.Expected(hi)
	if lowpass {
		reference = c.Expected(lo)
	}
	for i := 0; i < 60; i++ {
		mid := math.Sqrt(lo * hi)
		above := c.Expected(mid)-reference > -3
		if above == lowpass {
			lo = mid
		} else {
			hi = mid
		}
	}
	return lo
}
