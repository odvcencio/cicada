package fx

import (
	"math"
	"testing"
)

func TestDriveParamsAndLatency(t *testing.T) {
	if _, err := NewDrive(22_050); err == nil {
		t.Fatal("accepted unsupported sample rate")
	}
	drive, err := NewDrive(48_000)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []DriveParams{
		{Shape: driveShapes, ToneHz: 12_000, Mix: 1},
		{Shape: Soft, GainDB: 37, ToneHz: 12_000, Mix: 1},
		{Shape: Soft, GainDB: math.NaN(), ToneHz: 12_000, Mix: 1},
		{Shape: Soft, ToneHz: 999, Mix: 1},
		{Shape: Soft, ToneHz: 12_000, Mix: 1.01},
	} {
		if err := drive.SetParams(bad); err == nil {
			t.Fatalf("accepted invalid parameters %+v", bad)
		}
	}
	params := DefaultDriveParams()
	params.Mix = 0
	if err := drive.SetParams(params); err != nil {
		t.Fatal(err)
	}
	drive.Reset()
	if got := drive.LatencyFrames(); got != 15 {
		t.Fatalf("drive latency %d frames, want 15", got)
	}
	for i := 0; i < 30; i++ {
		input := float32(0)
		if i == 0 {
			input = .75
		}
		left, right := drive.Process(input, -input)
		if i == 15 {
			if left != .75 || right != -.75 {
				t.Fatalf("dry impulse arrived as (%g, %g)", left, right)
			}
		} else if left != 0 || right != 0 {
			t.Fatalf("dry impulse leaked at frame %d: (%g, %g)", i, left, right)
		}
	}
}

func TestDriveReferenceLevelAndStereo(t *testing.T) {
	const rate = 48_000
	const amplitude = 0.251188643150958
	inputRMS := amplitude / math.Sqrt2
	for _, shape := range []DriveShape{Soft, Hard, Fold, Diode} {
		for _, gain := range []float64{0, 18, 36} {
			drive, err := NewDrive(rate)
			if err != nil {
				t.Fatal(err)
			}
			params := DriveParams{Shape: shape, GainDB: gain, ToneHz: 20_000, Mix: 1}
			if err := drive.SetParams(params); err != nil {
				t.Fatal(err)
			}
			drive.Reset()
			var leftPower, rightPower float64
			for i := 0; i < rate; i++ {
				input := float32(amplitude * math.Sin(2*math.Pi*1_000*float64(i)/rate))
				left, right := drive.Process(input, -input)
				if i >= rate/2 {
					leftPower += float64(left) * float64(left)
					rightPower += float64(right) * float64(right)
				}
			}
			leftRMS := math.Sqrt(leftPower / (rate / 2))
			rightRMS := math.Sqrt(rightPower / (rate / 2))
			if math.Abs(20*math.Log10(leftRMS/inputRMS)) > 3 {
				t.Errorf("shape %d gain %g: left RMS %g, input %g", shape, gain, leftRMS, inputRMS)
			}
			if math.Abs(leftRMS-rightRMS) > inputRMS*.12 {
				t.Errorf("shape %d gain %g: asymmetry exceeds 12%% (%g vs %g)", shape, gain, leftRMS, rightRMS)
			}
		}
	}
}

func TestDriveShapeSwitchAndFault(t *testing.T) {
	drive, _ := NewDrive(48_000)
	for i := 0; i < 1_000; i++ {
		drive.Process(.25, .25)
	}
	before, _ := drive.Process(.25, .25)
	params := drive.Params()
	params.Shape = Hard
	params.GainDB = 24
	if err := drive.SetParams(params); err != nil {
		t.Fatal(err)
	}
	first, _ := drive.Process(.25, .25)
	if math.Abs(float64(first-before)) > .05 {
		t.Fatalf("shape/gain change clicked: %g -> %g", before, first)
	}
	for i := 0; i < 2_000; i++ {
		left, right := drive.Process(.25, -.25)
		if !finite(float64(left)) || !finite(float64(right)) {
			t.Fatal("nonfinite drive output")
		}
	}
	if _, _ = drive.Process(float32(math.NaN()), 0); !drive.Fault() {
		t.Fatal("nonfinite input did not fault")
	}
	left, right := drive.Process(.25, .25)
	if left != 0 || right != 0 {
		t.Fatal("faulted drive emitted audio")
	}
	drive.Reset()
	if drive.Fault() {
		t.Fatal("reset retained fault")
	}
}

func TestHardDriveOversamplingRejectsAlias(t *testing.T) {
	const rate = 48_000
	const start, count = 1_000, 4_800
	drive, _ := NewDrive(rate)
	if err := drive.SetParams(DriveParams{Shape: Hard, GainDB: 18, ToneHz: 20_000, Mix: 1}); err != nil {
		t.Fatal(err)
	}
	drive.Reset()
	pre, post := math.Pow(10, 18.0/20), drive.post[Hard]
	var actualReal, actualImag, naiveReal, naiveImag float64
	for frame := 0; frame < start+count; frame++ {
		input := .251188643150958 * math.Sin(2*math.Pi*9_000*float64(frame)/rate)
		output, _ := drive.Process(float32(input), 0)
		if frame < start {
			continue
		}
		// The fifth harmonic of a 9 kHz clipped sine aliases to 3 kHz
		// at 48 kHz. Compare it with clipping directly at the base rate.
		angle := 2 * math.Pi * 3_000 * float64(frame) / rate
		naive := math.Max(-1, math.Min(1, input*pre)) * post
		actualReal += float64(output) * math.Cos(angle)
		actualImag += float64(output) * math.Sin(angle)
		naiveReal += naive * math.Cos(angle)
		naiveImag += naive * math.Sin(angle)
	}
	actual := math.Hypot(actualReal, actualImag)
	naive := math.Hypot(naiveReal, naiveImag)
	if actual >= naive*.1 {
		t.Fatalf("3 kHz alias %.5g, naive clip %.5g: less than 20 dB rejection", actual, naive)
	}
}

func TestDriveDoesNotAllocate(t *testing.T) {
	drive, _ := NewDrive(48_000)
	if count := testing.AllocsPerRun(1_000, func() { drive.Process(.25, -.25) }); count != 0 {
		t.Fatalf("drive process allocated %g times", count)
	}
}

var benchmarkDriveOutput float32

func BenchmarkDriveProcess(b *testing.B) {
	drive, _ := NewDrive(48_000)
	params := DriveParams{Shape: Hard, GainDB: 18, ToneHz: 12_000, Mix: 1}
	drive.SetParams(params)
	drive.Reset()
	var output float32
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		output, _ = drive.Process(.25, -.25)
	}
	benchmarkDriveOutput = output
}
