package halfband

import (
	"math"
	"testing"
)

func TestCoefficientsAndPairedStopband(t *testing.T) {
	f := New()
	coefficients := f.Coefficients()
	var sum float64
	for n, value := range coefficients {
		sum += value
		if math.Abs(value-coefficients[Taps-1-n]) > 1e-15 {
			t.Fatalf("asymmetric tap %d", n)
		}
		if n != center && (n-center)%2 == 0 && value != 0 {
			t.Fatalf("nonzero half-band tap %d", n)
		}
	}
	if math.Abs(sum-1) > 1e-14 {
		t.Fatalf("coefficient sum %g", sum)
	}
	if coefficients[center] < 0.49 || coefficients[center] > 0.51 {
		t.Fatalf("center tap %g", coefficients[center])
	}
	pairedDB := func(frequency float64) float64 {
		var real, imaginary float64
		for n, value := range coefficients {
			angle := 2 * math.Pi * frequency * float64(n)
			real += value * math.Cos(angle)
			imaginary += value * math.Sin(angle)
		}
		magnitude := math.Hypot(real, imaginary)
		return 40 * math.Log10(magnitude)
	}
	if db := pairedDB(0.1); db < -0.1 || db > 0.1 {
		t.Fatalf("passband response %g dB", db)
	}
	if db := pairedDB(0.3); db > -60 {
		t.Fatalf("paired stopband response %g dB", db)
	}
	if checksum := f.Checksum(); checksum != 0xe5362dfc9652158e {
		t.Fatalf("coefficient checksum changed: %016x", checksum)
	}
}

func TestUpsampleDownsampleConstant(t *testing.T) {
	up, down := New(), New()
	var output float64
	for i := 0; i < 200; i++ {
		first, second := up.Upsample(1)
		output = down.Downsample(first, second)
	}
	if math.Abs(output-1) > 0.002 {
		t.Fatalf("constant changed to %g", output)
	}
	if count := testing.AllocsPerRun(1000, func() {
		first, second := up.Upsample(0.2)
		down.Downsample(first, second)
	}); count != 0 {
		t.Fatalf("filter allocated %v times", count)
	}
}

func TestUpsampleMatchesTwoFilteredPhases(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		optimized, reference := New(), New()
		if mixed {
			if got, want := optimized.Push(.25), reference.Push(.25); math.Float64bits(got) != math.Float64bits(want) {
				t.Fatal("mixed FIR prelude differs")
			}
		}
		for i := 0; i < 1024; i++ {
			input := math.Sin(float64(i)*.13) * .7
			first, second := optimized.Upsample(input)
			wantFirst, wantSecond := 2*reference.Push(input), 2*reference.Push(0)
			if math.Float64bits(first) != math.Float64bits(wantFirst) || math.Float64bits(second) != math.Float64bits(wantSecond) {
				t.Fatalf("phase %d differs after mixed=%v", i, mixed)
			}
		}
	}
}

func TestUnrolledPushMatchesTapLoopBits(t *testing.T) {
	optimized, reference := New(), New()
	for i := 0; i < 1024; i++ {
		input := math.Sin(float64(i)*.17) * .7
		got := optimized.Push(input)
		reference.history[reference.position] = input
		reference.history[reference.position+Taps] = input
		var want float64
		for tap := 0; tap < center; tap += 2 {
			want += reference.coefficients[tap] * reference.history[reference.position+Taps-tap]
		}
		want += reference.coefficients[center] * reference.history[reference.position+Taps-center]
		for tap := center + 1; tap < Taps; tap += 2 {
			want += reference.coefficients[tap] * reference.history[reference.position+Taps-tap]
		}
		reference.position = (reference.position + 1) % Taps
		if math.Float64bits(got) != math.Float64bits(want) || optimized.position != reference.position {
			t.Fatalf("sample %d differs from tap loop: got %.17g, want %.17g", i, got, want)
		}
	}
}

func TestDecision0002HalfbandResponseGate(t *testing.T) {
	filter := New()
	coefficients := filter.Coefficients()
	responseDB := func(frequency float64) float64 {
		var real, imaginary float64
		for n, coefficient := range coefficients {
			angle := 2 * math.Pi * frequency * float64(n)
			real += coefficient * math.Cos(angle)
			imaginary += coefficient * math.Sin(angle)
		}
		return 20 * math.Log10(math.Hypot(real, imaginary))
	}
	passbandDB := responseDB(0.2)
	stopbandEdgeDB := responseDB(0.3)
	stopbandDB := responseDB(0.35)
	t.Logf("half-band response: 0.2 fso %.3f dB, 0.3 fso %.3f dB, 0.35 fso %.3f dB", passbandDB, stopbandEdgeDB, stopbandDB)
	if math.Abs(passbandDB) > 0.1 || stopbandEdgeDB > -60 || stopbandDB > -60 {
		t.Fatalf("half-band response violates decision 0002: %.3f dB at 0.2 fso, %.3f dB at 0.3 fso, and %.3f dB at 0.35 fso", passbandDB, stopbandEdgeDB, stopbandDB)
	}
	for step := 0; step <= 400; step++ {
		if frequency := .2 * float64(step) / 400; math.Abs(responseDB(frequency)) > .1 {
			t.Fatalf("passband ripple exceeds 0.1 dB at %.5f fso", frequency)
		}
		if frequency := .3 + .2*float64(step)/400; responseDB(frequency) > -60 {
			t.Fatalf("stopband attenuation is below 60 dB at %.5f fso", frequency)
		}
	}
}
