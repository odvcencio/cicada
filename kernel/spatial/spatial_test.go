package spatial

import (
	"math"
	"testing"
)

func TestSN3DAxesDistanceAndCoincidentPoint(t *testing.T) {
	for _, tc := range []struct {
		point Vec3
		want  BFormat
	}{
		{Vec3{X: 1}, BFormat{W: 1, X: 1}},
		{Vec3{Y: -1}, BFormat{W: 1, Y: -1}},
		{Vec3{Z: 2}, BFormat{W: .5, Z: .5}},
		{Vec3{}, BFormat{W: 1}},
	} {
		if got := Coefficients(tc.point, Vec3{}); got != tc.want {
			t.Fatalf("point %+v: got %+v, want %+v", tc.point, got, tc.want)
		}
	}
	if got := Coefficients(Vec3{X: 5, Y: 6, Z: 7}, Vec3{X: 5, Y: 5, Z: 7}); got != (BFormat{W: 1, Y: 1}) {
		t.Fatalf("listener translation: %+v", got)
	}
}

func TestListenerRotationAxesAndEnergy(t *testing.T) {
	for _, tc := range []struct {
		angles      [3]float32
		input, want BFormat
	}{
		{[3]float32{math.Pi / 2, 0, 0}, BFormat{W: 1, X: 1}, BFormat{W: 1, Y: -1}},
		{[3]float32{0, math.Pi / 2, 0}, BFormat{W: 1, Z: 1}, BFormat{W: 1, X: 1}},
		{[3]float32{0, 0, math.Pi / 2}, BFormat{W: 1, Y: 1}, BFormat{W: 1, Z: -1}},
	} {
		got := ListenerRotation(tc.angles[0], tc.angles[1], tc.angles[2]).Apply(tc.input)
		if got.W != tc.want.W || math.Abs(float64(got.X-tc.want.X))+math.Abs(float64(got.Y-tc.want.Y))+math.Abs(float64(got.Z-tc.want.Z)) > 1e-6 {
			t.Fatalf("rotation %+v: got %+v, want %+v", tc.angles, got, tc.want)
		}
	}
	b := BFormat{W: .5, X: .123, Y: -.456, Z: .789}
	rotated := ListenerRotation(.731, -.325, 1.51).Apply(b)
	energy := func(b BFormat) float64 { return float64(b.X*b.X + b.Y*b.Y + b.Z*b.Z) }
	if rotated.W != b.W || math.Abs(energy(rotated)-energy(b)) > 1e-6 {
		t.Fatalf("rotation changed directional energy: %+v", rotated)
	}
}

func impulse(rate int, b BFormat) (left, right [1024]float32) {
	var d Decoder
	d.Init(rate)
	for i := range left {
		input := BFormat{}
		if i == 0 {
			input = b
		}
		left[i], right[i] = d.Process(input)
	}
	return
}

func TestBinauralTimingShadowSymmetryAndHeight(t *testing.T) {
	for _, rate := range []int{44100, 48000, 96000} {
		left, right := impulse(rate, BFormat{W: 1, Y: 1})
		mirrorL, mirrorR := impulse(rate, BFormat{W: 1, Y: -1})
		peakL, peakR := 0, 0
		var energyL, energyR float64
		for i := range left {
			if left[i] != mirrorR[i] || right[i] != mirrorL[i] {
				t.Fatal("ear model is not symmetric")
			}
			if math.Abs(float64(left[i])) > math.Abs(float64(left[peakL])) {
				peakL = i
			}
			if math.Abs(float64(right[i])) > math.Abs(float64(right[peakR])) {
				peakR = i
			}
			energyL += float64(left[i] * left[i])
			energyR += float64(right[i] * right[i])
		}
		if peakR <= peakL || energyL <= energyR || energyR == 0 {
			t.Fatalf("missing binaural timing or shadow at %d Hz: peaks=%d/%d energy=%g/%g", rate, peakL, peakR, energyL, energyR)
		}
		front, _ := impulse(rate, BFormat{W: 1, X: 1})
		back, _ := impulse(rate, BFormat{W: 1, X: -1})
		above, _ := impulse(rate, BFormat{W: 1, Z: 1})
		below, _ := impulse(rate, BFormat{W: 1, Z: -1})
		if front == back || above == below {
			t.Fatal("front/back or elevation cues are identical")
		}
		t.Logf("rate=%d left_peak=%d right_peak=%d ILD=%.2f dB", rate, peakL, peakR, 10*math.Log10(energyL/energyR))
	}
}

func TestSpatialAllocationFreeAndReset(t *testing.T) {
	var d Decoder
	if allocations := testing.AllocsPerRun(100, func() {
		d.Init(96000)
		r := ListenerRotation(.25, -.5, .75)
		b := Coefficients(Vec3{X: 1, Y: 2, Z: 3}, Vec3{})
		for range 128 {
			d.Process(r.Apply(b))
		}
		d.Reset()
	}); allocations != 0 {
		t.Fatalf("spatial DSP allocated %g objects", allocations)
	}
	for range 1024 {
		l, r := d.Process(BFormat{})
		if l != 0 || r != 0 {
			t.Fatal("reset retained decoder history")
		}
	}
}
