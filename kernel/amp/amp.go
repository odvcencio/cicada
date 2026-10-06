// Package amp implements a pinned causal neural amplifier with integer inference.
// The original trained model uses an eight-tap convolution, four tanh channels,
// and a learned linear readout. Weights and training data are CC0-1.0.
package amp

const (
	Taps   = 8
	Hidden = 4
)

// Model owns its complete causal history. Its zero value is ready to process.
// Process and ProcessFloat allocate nothing and preserve sample order across blocks.
type Model struct {
	history [Taps]int32
}

func (m *Model) Reset() { *m = Model{} }

// Process consumes Q15 audio and Q12 drive. Drive is bounded to [0,8].
// All products, accumulations, shifts, saturation and table interpolation use
// integers; output is Q15 and is bit-identical on native and WebAssembly targets.
func (m *Model) Process(input, drive int32) int32 {
	input = bound(input, -32768, 32767)
	drive = bound(drive, 0, 32768)
	x := bound((input*drive)>>12, -32768, 32767)
	for i := Taps - 1; i > 0; i-- {
		m.history[i] = m.history[i-1]
	}
	m.history[0] = x
	// The pinned rows and readout each have an absolute-weight sum <32767.
	// Q15 products therefore stay below 2^30; TestPinnedWeights proves it.
	var out int32
	for channel := 0; channel < Hidden; channel++ {
		var sum int32
		for tap := 0; tap < Taps; tap++ {
			sum += m.history[tap] * int32(convolutionWeights[channel][tap])
		}
		out += activate(sum>>15) * int32(outputWeights[channel])
	}
	return bound(out>>14, -32768, 32767)
}

// ProcessFloat bounds non-finite controls before conversion. Power-of-two
// scaling makes the float32 bridge exact; no floating inference runs here.
func (m *Model) ProcessFloat(input, drive float32) float32 {
	return float32(m.Process(quantize(input, -1, 32767.0/32768, 32768), quantize(drive, 0, 8, 4096))) / 32768
}

func quantize(x, lo, hi, scale float32) int32 {
	if x != x { // NaN becomes silence/zero drive.
		return 0
	}
	if x < lo {
		x = lo
	}
	if x > hi {
		x = hi
	}
	return int32(x * scale)
}

func activate(x int32) int32 {
	negative := x < 0
	if negative {
		x = -x
	}
	if x >= 16384 {
		x = int32(tanhTable[256])
	} else {
		index, fraction := x>>6, x&63
		a, b := int32(tanhTable[index]), int32(tanhTable[index+1])
		x = a + ((b-a)*fraction)>>6
	}
	if negative {
		return -x
	}
	return x
}

func bound(x, lo, hi int32) int32 {
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}
