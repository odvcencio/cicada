package acid

import "math"

// TableOscillator shares the acid voice's immutable harmonic-limited tables.
// Construct it at the output sample rate, even when evaluating at 2x that rate.
// Selection is cached for stationary notes; evaluation allocates nothing.
type TableOscillator struct {
	bank      *oscillatorBank
	selection oscillatorSelection
	frequency float64
}

func NewTableOscillator(sampleRate int) (*TableOscillator, error) {
	if _, ok := oscillatorBankIndex(sampleRate); !ok {
		return nil, Error("unsupported oscillator sample rate")
	}
	return &TableOscillator{bank: bankForSampleRate(sampleRate)}, nil
}

func (o *TableOscillator) selectFrequency(frequency float64) {
	if o.selection.low == nil || frequency != o.frequency {
		o.frequency = frequency
		o.selection = o.bank.selectTables(math.Log2(frequency))
	}
}

func (o *TableOscillator) Saw(phase, frequency float64) float64 {
	if frequency <= 0 {
		return 0
	}
	o.selectFrequency(frequency)
	return o.selection.saw(phase)
}

func (o *TableOscillator) Pulse(phase, frequency, width float64) float64 {
	if frequency <= 0 {
		return 0
	}
	o.selectFrequency(frequency)
	return o.selection.pulse(phase, width)
}
