package modulate

// Wave is an LFO waveform.
type Wave uint8

// The LFO waveforms.
const (
	Sine Wave = iota
	Triangle
	Square
	SampleHold
)

// Hash domains of the sources (spec 24, "Seeding"). A modulator's track byte is
// always 0xFF and its slot byte is its index in the table.
const (
	DomainLFO  = 1
	DomainSway = 2
)

// Sin returns sin(2*pi*phase) in Q30 from the quarter-wave table, linearly
// interpolated. The error is below 3e-7 of full scale.
func Sin(phase uint32) int32 {
	x := phase & (1<<30 - 1)
	if phase>>30&1 == 1 {
		x = 1<<30 - x
	}
	i, f := x>>20, int64(x&(1<<20-1))
	v := sineQuarter[1024] // x is 1<<30 only when mirroring phase 0.25 or 0.75
	if i < 1024 {
		a, b := int64(sineQuarter[i]), int64(sineQuarter[i+1])
		v = int32(a + (b-a)*f>>20)
	}
	if phase>>31 == 1 {
		return -v
	}
	return v
}

// triangle rises from 0 to +One at a quarter cycle, falls to -One at three
// quarters and returns to 0.
func triangle(phase uint32) int32 {
	q := int64(phase>>30) & 3
	x := int64(phase & (1<<30 - 1)) // Q30 inside the quarter
	switch q {
	case 0:
		return int32(x)
	case 1:
		return int32(One - x)
	case 2:
		return int32(-x)
	}
	return int32(x - One)
}

// LFO returns the value of an LFO at a position. seed is the project seed and
// index the source's slot in the table (used by sample-hold only).
func LFO(wave Wave, seed uint32, index uint8, p Position) int32 {
	switch wave {
	case Sine:
		return Sin(p.Phase)
	case Triangle:
		return triangle(p.Phase)
	case Square:
		if p.Phase < 1<<31 {
			return One
		}
		return -One
	}
	return draw(Hash(seed, DomainLFO, 0xFF, index, 0, p.Cycle))
}

// LFOFitted repeats sample-and-hold values after cycles fitted to a song loop.
// A zero cycle count leaves the source on its ordinary unbounded timeline.
func LFOFitted(wave Wave, seed uint32, index uint8, p Position, cycles uint64) int32 {
	if cycles != 0 {
		p.Cycle %= cycles
	}
	return LFO(wave, seed, index, p)
}

// Sway returns the stateless smooth random source: octaves layers, layer o at
// 2^o times the base rate and weight 2^(octaves-1-o)/(2^octaves-1). Each layer
// draws h(k) at its period boundaries and joins h(k) to h(k+1) with smoothstep.
func Sway(seed uint32, index uint8, octaves uint, p Position) int32 {
	if octaves < 1 {
		octaves = 1
	}
	if octaves > 4 {
		octaves = 4
	}
	if octaves == 4 {
		q0, q1, q2, q3 := p, p.Scale(1), p.Scale(2), p.Scale(3)
		a0 := draw(Hash(seed, DomainSway, 0xFF, index, 0, q0.Cycle))
		b0 := draw(Hash(seed, DomainSway, 0xFF, index, 0, q0.Cycle+1))
		a1 := draw(Hash(seed, DomainSway, 0xFF, index, 1, q1.Cycle))
		b1 := draw(Hash(seed, DomainSway, 0xFF, index, 1, q1.Cycle+1))
		a2 := draw(Hash(seed, DomainSway, 0xFF, index, 2, q2.Cycle))
		b2 := draw(Hash(seed, DomainSway, 0xFF, index, 2, q2.Cycle+1))
		a3 := draw(Hash(seed, DomainSway, 0xFF, index, 3, q3.Cycle))
		b3 := draw(Hash(seed, DomainSway, 0xFF, index, 3, q3.Cycle+1))
		sum := int64(lerp(a0, b0, smooth(q0.Phase)))<<3 +
			int64(lerp(a1, b1, smooth(q1.Phase)))<<2 +
			int64(lerp(a2, b2, smooth(q2.Phase)))<<1 +
			int64(lerp(a3, b3, smooth(q3.Phase)))
		return int32(sum / 15)
	}
	var sum int64
	for o := uint(0); o < octaves; o++ {
		q := p.Scale(o)
		a := draw(Hash(seed, DomainSway, 0xFF, index, uint8(o), q.Cycle))
		b := draw(Hash(seed, DomainSway, 0xFF, index, uint8(o), q.Cycle+1))
		sum += int64(lerp(a, b, smooth(q.Phase))) << (octaves - 1 - o)
	}
	return int32(sum / (1<<octaves - 1))
}

// SwayFitted wraps each octave's random endpoints at the fitted loop boundary.
// Without this, the last segment approaches a different value from the first.
// A zero cycle count keeps the ordinary unbounded sequence.
func SwayFitted(seed uint32, index uint8, octaves uint, p Position, cycles uint64) int32 {
	if cycles == 0 {
		return Sway(seed, index, octaves, p)
	}
	if octaves < 1 {
		octaves = 1
	}
	if octaves > 4 {
		octaves = 4
	}
	p.Cycle %= cycles
	var sum int64
	for o := uint(0); o < octaves; o++ {
		q := p.Scale(o)
		a := draw(Hash(seed, DomainSway, 0xFF, index, uint8(o), q.Cycle))
		next := q.Cycle + 1
		if p.Cycle == cycles-1 && uint64(p.Phase)>>(32-o) == (uint64(1)<<o)-1 {
			next = 0
		}
		b := draw(Hash(seed, DomainSway, 0xFF, index, uint8(o), next))
		sum += int64(lerp(a, b, smooth(q.Phase))) << (octaves - 1 - o)
	}
	return int32(sum / (1<<octaves - 1))
}

// Shape is a contour segment's interpolation.
type Shape uint8

// The contour shapes. A point's shape governs the segment that arrives at it.
// Exponential is linear here, as it is in automation: a route maps the value
// into the target's control space, so a straight line there is the curve the
// ear follows. The word stays so a score can say what it means.
const (
	Linear Shape = iota
	Step
	Smooth
	Exponential
)

// Point is a contour breakpoint: At is a Q32 fraction of the period in
// [0, 1<<32], Value a Q30 level.
type Point struct {
	At    uint64
	Value int32
	Shape Shape
}

// Contour evaluates a periodic breakpoint curve. points must start at 0, end at
// 1<<32 and increase; the project compiler guarantees it.
func Contour(points []Point, p Position) int32 {
	n := len(points)
	if n == 0 {
		return 0
	}
	t := uint64(p.Phase)
	i := 0
	for i+2 < n && points[i+1].At <= t {
		i++
	}
	if n == 1 {
		return points[0].Value
	}
	a, b := points[i], points[i+1]
	width := b.At - a.At
	if width == 0 || t < a.At {
		return a.Value
	}
	u := (t - a.At) << 32 / width
	switch b.Shape {
	case Step:
		return a.Value
	case Smooth:
		u = smooth(uint32(u))
	}
	return lerp(a.Value, b.Value, u)
}

// Follower is the envelope source's state: a one-pole follower of the peak
// that the engine measures over the frames before each grid point. It starts at
// zero at load, at a seek and at a loop wrap.
type Follower struct{ Level int32 }

// Reset returns the follower to zero, as at load, at a seek and at a loop wrap.
func (f *Follower) Reset() { f.Level = 0 }

// Step moves the level toward peak (Q30, at least 0) by the attack coefficient
// when rising and the release coefficient when falling. Both are Q32
// fractions of the remaining distance per grid point.
func (f *Follower) Step(peak int32, attack, release uint32) int32 {
	if peak < 0 {
		peak = 0
	}
	coef := uint64(release)
	if peak > f.Level {
		coef = uint64(attack)
	}
	f.Level += int32((int64(peak) - int64(f.Level)) * int64(coef) >> 32)
	if f.Level < 0 {
		f.Level = 0
	}
	return f.Level
}
