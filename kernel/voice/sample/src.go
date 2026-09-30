package sample

import "math"

const phases = 1024

// Each bank protects the output Nyquist at its largest admitted ratio. The
// smaller ratios use fewer taps; coefficients are shared by every voice.
type sincBank struct {
	ratio float64
	taps  int
	coeff []float32
}

var banks = [...]sincBank{
	{ratio: 1, taps: 96},
	{ratio: 1.125, taps: 108},
	{ratio: 1.25, taps: 120},
	{ratio: 1.5, taps: 144},
	{ratio: 2, taps: 192},
	{ratio: 3, taps: 288},
	{ratio: 4, taps: 384},
	{ratio: 8, taps: 768},
}

func init() {
	const beta = 12.0
	normalizer := besselI0(beta)
	for b := range banks {
		bank := &banks[b]
		bank.coeff = make([]float32, (phases+1)*bank.taps)
		cutoff := .45 / bank.ratio
		for p := 0; p <= phases; p++ {
			row := bank.coeff[p*bank.taps : (p+1)*bank.taps]
			var sum float64
			for tap := range row {
				x := float64(tap-bank.taps/2+1) - float64(p)/phases
				u := 2 * x / float64(bank.taps)
				if u*u >= 1 {
					continue
				}
				window := besselI0(beta*math.Sqrt(1-u*u)) / normalizer
				value := 2 * cutoff
				if x != 0 {
					value = math.Sin(2*math.Pi*cutoff*x) / (math.Pi * x)
				}
				row[tap] = float32(value * window)
				sum += float64(row[tap])
			}
			for tap := range row {
				row[tap] = float32(float64(row[tap]) / sum)
			}
		}
	}
}

func besselI0(x float64) float64 {
	sum, term := 1.0, 1.0
	for k := 1; k <= 32; k++ {
		term *= (x * x / 4) / float64(k*k)
		sum += term
	}
	return sum
}

func bankFor(ratio float64) *sincBank {
	for i := range banks {
		// Pitch exponentiation can put a mathematically exact bank boundary
		// one ULP above it. This tolerance changes only the bank, not phase.
		if ratio <= banks[i].ratio*(1+1e-14) {
			return &banks[i]
		}
	}
	return &banks[len(banks)-1]
}

func (v *Voice) interpolate() (float64, float64) {
	bank := v.bank
	base := int(v.phase)
	position := (v.phase - float64(base)) * phases
	index := int(position)
	fraction := position - float64(index)
	row := bank.coeff[index*bank.taps : (index+1)*bank.taps]
	next := bank.coeff[(index+1)*bank.taps : (index+2)*bank.taps]
	first := base - bank.taps/2 + 1
	var left, right float64
	// Explicit float64 conversions round products before addition, preventing
	// native FMA contraction. Taps always accumulate in ascending source order.
	if first >= v.region.Start && first+bank.taps <= v.region.End &&
		(!v.region.Loop || (first >= v.region.LoopStart && first+bank.taps <= v.region.LoopEnd) ||
			(!v.looped && first+bank.taps <= v.region.LoopEnd)) {
		l := v.region.Left[first : first+bank.taps]
		if len(v.region.Right) == 0 {
			for i, a := range row {
				c := float64(a) + float64((float64(next[i])-float64(a))*fraction)
				left += float64(float64(l[i]) * c)
			}
			return left, left
		}
		r := v.region.Right[first : first+bank.taps]
		for i, a := range row {
			c := float64(a) + float64((float64(next[i])-float64(a))*fraction)
			left += float64(float64(l[i]) * c)
			right += float64(float64(r[i]) * c)
		}
		return left, right
	}
	for i, a := range row {
		c := float64(a) + float64((float64(next[i])-float64(a))*fraction)
		l, r := v.frame(first + i)
		left += float64(float64(l) * c)
		right += float64(float64(r) * c)
	}
	return left, right
}

func (v *Voice) frame(index int) (float32, float32) {
	r := &v.region
	if r.Loop && (index >= r.LoopEnd || (v.looped && index < r.LoopStart)) {
		length := r.LoopEnd - r.LoopStart
		index = (index - r.LoopStart) % length
		if index < 0 {
			index += length
		}
		index += r.LoopStart
	}
	if index < r.Start || index >= r.End {
		return 0, 0
	}
	left := r.Left[index]
	if len(r.Right) == 0 {
		return left, left
	}
	return left, r.Right[index]
}
