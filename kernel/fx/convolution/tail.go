package convolution

import "math"

const tailFrames = 2048
const jobFrames = 128

const (
	fillInput = iota
	reverseForward
	forwardFFT
	saveSpectrum
	multiplySpectra
	mirrorSpectrum
	reverseInverse
	inverseFFT
	saveOutput
)

// tail completes one partition's computation during the next input partition.
// Its two-partition latency lets a short head cover the start of the impulse.
// FFT butterflies and spectral products share a fixed operation budget; no
// long-tail FFT or full frequency-domain sum runs in one host callback.
type tail struct {
	convolver *Reverb
	input     [2][2][]float32
	output    [2][2][]float64
	write     int
	read      int
	position  int
	workInput int
	workOut   int
	phase     int
	channel   int
	cursor    int
	width     int
	partition int
	slot      int
	budget    int
	active    bool
	ready     bool
	fault     bool
}

func newTail(left, right []float32) *tail {
	t := &tail{convolver: prepare(left, right, tailFrames)}
	for bank := range t.input {
		for c := range t.input[bank] {
			t.input[bank][c] = make([]float32, tailFrames)
			t.output[bank][c] = make([]float64, tailFrames)
		}
	}
	// Per channel: forward fill/reversal/FFT/copy + inverse mirror/reversal/
	// FFT/output. A paired stereo multiply costs two budget units. The extra
	// margin covers rounding when a job ends with one unit before a multiply.
	n, bins, stages := 2*tailFrames, tailFrames+1, 12
	steps := 2*(n+n+(n/2)*stages+bins+(tailFrames-1)+n+(n/2)*stages+tailFrames) + 2*t.convolver.partitions*bins
	t.budget = (steps+15)/16 + 32
	return t
}

func (t *tail) reset() {
	t.convolver.Reset()
	for bank := range t.input {
		for c := range t.input[bank] {
			clear(t.input[bank][c])
			clear(t.output[bank][c])
		}
	}
	t.write, t.read, t.position = 0, 0, 0
	t.workInput, t.workOut = 0, 0
	t.phase, t.channel, t.cursor, t.width, t.partition, t.slot = 0, 0, 0, 0, 0, 0
	t.active, t.ready, t.fault = false, false, false
}

func (t *tail) process(left, right float32) (float64, float64) {
	p := t.position
	l, rr := t.output[t.read][0][p], t.output[t.read][1][p]
	t.input[t.write][0][p], t.input[t.write][1][p] = left, right
	t.position++
	if t.position%jobFrames == 0 && t.active {
		t.job()
	}
	if t.position == tailFrames {
		if t.active { // A bounded job must finish before its output is due.
			t.fault = true
			return 0, 0
		}
		if t.ready {
			t.read = t.workOut
			t.ready = false
		}
		t.workInput, t.workOut = t.write, t.read^1
		t.write ^= 1
		t.phase, t.channel, t.cursor, t.width = fillInput, 0, 0, 2
		t.active = true
		t.position = 0
	}
	return l, rr
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (t *tail) advance(phase int) {
	t.phase, t.cursor = phase, 0
}

func (t *tail) job() {
	r := t.convolver
	n, bins := 2*tailFrames, tailFrames+1
	budget := t.budget
	for budget > 0 && t.active {
		c := &r.channels[t.channel]
		work := c.work
		switch t.phase {
		case fillInput:
			count := min(budget, n-t.cursor)
			for j := t.cursor; j < t.cursor+count; j++ {
				var x float32
				if j < tailFrames {
					x = t.input[t.workInput][t.channel][j]
				}
				work[j] = complex(float64(x), 0)
			}
			t.cursor += count
			budget -= count
			if t.cursor == n {
				t.advance(reverseForward)
			}
		case reverseForward, reverseInverse:
			count := min(budget, n-t.cursor)
			for j := t.cursor; j < t.cursor+count; j++ {
				if k := r.plan.order[j]; j < k {
					work[j], work[k] = work[k], work[j]
				}
			}
			t.cursor += count
			budget -= count
			if t.cursor == n {
				if t.phase == reverseForward {
					t.advance(forwardFFT)
				} else {
					t.advance(inverseFFT)
				}
				t.width = 2
			}
		case forwardFFT, inverseFFT:
			half, stride := t.width/2, n/t.width
			count := min(budget, n/2-t.cursor)
			for cursor := t.cursor; cursor < t.cursor+count; cursor++ {
				j := cursor & (half - 1)
				base := (cursor - j) * 2
				root := r.plan.roots[j*stride]
				if t.phase == inverseFFT {
					root = complex(real(root), -imag(root))
				}
				u, v := work[base+j], work[base+j+half]*root
				work[base+j], work[base+j+half] = u+v, u-v
			}
			t.cursor += count
			budget -= count
			if t.cursor == n/2 {
				t.cursor = 0
				t.width *= 2
				if t.width > n {
					if t.phase == forwardFFT {
						t.advance(saveSpectrum)
					} else {
						t.advance(saveOutput)
					}
				}
			}
		case saveSpectrum:
			count := min(budget, bins-t.cursor)
			history := c.history[r.head*bins : (r.head+1)*bins]
			for j := t.cursor; j < t.cursor+count; j++ {
				history[j], work[j] = work[j], 0
			}
			t.cursor += count
			budget -= count
			if t.cursor == bins {
				if t.channel == 0 {
					t.channel = 1
					t.advance(fillInput)
				} else {
					t.channel, t.partition, t.slot = 0, 0, r.head
					t.advance(multiplySpectra)
				}
			}
		case multiplySpectra:
			count := min(budget/2, bins-t.cursor)
			if count == 0 {
				return
			}
			l, rr := &r.channels[0], &r.channels[1]
			startH, startX := t.partition*bins, t.slot*bins
			for j := t.cursor; j < t.cursor+count; j++ {
				l.work[j] += l.impulse[startH+j] * l.history[startX+j]
				rr.work[j] += rr.impulse[startH+j] * rr.history[startX+j]
			}
			t.cursor += count
			budget -= 2 * count
			if t.cursor == bins {
				t.cursor = 0
				t.partition++
				t.slot--
				if t.slot < 0 {
					t.slot = r.partitions - 1
				}
				if t.partition == r.partitions {
					t.advance(mirrorSpectrum)
				}
			}
		case mirrorSpectrum:
			count := min(budget, tailFrames-1-t.cursor)
			for j := t.cursor + 1; j <= t.cursor+count; j++ {
				x := work[j]
				work[n-j] = complex(real(x), -imag(x))
			}
			t.cursor += count
			budget -= count
			if t.cursor == tailFrames-1 {
				t.advance(reverseInverse)
			}
		case saveOutput:
			count := min(budget, tailFrames-t.cursor)
			output := t.output[t.workOut][t.channel]
			for j := t.cursor; j < t.cursor+count; j++ {
				output[j] = real(work[j])/float64(n) + c.overlap[j]
				c.overlap[j] = real(work[tailFrames+j]) / float64(n)
				if math.IsNaN(output[j]) || math.IsInf(output[j], 0) {
					t.fault = true
				}
			}
			t.cursor += count
			budget -= count
			if t.cursor == tailFrames {
				if t.channel == 0 {
					t.channel = 1
					t.advance(mirrorSpectrum)
				} else {
					t.active, t.ready = false, true
					r.head++
					if r.head == r.partitions {
						r.head = 0
					}
				}
			}
		}
	}
}
