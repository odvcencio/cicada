package fx

import (
	"math"
	"testing"
)

func TestDeesserSelectiveSibilanceAndStereoLink(t *testing.T) {
	for _, rate := range []int{44100, 48000, 96000} {
		d, err := NewDeesser(rate, DefaultDeesserParams())
		if err != nil {
			t.Fatal(err)
		}
		var bodyIn, bodyOut, sibIn, sibOut float64
		for i := 0; i < rate; i++ {
			body := .2 * math.Sin(2*math.Pi*200*float64(i)/float64(rate))
			sib := .25 * math.Sin(2*math.Pi*9000*float64(i)/float64(rate))
			x := float32(body + sib)
			l, r := d.Process(x, x*.5)
			if math.Abs(float64(r-l*.5)) > 1e-7 {
				t.Fatal("de-esser changed linked stereo image")
			}
			if i >= rate/2 {
				low := math.Sin(2 * math.Pi * 200 * float64(i) / float64(rate))
				hi := math.Sin(2 * math.Pi * 9000 * float64(i) / float64(rate))
				bodyIn += float64(x) * low
				bodyOut += float64(l) * low
				sibIn += float64(x) * hi
				sibOut += float64(l) * hi
			}
		}
		bodyDB := 20 * math.Log10(math.Abs(bodyOut/bodyIn))
		sibDB := 20 * math.Log10(math.Abs(sibOut/sibIn))
		if math.Abs(bodyDB) > .2 || sibDB > -4 {
			t.Fatalf("rate %d: body %.3f dB, sibilance %.3f dB", rate, bodyDB, sibDB)
		}
		if d.GainReductionDB() > 9.00001 || d.GainReductionDB() < 5 || d.Fault() {
			t.Fatalf("invalid reduction %g", d.GainReductionDB())
		}
		for i := 0; i < rate; i++ {
			d.Process(0, 0)
		}
		if d.GainReductionDB() > .001 {
			t.Fatal("reduction did not release")
		}
	}
}
func TestDeesserBypassResetFaultAndNoAllocation(t *testing.T) {
	p := DefaultDeesserParams()
	p.Mix = 0
	d, _ := NewDeesser(48000, p)
	for _, x := range []float32{0, 1, -1, .314159, 1e-30} {
		l, r := d.Process(x, -x)
		if l != x || r != -x {
			t.Fatal("bypass changed original PCM")
		}
	}
	p.Mix = 1
	d, _ = NewDeesser(48000, p)
	var first [1000]float32
	for i := range first {
		first[i], _ = d.Process(float32(math.Sin(float64(i))), 0)
	}
	d.Reset()
	for i := range first {
		l, _ := d.Process(float32(math.Sin(float64(i))), 0)
		if l != first[i] {
			t.Fatal("reset not deterministic")
		}
	}
	if n := testing.AllocsPerRun(1000, func() { d.Process(.2, -.2) }); n != 0 {
		t.Fatalf("allocated %g", n)
	}
	d.Process(float32(math.NaN()), 0)
	if !d.Fault() {
		t.Fatal("non-finite input did not fault")
	}
	l, r := d.Process(1, 1)
	if l != 0 || r != 0 {
		t.Fatal("fault did not mute")
	}
	d.Reset()
	if d.Fault() {
		t.Fatal("reset did not clear fault")
	}
}
func TestDeesserParameterBounds(t *testing.T) {
	if _, err := NewDeesser(32000, DefaultDeesserParams()); err == nil {
		t.Fatal("unsupported rate")
	}
	for _, f := range []func(*DeesserParams){func(p *DeesserParams) { p.FrequencyHz = 1999 }, func(p *DeesserParams) { p.ThresholdDB = math.NaN() }, func(p *DeesserParams) { p.Ratio = 21 }, func(p *DeesserParams) { p.RangeDB = 25 }, func(p *DeesserParams) { p.AttackMs = 0 }, func(p *DeesserParams) { p.ReleaseMs = math.Inf(1) }, func(p *DeesserParams) { p.Mix = 1.1 }} {
		p := DefaultDeesserParams()
		f(&p)
		if _, err := NewDeesser(48000, p); err == nil {
			t.Fatalf("accepted %+v", p)
		}
	}
}
func BenchmarkDeesserStereo(b *testing.B) {
	d, _ := NewDeesser(48000, DefaultDeesserParams())
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		d.Process(.2, -.2)
	}
}
