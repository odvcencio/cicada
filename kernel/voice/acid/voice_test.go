package acid

import (
	"math"
	"testing"

	"m31labs.dev/cicada/kernel/dsp/fastmath"
	"m31labs.dev/cicada/kernel/seq"
)

func TestDriveCompensationStaysWithinOneDecibel(t *testing.T) {
	voice, err := New(48_000)
	if err != nil {
		t.Fatal(err)
	}
	const amplitude = 0.251188643150958
	inputRMS := amplitude / math.Sqrt2
	for i := 0; i <= 64; i++ {
		params := voice.Params()
		params.Drive = float64(i) / 64
		if err := voice.SetParams(params); err != nil {
			t.Fatal(err)
		}
		var power float64
		for sample := 0; sample < 1024; sample++ {
			x := amplitude * math.Sin(2*math.Pi*float64(sample)/1024)
			y := fastmath.Tanh(voice.drivePre*x) * voice.drivePost
			power += y * y
		}
		rms := math.Sqrt(power / 1024)
		deltaDB := 20 * math.Log10(rms/inputRMS)
		if math.Abs(deltaDB) > 1 {
			t.Fatalf("drive %.4f changed sine RMS by %.3f dB", params.Drive, deltaDB)
		}
	}
}

func TestAccentChargeReferenceVector(t *testing.T) {
	voice, err := New(48_000)
	if err != nil {
		t.Fatal(err)
	}
	params := voice.Params()
	params.Resonance = 1
	if err := voice.SetParams(params); err != nil {
		t.Fatal(err)
	}
	clock, _ := seq.NewClock(48_000, 130_000)
	want := []float64{1, 1.189094, 1.308282, 1.378188}
	var previousSample int64
	for i, expected := range want {
		nextSample := clock.SampleAtTick(int64(i) * seq.TicksPerStep)
		for sample := previousSample; sample < nextSample; sample++ {
			voice.Next()
		}
		voice.NoteOn(45, true, false, 127)
		if got := voice.AccentStrength(); math.Abs(got-expected) > .0005 {
			t.Fatalf("accent %d strength %.6f, want %.6f", i, got, expected)
		}
		previousSample = nextSample
	}
	voice.Reset()
	voice.NoteOn(45, true, false, 127)
	if voice.AccentStrength() != 1 {
		t.Fatal("reset retained capacitor charge")
	}
}

func TestSlidePreservesPhaseAndEnvelope(t *testing.T) {
	voice, _ := New(48_000)
	voice.NoteOn(45, false, false, 100)
	for range 128 {
		voice.Next()
	}
	phase, meg, vca := voice.phaseA, voice.meg, voice.vca
	voice.NoteOn(52, false, true, 100)
	if voice.phaseA != phase || voice.meg != meg || voice.vca != vca || voice.targetLog == voice.pitchLog {
		t.Fatal("slide reset phase or envelope, or did not set pitch target")
	}
	voice.NoteOn(52, false, false, 100)
	if voice.phaseA != 0 || voice.meg != 1 || voice.vca != 0 {
		t.Fatal("plain note did not retrigger")
	}
}

func TestEnvelopeCanOpenCutoffAboveEightKilohertz(t *testing.T) {
	voice, err := New(48_000)
	if err != nil {
		t.Fatal(err)
	}
	params := voice.Params()
	params.Cutoff, params.EnvMod = 600, 1
	if err := voice.SetParams(params); err != nil {
		t.Fatal(err)
	}
	voice.meg = 1
	if got := voice.cutoffHz(); math.Abs(got-19_200) > .02 {
		t.Fatalf("full envelope cutoff = %g Hz, want 19200 Hz", got)
	}
	params.Cutoff = 8000
	if err := voice.SetParams(params); err != nil {
		t.Fatal(err)
	}
	if got := voice.cutoffHz(); got != 21_600 {
		t.Fatalf("swept cutoff = %g Hz, want 0.45*sampleRate", got)
	}
}

func TestAcidVoiceFiniteAndRelease(t *testing.T) {
	for _, rate := range []int{44_100, 48_000, 96_000} {
		for _, model := range []FilterModel{Diode, Ladder} {
			voice, err := New(rate)
			if err != nil {
				t.Fatal(err)
			}
			params := voice.Params()
			params.Filter, params.Cutoff, params.Resonance = model, 8000, 1
			if err := voice.SetParams(params); err != nil {
				t.Fatal(err)
			}
			voice.NoteOn(60, true, false, 127)
			peak := 0.0
			for i := 0; i < rate/4; i++ {
				value := float64(voice.Next())
				if math.IsNaN(value) || math.IsInf(value, 0) {
					t.Fatalf("nonfinite %d Hz model %d", rate, model)
				}
				if math.Abs(value) > peak {
					peak = math.Abs(value)
				}
			}
			if peak < .001 || voice.Fault() {
				t.Fatalf("silent or faulted %d Hz model %d: peak %g", rate, model, peak)
			}
			voice.NoteOff()
			for i := 0; i < rate/4; i++ {
				voice.Next()
			}
			if voice.Active() {
				t.Fatalf("voice remained active after release at %d Hz", rate)
			}
		}
	}
}

func TestAcidRenderDoesNotAllocate(t *testing.T) {
	voice, _ := New(48_000)
	voice.NoteOn(45, false, false, 100)
	var block [128]float32
	if count := testing.AllocsPerRun(100, func() { voice.Render(block[:]) }); count != 0 {
		t.Fatalf("acid render allocated %v times", count)
	}
}

func TestRestingDiodeCacheMatchesDynamicFilterBits(t *testing.T) {
	for _, rate := range []int{44_100, 48_000, 96_000} {
		for _, settings := range []struct{ cutoff, resonance float64 }{{20, 0}, {600, .55}, {8000, 1}} {
			cached, err := New(rate)
			if err != nil {
				t.Fatal(err)
			}
			params := cached.Params()
			params.Cutoff, params.Resonance = settings.cutoff, settings.resonance
			if err := cached.SetParams(params); err != nil {
				t.Fatal(err)
			}
			dynamic := *cached
			for sample := 0; sample < 4096; sample++ {
				first := math.Sin(float64(sample) * .017)
				second := math.Cos(float64(sample) * .011)
				gotFirst, gotSecond := cached.filterRestingDiodePair(first, second)
				wantFirst, wantSecond := dynamic.filterPair(first, second, dynamic.restingFilterG)
				if math.Float64bits(gotFirst) != math.Float64bits(wantFirst) || math.Float64bits(gotSecond) != math.Float64bits(wantSecond) {
					t.Fatalf("%d Hz cutoff %g resonance %g sample %d changed filter bits: (%g, %g) vs (%g, %g)", rate, settings.cutoff, settings.resonance, sample, gotFirst, gotSecond, wantFirst, wantSecond)
				}
				for stage := range cached.diode.state {
					if math.Float64bits(cached.diode.state[stage]) != math.Float64bits(dynamic.diode.state[stage]) {
						t.Fatalf("%d Hz cutoff %g resonance %g sample %d changed stage %d", rate, settings.cutoff, settings.resonance, sample, stage)
					}
				}
			}
		}
	}
}

var benchmarkOutput float32

func BenchmarkAcidNext(b *testing.B) {
	voice, _ := New(48_000)
	voice.NoteOn(45, true, false, 127)
	var output float32
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		output = voice.Next()
	}
	benchmarkOutput = output
}

func BenchmarkAcidNextRetrigger(b *testing.B) {
	voice, _ := New(48_000)
	var output float32
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if i%4800 == 0 {
			voice.NoteOn(45, true, false, 127)
		}
		output = voice.Next()
	}
	benchmarkOutput = output
}
