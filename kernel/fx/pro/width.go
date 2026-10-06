package pro

import "math"

type WidthParams struct {
	Enabled    bool
	Amount     float64 // 0 = mono, 1 = unity, 2 = twice the side level
	BassMonoHz float64 // 0 bypasses the side highpass; otherwise 20..500 Hz
}

// Width preserves the mid signal exactly. A two-pole Butterworth side
// highpass optionally centers bass without filtering the mid signal.
type Width struct {
	amount float64
	filter biquad
	bass   bool
}

func NewWidth(sampleRate int, p WidthParams) (*Width, error) {
	if !validRate(sampleRate) {
		return nil, ErrSampleRate
	}
	if !finite(p.Amount) || !finite(p.BassMonoHz) || p.Amount < 0 || p.Amount > 2 ||
		p.BassMonoHz != 0 && (p.BassMonoHz < 20 || p.BassMonoHz > 500) {
		return nil, ErrParams
	}
	w := &Width{amount: p.Amount, bass: p.BassMonoHz != 0}
	if w.bass {
		w.filter = designBand(sampleRate, Band{Type: Highpass, FrequencyHz: p.BassMonoHz, Q: 1 / math.Sqrt2})
	}
	return w, nil
}

func (w *Width) Process(left, right float32) (float32, float32) {
	if w.amount == 1 && !w.bass {
		return left, right
	}
	mid := (float64(left) + float64(right)) * .5
	side := (float64(left) - float64(right)) * .5
	if w.bass {
		side = w.filter.process(side, 0)
	}
	side *= w.amount
	return float32(mid + side), float32(mid - side)
}

func (w *Width) Reset() {
	w.filter.z1, w.filter.z2 = [2]float64{}, [2]float64{}
}

func (w *Width) LatencyFrames() int { return 0 }
