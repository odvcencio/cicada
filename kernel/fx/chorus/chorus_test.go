package chorus

import (
	"math"
	"testing"
	"unsafe"
)

func TestParamsAndFault(t *testing.T) {
	if _, err := New(22050, DefaultParams()); err != ErrSampleRate {
		t.Fatalf("unsupported rate: %v", err)
	}
	for _, edit := range []func(*Params){
		func(p *Params) { p.Voices = 0 }, func(p *Params) { p.Voices = 4 },
		func(p *Params) { p.Rate = .049 }, func(p *Params) { p.Rate = 5.01 },
		func(p *Params) { p.Depth = 8.01 }, func(p *Params) { p.Spread = -1 },
		func(p *Params) { p.Mix = 1.01 }, func(p *Params) { p.Depth = math.NaN() },
	} {
		params := DefaultParams()
		edit(&params)
		if _, err := New(48000, params); err == nil {
			t.Fatalf("accepted %+v", params)
		}
	}
	p, _ := New(48000, DefaultParams())
	changed := p.Params()
	changed.Voices = 1
	if p.SetParams(changed) == nil {
		t.Fatal("voice count changed after allocation")
	}
	p.Process(float32(math.NaN()), 0)
	if !p.Fault() {
		t.Fatal("nonfinite input did not fault")
	}
	if l, r := p.Process(1, 1); l != 0 || r != 0 {
		t.Fatal("fault did not mute")
	}
	p.Reset()
	if p.Fault() {
		t.Fatal("reset did not clear fault")
	}
}

func TestDryHistoryAndSmoothing(t *testing.T) {
	for _, rate := range []int{44100, 48000, 96000} {
		params := DefaultParams()
		params.Mix = 0
		params.Voices = 1
		params.Depth = 0
		p, _ := New(rate, params)
		for i := 0; i < rate/100; i++ {
			input := float32(.125)
			if i == 0 {
				input = 1
			}
			l, r := p.Process(input, -input)
			if l != input || r != -input {
				t.Fatalf("%d dry %d: %g, %g", rate, i, l, r)
			}
		}
		params.Mix = 1
		if err := p.SetParams(params); err != nil {
			t.Fatal(err)
		}
		first, _ := p.Process(.125, -.125)
		if first == 0 || first == .125 {
			t.Fatalf("%d mix did not ramp: %g", rate, first)
		}
		// If dry operation had skipped history, this wet signal would be zero.
		var energy float64
		for i := 0; i < rate/50; i++ {
			l, _ := p.Process(0, 0)
			energy += math.Abs(float64(l))
		}
		if energy == 0 {
			t.Fatalf("%d dry bypass did not keep history", rate)
		}
		params.Mix = 0
		_ = p.SetParams(params)
		for i := 0; i < rate; i++ {
			p.Process(0, 0)
		}
		l, r := p.Process(.125, -.125)
		if l != .125 || r != -.125 {
			t.Fatalf("%d settled mix not bit-exact", rate)
		}
	}
}

func TestImpulseResetAndSpread(t *testing.T) {
	for _, rate := range []int{44100, 48000, 96000} {
		params := DefaultParams()
		params.Mix = 1
		params.Spread = 0
		params.Depth = 0
		p, _ := New(rate, params)
		first := make([][2]float32, rate/20)
		var energy float64
		for i := range first {
			in := float32(0)
			if i == 0 {
				in = 1
			}
			l, r := p.Process(in, 0)
			first[i] = [2]float32{l, r}
			energy += math.Abs(float64(l))
			if l != r {
				t.Fatalf("%d mono spread unequal at %d", rate, i)
			}
			if i < rate*15/1000 && (l != 0 || r != 0) {
				t.Fatalf("%d early wet signal at %d", rate, i)
			}
		}
		if energy == 0 {
			t.Fatalf("%d impulse absent", rate)
		}
		p.Reset()
		for i, want := range first {
			in := float32(0)
			if i == 0 {
				in = 1
			}
			l, r := p.Process(in, 0)
			if l != want[0] || r != want[1] {
				t.Fatalf("%d reset mismatch at %d", rate, i)
			}
		}
	}
}

func TestAudioCallbackDoesNotAllocate(t *testing.T) {
	p, _ := New(48000, DefaultParams())
	if got := testing.AllocsPerRun(1000, func() { p.Process(.1, -.1) }); got != 0 {
		t.Fatalf("%g allocations/frame", got)
	}
}

func TestDelayMemoryBound(t *testing.T) {
	for _, rate := range []int{44100, 48000, 96000} {
		p, err := New(rate, DefaultParams())
		if err != nil {
			t.Fatal(err)
		}
		want := int(math.Ceil(float64(rate)*.028)) + 2
		if len(p.ring) != want || unsafe.Sizeof(*p) > 1024 {
			t.Fatalf("rate %d: %d frames, %d bytes of processor state", rate, len(p.ring), unsafe.Sizeof(*p))
		}
		if rate == 96000 && uintptr(len(p.ring))*unsafe.Sizeof(frame{}) != 21520 {
			t.Fatal("96 kHz delay history is not 21,520 bytes")
		}
	}
}

func BenchmarkProcess(b *testing.B) {
	p, _ := New(48000, DefaultParams())
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		p.Process(.1, -.1)
	}
}

// TestReferenceSignal checks the specified tap timing, polarity, stereo mix,
// lowpass and interpolation against a deliberately direct floating-point
// reference. Its oscillator uses math.Sin instead of the production table.
func TestReferenceSignal(t *testing.T) {
	for _, rate := range []int{44100, 48000, 96000} {
		for voices := 1; voices <= 3; voices++ {
			params := DefaultParams()
			params.Voices = voices
			params.Rate = 1.7
			params.Depth = 5
			params.Spread = .4
			params.Mix = .7
			p, _ := New(rate, params)
			type pair struct{ l, r float32 }
			buf := make([]pair, int(math.Ceil(.028*float64(rate)))+2)
			alpha := 1 - math.Exp(-2*math.Pi*6500/float64(rate))
			var filterL, filterR float64
			bases := [3]float64{16, 17.3, 18.6}
			slows := [3]float64{1, 1.205, 1.410}
			fasts := [3]float64{5.73, 6.347, 6.964}
			var slow, fast [3]uint32
			var fastStep [3]uint32
			for i := 0; i < 3; i++ {
				slow[i] = uint32(uint64(i) * (1 << 32) / 3)
				fast[i] = slow[i]
				fastStep[i] = uint32(fasts[i]*(4294967296/float64(rate)) + .5)
			}
			at := 0
			read := func(d float64, left bool) float64 {
				n := int(d)
				fraction := d - float64(n)
				a := (at - n + len(buf)) % len(buf)
				b := (a - 1 + len(buf)) % len(buf)
				x, y := buf[a], buf[b]
				if left {
					return float64(x.l) + (float64(y.l)-float64(x.l))*fraction
				}
				return float64(x.r) + (float64(y.r)-float64(x.r))*fraction
			}
			for frame := 0; frame < rate/10; frame++ {
				l := float32(.1 * math.Sin(float64(frame)*.031))
				r := float32(.08 * math.Cos(float64(frame)*.023))
				filterL += (float64(l) - filterL) * alpha
				filterR += (float64(r) - filterR) * alpha
				buf[at] = pair{float32(filterL), float32(filterR)}
				var wl, wr float64
				for i := 0; i < voices; i++ {
					offset := params.Depth*math.Sin(float64(slow[i])*(2*math.Pi/4294967296)) + .65*math.Sin(float64(fast[i])*(2*math.Pi/4294967296))
					wl += read((bases[i]+offset)*float64(rate)/1000, true)
					wr += read((bases[i]-offset)*float64(rate)/1000, false)
					slow[i] += uint32(params.Rate*slows[i]*(4294967296/float64(rate)) + .5)
					fast[i] += fastStep[i]
				}
				at = (at + 1) % len(buf)
				wl /= float64(voices)
				wr /= float64(voices)
				mid := (wl + wr) / 2
				wantL := (1-params.Mix)*float64(l) + params.Mix*((1-params.Spread)*mid+params.Spread*wl)
				wantR := (1-params.Mix)*float64(r) + params.Mix*((1-params.Spread)*mid+params.Spread*wr)
				gotL, gotR := p.Process(l, r)
				if math.Abs(float64(gotL)-wantL) > 1e-6 || math.Abs(float64(gotR)-wantR) > 1e-6 {
					t.Fatalf("rate %d voices %d frame %d: got (%g,%g) want (%g,%g)", rate, voices, frame, gotL, gotR, wantL, wantR)
				}
			}
		}
	}
}
