package convolution

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
	"time"
)

func direct(input, impulse []float32, n int) []float64 {
	result := make([]float64, n)
	for i, x := range input {
		for j, h := range impulse {
			if i+j < n {
				result[i+j] += float64(x) * float64(h)
			}
		}
	}
	return result
}

func TestDirectConvolutionAndPartitionIndependence(t *testing.T) {
	rng := rand.New(rand.NewSource(17))
	h := [2][]float32{make([]float32, 1037), make([]float32, 1037)}
	input := [2][]float32{make([]float32, 783), make([]float32, 783)}
	for c := range h {
		for i := range h[c] {
			h[c][i] = (rng.Float32() - .5) * .04
		}
		for i := range input[c] {
			input[c][i] = rng.Float32() - .5
		}
	}
	n := len(input[0]) + len(h[0]) - 1
	want := [2][]float64{direct(input[0], h[0], n), direct(input[1], h[1], n)}
	for _, block := range []int{64, 128, 256, 512, 2048} {
		t.Run(fmt.Sprint(block), func(t *testing.T) {
			r, err := New(48000, h[0], h[1], block)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < n+r.LatencyFrames()+16; i++ {
				var l, rr float32
				if i < len(input[0]) {
					l, rr = input[0][i], input[1][i]
				}
				l, rr = r.Process(l, rr)
				for c, actual := range []float32{l, rr} {
					var expected float64
					j := i - r.LatencyFrames()
					if j >= 0 && j < n {
						expected = want[c][j]
					}
					if delta := math.Abs(float64(actual) - expected); delta > 1e-5 {
						t.Fatalf("frame %d channel %d delta %g", i, c, delta)
					}
				}
			}
		})
	}
}

func TestMonoImpulseLatencyAndReset(t *testing.T) {
	r, err := New(48000, []float32{1, .5, -.25}, nil, 128)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 300; i++ {
		var in float32
		if i == 0 {
			in = 1
		}
		l, rr := r.Process(in, -in)
		var expected float32
		if j := i - 128; j >= 0 && j < 3 {
			expected = []float32{1, .5, -.25}[j]
		}
		if math.Abs(float64(l-expected)) > 1e-7 || l != -rr {
			t.Fatalf("frame %d: %g %g, expected %g", i, l, rr, expected)
		}
	}
	r.Reset()
	for i := 0; i < 300; i++ {
		if l, rr := r.Process(0, 0); l != 0 || rr != 0 {
			t.Fatal("reset retains a tail")
		}
	}
}

func TestValidationAndFault(t *testing.T) {
	for _, setup := range []struct {
		rate, block int
		left, right []float32
	}{
		{48000, 128, nil, nil}, {1, 128, []float32{1}, nil},
		{48000, 100, []float32{1}, nil}, {48000, 128, []float32{1}, []float32{1, 2}},
		{48000, 128, []float32{float32(math.NaN())}, nil},
	} {
		if _, err := New(setup.rate, setup.left, setup.right, setup.block); err == nil {
			t.Fatal("invalid configuration accepted")
		}
	}
	r, _ := New(48000, []float32{1}, nil, 64)
	if l, rr := r.Process(float32(math.Inf(1)), 0); l != 0 || rr != 0 || !r.Fault() {
		t.Fatal("non-finite input did not latch a silent fault")
	}
	r.Reset()
	if r.Fault() {
		t.Fatal("reset did not clear the fault")
	}
}

func TestProcessAllocationFree(t *testing.T) {
	r, _ := New(48000, make([]float32, 48000), nil, 128)
	if got := testing.AllocsPerRun(3, func() {
		for i := 0; i < 8192; i++ {
			r.Process(.1, -.1)
		}
		r.Reset()
	}); got != 0 {
		t.Fatalf("%g render/reset allocations", got)
	}
}

func TestNonuniformTailMatchesDirect(t *testing.T) {
	rng := rand.New(rand.NewSource(29))
	h, input := make([]float32, 17029), make([]float32, 503)
	for i := range h {
		h[i] = (rng.Float32() - .5) * .03
	}
	for i := range input {
		input[i] = rng.Float32() - .5
	}
	n := len(input) + len(h) - 1
	want := direct(input, h, n)
	for _, block := range []int{64, 128, 256, 512} {
		t.Run(fmt.Sprint(block), func(t *testing.T) {
			r, err := New(48000, h, nil, block)
			if err != nil || r.tail == nil {
				t.Fatalf("nonuniform preparation: %v", err)
			}
			var maximum float64
			for i := 0; i < n+block+2048; i++ {
				var x float32
				if i < len(input) {
					x = input[i]
				}
				l, rr := r.Process(x, -x)
				var expected float64
				if j := i - block; j >= 0 && j < n {
					expected = want[j]
				}
				maximum = math.Max(maximum, math.Abs(float64(l)-expected))
				if r.Fault() || l != -rr || maximum > 1e-5 {
					t.Fatalf("frame %d max error %g fault %v", i, maximum, r.Fault())
				}
			}
			t.Logf("nonuniform direct error %g", maximum)
			r.Reset()
			for i := 0; i < 8192; i++ {
				if l, rr := r.Process(0, 0); l != 0 || rr != 0 || r.Fault() {
					t.Fatal("nonuniform reset silence failed")
				}
			}
		})
	}
}

func TestMaximumTailJobDeadline(t *testing.T) {
	r, err := New(48000, make([]float32, 12*48000), nil, 128)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8192; i++ {
		r.Process(.001, -.001)
		if r.Fault() {
			t.Fatalf("maximum bounded tail job missed its output deadline at frame %d", i)
		}
	}
}

func BenchmarkReverb(b *testing.B) {
	for _, seconds := range []int{1, 4} {
		for _, block := range []int{128, 512, 2048} {
			b.Run(fmt.Sprintf("%ds/%d", seconds, block), func(b *testing.B) {
				h := make([]float32, seconds*48000)
				for i := range h {
					h[i] = float32(math.Exp(-float64(i)/8000)) * .01
				}
				r, _ := New(48000, h, nil, block)
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					r.Process(.1, -.1)
				}
			})
		}
	}
}

// BenchmarkCallbackBurst reports average and worst observed 128-frame host
// callback duration. The maximum includes scheduler noise and is evidence,
// not a hardware-independent pass/fail test.
func BenchmarkCallbackBurst(b *testing.B) {
	for _, block := range []int{128, 512, 2048} {
		b.Run(fmt.Sprint(block), func(b *testing.B) {
			h := make([]float32, 217280) // selected ballroom response duration
			for i := range h {
				h[i] = float32(math.Exp(-float64(i)/36000)) * .01
			}
			r, _ := New(48000, h, nil, block)
			var maximum time.Duration
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				start := time.Now()
				for j := 0; j < 128; j++ {
					r.Process(.1, -.1)
				}
				if elapsed := time.Since(start); elapsed > maximum {
					maximum = elapsed
				}
			}
			b.ReportMetric(float64(maximum.Nanoseconds()), "max-ns/block")
		})
	}
}
