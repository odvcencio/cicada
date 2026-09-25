package fx

import (
	"math"
	"testing"
)

func TestReverbBoundsAndImpulse(t *testing.T) {
	if _, err := NewReverb(22_050); err == nil {
		t.Fatal("accepted unsupported sample rate")
	}
	r, err := NewReverb(48_000)
	if err != nil {
		t.Fatal(err)
	}
	r.predelay[0][len(r.predelay[0])-1] = 2
	if got := r.preRead(0, .25, 1); got != 1.25 {
		t.Fatalf("subsample predelay interpolated %g, want 1.25", got)
	}
	r.Reset()
	for _, mutate := range []func(*ReverbParams){
		func(p *ReverbParams) { p.Size = .49 },
		func(p *ReverbParams) { p.DecaySec = 12.1 },
		func(p *ReverbParams) { p.DampHz = 1_999 },
		func(p *ReverbParams) { p.HighpassHz = 401 },
		func(p *ReverbParams) { p.PredelayMs = 201 },
		func(p *ReverbParams) { p.Mix = math.NaN() },
	} {
		p := DefaultReverbParams()
		mutate(&p)
		if err := r.SetParams(p); err == nil {
			t.Fatalf("accepted invalid reverb params %+v", p)
		}
	}
	p := DefaultReverbParams()
	p.PredelayMs = 40
	if err := r.SetParams(p); err != nil {
		t.Fatal(err)
	}
	r.Reset()
	var first, tail float64
	for frame := 0; frame < 48_000; frame++ {
		input := float32(0)
		if frame == 0 {
			input = 1
		}
		left, right := r.Process(input, input)
		if frame < 3_000 && (left != 0 || right != 0) {
			t.Fatalf("reverb returned before predelay and diffuser: frame %d", frame)
		}
		if frame >= 3_000 && frame < 10_000 {
			first += math.Abs(float64(left)) + math.Abs(float64(right))
		}
		if frame >= 10_000 {
			tail += math.Abs(float64(left)) + math.Abs(float64(right))
		}
		if !finite(float64(left)) || !finite(float64(right)) {
			t.Fatalf("nonfinite reverb at frame %d", frame)
		}
	}
	if first < .01 || tail < .01 {
		t.Fatalf("reverb did not form an audible tail: early=%g late=%g", first, tail)
	}
	var afterReset [2]float32
	r.Reset()
	afterReset[0], afterReset[1] = r.Process(0, 0)
	if afterReset != [2]float32{} {
		t.Fatalf("reset retained tail: %v", afterReset)
	}
}

func TestReverbParameterChangesAndAllocationFree(t *testing.T) {
	r, _ := NewReverb(48_000)
	if count := testing.AllocsPerRun(1_000, func() { r.Process(.5, -.5) }); count != 0 {
		t.Fatalf("reverb process allocated %g times", count)
	}
	p := DefaultReverbParams()
	p.Size, p.DecaySec, p.DampHz, p.HighpassHz, p.PredelayMs = 1.5, 12, 16_000, 40, 200
	if err := r.SetParams(p); err != nil {
		t.Fatal(err)
	}
	for frame := 0; frame < 80; frame++ {
		r.Process(.1, -.1)
	}
	intermediate := r.oldLength[0]*(1-r.lengthFade) + r.newLength[0]*r.lengthFade
	p.Size = .5
	if err := r.SetParams(p); err != nil {
		t.Fatal(err)
	}
	if math.Abs(r.oldLength[0]-intermediate) > 1e-9 {
		t.Fatalf("rapid size change jumped from %g to %g frames", intermediate, r.oldLength[0])
	}
	p.Size = 1.5
	if count := testing.AllocsPerRun(1_000, func() {
		if err := r.SetParams(p); err != nil {
			t.Fatal(err)
		}
	}); count != 0 {
		t.Fatalf("reverb parameter update allocated %g times", count)
	}
	for frame := 0; frame < 150_000; frame++ {
		left, right := r.Process(1, -1)
		if !finite(float64(left)) || !finite(float64(right)) || math.Abs(float64(left)) > 4 || math.Abs(float64(right)) > 4 {
			t.Fatalf("reverb feedback unstable at frame %d: (%g,%g)", frame, left, right)
		}
	}
	r.Process(float32(math.NaN()), 0)
	if !r.Fault() {
		t.Fatal("nonfinite input did not fault")
	}
	r.Reset()
	if r.Fault() {
		t.Fatal("reset retained fault")
	}
}

func TestReverbDecayAndDC(t *testing.T) {
	r, _ := NewReverb(48_000)
	p := DefaultReverbParams()
	p.DecaySec, p.DampHz, p.HighpassHz = 2, 16_000, 40
	if err := r.SetParams(p); err != nil {
		t.Fatal(err)
	}
	r.Reset()
	var early, late, dc float64
	for frame := 0; frame < 30*48_000; frame++ {
		input := float32(0)
		if frame == 0 {
			input = 1
		}
		left, right := r.Process(input, input)
		if frame >= 48_000 && frame < 52_800 {
			early += float64(left*left + right*right)
		}
		if frame >= 3*48_000 && frame < 3*48_000+4_800 {
			late += float64(left*left + right*right)
		}
		if frame >= 29*48_000 {
			dc += float64(left+right) / (2 * 48_000)
		}
	}
	if early <= 0 || late <= 0 {
		t.Fatalf("reverb energy vanished: early=%g late=%g", early, late)
	}
	measuredT60 := 120 / (10 * math.Log10(early/late))
	if measuredT60 < 1.8 || measuredT60 > 2.2 {
		t.Fatalf("measured T60 %.3f s, want 2 s ±10%%", measuredT60)
	}
	if math.Abs(dc) > 1e-6 {
		t.Fatalf("reverb retained DC after 30 seconds: %g", dc)
	}
}

func BenchmarkReverbStereoSample(b *testing.B) {
	r, _ := NewReverb(48_000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Process(.1, -.1)
	}
}
