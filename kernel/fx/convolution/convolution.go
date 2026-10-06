// Package convolution provides bounded stereo partitioned FFT convolution.
// Prepare the impulse before rendering; Process and Reset do not allocate.
package convolution

import (
	"errors"
	"math"
)

// MaxFrames bounds one impulse to less than 22 seconds at 48 kHz. New also
// limits it to twelve seconds at the selected sample rate.
const MaxFrames = 1 << 20

type fft struct {
	order []int
	roots []complex128
}

func newFFT(n int) fft {
	f := fft{order: make([]int, n), roots: make([]complex128, n/2)}
	for i, j := 0, 0; i < n; i++ {
		f.order[i] = j
		bit := n >> 1
		for j&bit != 0 {
			j ^= bit
			bit >>= 1
		}
		j ^= bit
	}
	for i := range f.roots {
		angle := -2 * math.Pi * float64(i) / float64(n)
		f.roots[i] = complex(math.Cos(angle), math.Sin(angle))
	}
	return f
}

func (f *fft) transform(x []complex128, inverse bool) {
	n := len(x)
	for i, j := range f.order {
		if i < j {
			x[i], x[j] = x[j], x[i]
		}
	}
	for width := 2; width <= n; width <<= 1 {
		half, stride := width/2, n/width
		for base := 0; base < n; base += width {
			for j := 0; j < half; j++ {
				root := f.roots[j*stride]
				if inverse {
					root = complex(real(root), -imag(root))
				}
				u, v := x[base+j], x[base+j+half]*root
				x[base+j], x[base+j+half] = u+v, u-v
			}
		}
	}
	if inverse {
		gain := complex(1/float64(n), 0)
		for i := range x {
			x[i] *= gain
		}
	}
}

type channel struct {
	impulse []complex128
	history []complex128
	input   []float32
	output  []float64
	overlap []float64
	work    []complex128
}

// Reverb applies each impulse channel to the matching input channel. For a
// mono impulse, pass a nil right slice to apply the same response to both.
// It returns only the wet signal; align a parallel dry path by LatencyFrames.
// One owner must render, reset, or inspect fault state at a time.
type Reverb struct {
	plan       fft
	channels   [2]channel
	block      int
	partitions int
	position   int
	head       int
	fault      bool
	tail       *tail
}

// New copies and transforms the impulse. partitionFrames must be a power of
// two from 64 through 2048. Long responses at partitions of 512 or fewer
// use a 2048-frame tail whose FFT work is spread over 128-frame jobs, keeping
// the selected head latency. Larger head partitions trade latency for CPU.
// Impulse data must already have sampleRate; this processor never resamples.
func New(sampleRate int, left, right []float32, partitionFrames int) (*Reverb, error) {
	if sampleRate < 8000 || sampleRate > 192000 || len(left) == 0 || len(left) > MaxFrames || len(left) > sampleRate*12 ||
		(len(right) != 0 && len(right) != len(left)) || partitionFrames < 64 || partitionFrames > 2048 || partitionFrames&(partitionFrames-1) != 0 {
		return nil, errors.New("invalid convolution sample rate, impulse length, or partition size")
	}
	for _, impulse := range [][]float32{left, right} {
		for _, x := range impulse {
			if !finite(float64(x)) {
				return nil, errors.New("convolution impulse contains non-finite samples")
			}
		}
	}
	if split := 4096 - partitionFrames; partitionFrames <= 512 && len(left) > split {
		var headRight, tailRight []float32
		if len(right) != 0 {
			headRight, tailRight = right[:split], right[split:]
		}
		r := prepare(left[:split], headRight, partitionFrames)
		r.tail = newTail(left[split:], tailRight)
		return r, nil
	}
	return prepare(left, right, partitionFrames), nil
}

func prepare(left, right []float32, partitionFrames int) *Reverb {
	r := &Reverb{block: partitionFrames, partitions: (len(left) + partitionFrames - 1) / partitionFrames}
	n := 2 * partitionFrames
	bins := partitionFrames + 1
	r.plan = newFFT(n)
	for i := range r.channels {
		impulse := left
		if i == 1 && len(right) != 0 {
			impulse = right
		}
		c := &r.channels[i]
		// Real input has conjugate-symmetric spectra. Keeping only the
		// independent bins halves the long-tail multiply and storage cost.
		c.impulse = make([]complex128, r.partitions*bins)
		c.history = make([]complex128, len(c.impulse))
		c.input = make([]float32, r.block)
		c.output = make([]float64, r.block)
		c.overlap = make([]float64, r.block)
		c.work = make([]complex128, n)
		for p := 0; p < r.partitions; p++ {
			clear(c.work)
			for j := 0; j < r.block && p*r.block+j < len(impulse); j++ {
				c.work[j] = complex(float64(impulse[p*r.block+j]), 0)
			}
			r.plan.transform(c.work, false)
			copy(c.impulse[p*bins:(p+1)*bins], c.work[:bins])
		}
		clear(c.work)
	}
	return r
}

func finite(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) }

func (r *Reverb) LatencyFrames() int { return r.block }
func (r *Reverb) Fault() bool        { return r.fault }

// Reset discards every input, output and overlap tail without reallocating
// the prepared impulse. Subsequent zero input is exactly silent.
func (r *Reverb) Reset() {
	for i := range r.channels {
		c := &r.channels[i]
		clear(c.history)
		clear(c.input)
		clear(c.output)
		clear(c.overlap)
		clear(c.work)
	}
	r.position, r.head, r.fault = 0, 0, false
	if r.tail != nil {
		r.tail.reset()
	}
}

// Process is allocation-free. A non-finite input or overflowing output latches
// a silent fault until Reset. All FFT work uses precomputed twiddle factors.
func (r *Reverb) Process(left, right float32) (float32, float32) {
	if r.fault {
		return 0, 0
	}
	if !finite(float64(left)) || !finite(float64(right)) {
		r.fault = true
		return 0, 0
	}
	p := r.position
	l, rr := r.channels[0].output[p], r.channels[1].output[p]
	if r.tail != nil {
		tl, tr := r.tail.process(left, right)
		l, rr = l+tl, rr+tr
		if r.tail.fault {
			r.fault = true
			return 0, 0
		}
	}
	if !finite(l) || !finite(rr) || math.Abs(l) > math.MaxFloat32 || math.Abs(rr) > math.MaxFloat32 {
		r.fault = true
		return 0, 0
	}
	r.channels[0].input[p], r.channels[1].input[p] = left, right
	r.position++
	if r.position == r.block {
		r.render()
		r.position = 0
	}
	return float32(l), float32(rr)
}

func (r *Reverb) render() {
	n := 2 * r.block
	bins := r.block + 1
	for i := range r.channels {
		c := &r.channels[i]
		x := c.history[r.head*bins : (r.head+1)*bins]
		for j, value := range c.input {
			c.work[j] = complex(float64(value), 0)
		}
		clear(c.work[r.block:])
		r.plan.transform(c.work, false)
		copy(x, c.work[:bins])
		clear(c.work)
		for p, slot := 0, r.head; p < r.partitions; p++ {
			h := c.impulse[p*bins : (p+1)*bins]
			x = c.history[slot*bins : (slot+1)*bins]
			for j := 0; j < bins; j++ {
				c.work[j] += h[j] * x[j]
			}
			slot--
			if slot < 0 {
				slot = r.partitions - 1
			}
		}
		for j := 1; j < r.block; j++ {
			x := c.work[j]
			c.work[n-j] = complex(real(x), -imag(x))
		}
		r.plan.transform(c.work, true)
		for j := range c.output {
			c.output[j] = real(c.work[j]) + c.overlap[j]
			c.overlap[j] = real(c.work[r.block+j])
		}
	}
	r.head++
	if r.head == r.partitions {
		r.head = 0
	}
}
