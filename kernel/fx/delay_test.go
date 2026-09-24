package fx

import (
	"math"
	"testing"
)

func TestDelayDivisionsAndBounds(t *testing.T) {
	for _, name := range delayDivisionNames {
		division, err := ParseDelayDivision(name)
		if err != nil || division.String() != name {
			t.Fatalf("division %q: %v, %q", name, err, division)
		}
	}
	if _, err := ParseDelayDivision("1/64"); err == nil {
		t.Fatal("accepted unknown division")
	}
	if _, err := NewDelay(22_050, 120_000); err == nil {
		t.Fatal("accepted unsupported rate")
	}
	delay, err := NewDelay(48_000, 20_000)
	if err != nil {
		t.Fatal(err)
	}
	params := DefaultDelayParams()
	params.Division = Half // 6 seconds at 20 BPM exceeds the 4-second buffer.
	if err := delay.SetParams(params); err == nil {
		t.Fatal("accepted a synced time longer than the buffer")
	}
	params = DefaultDelayParams()
	params.Division, params.TimeMs = FreeDelay, 2_001
	if err := delay.SetParams(params); err == nil {
		t.Fatal("accepted free time above 2 seconds")
	}
	params.TimeMs = 10
	params.Feedback = .96
	if err := delay.SetParams(params); err == nil {
		t.Fatal("accepted unstable feedback")
	}
}

func TestDelayImpulseTimingAndPingPong(t *testing.T) {
	delay, _ := NewDelay(48_000, 120_000)
	params := DefaultDelayParams()
	params.Feedback = 0
	if err := delay.SetParams(params); err != nil {
		t.Fatal(err)
	}
	delay.Reset()
	for frame := 0; frame <= 12_000; frame++ {
		input := float32(0)
		if frame == 0 {
			input = 1
		}
		left, right := delay.Process(input, -input)
		if frame == 12_000 {
			if left != 1 || right != -1 {
				t.Fatalf("1/8 echo at frame %d = (%g, %g)", frame, left, right)
			}
		} else if left != 0 || right != 0 {
			t.Fatalf("premature echo at frame %d: (%g, %g)", frame, left, right)
		}
	}
	params.Division, params.TimeMs = FreeDelay, 10
	params.Feedback, params.PingPong = .5, true
	params.DampHz = 16_000
	if err := delay.SetParams(params); err != nil {
		t.Fatal(err)
	}
	delay.Reset()
	var firstL, firstR, secondL, secondR float32
	for frame := 0; frame <= 960; frame++ {
		input := float32(0)
		if frame == 0 {
			input = 1
		}
		left, right := delay.Process(input, 0)
		if frame == 480 {
			firstL, firstR = left, right
		}
		if frame == 960 {
			secondL, secondR = left, right
		}
	}
	if firstL != 1 || firstR != 0 || math.Abs(float64(secondL)) > .02 || secondR < .3 {
		t.Fatalf("pingpong echoes first=(%g,%g), second=(%g,%g)", firstL, firstR, secondL, secondR)
	}
}

func TestDelayTempoChangeCrossfadesReadHeads(t *testing.T) {
	delay, _ := NewDelay(48_000, 120_000)
	params := DefaultDelayParams()
	params.Feedback = 0
	delay.SetParams(params)
	delay.Reset()
	for frame := 0; frame < 13_000; frame++ {
		delay.Process(1, 1)
	}
	if err := delay.SetTempo(60_000); err != nil {
		t.Fatal(err)
	}
	if delay.newFrames != 24_000 || delay.oldFrames != 12_000 {
		t.Fatalf("tempo did not move read head: %g -> %g", delay.oldFrames, delay.newFrames)
	}
	first, _ := delay.Process(1, 1)
	if first < .99 {
		t.Fatalf("time change clicked from 1 to %g", first)
	}
	var last float32
	for frame := 0; frame < 960; frame++ {
		last, _ = delay.Process(1, 1)
	}
	if last > .01 || delay.fade != 1 {
		t.Fatalf("20 ms crossfade did not reach new read head: %g, fade %g", last, delay.fade)
	}
}

func TestDelayFiniteAndAllocationFree(t *testing.T) {
	delay, _ := NewDelay(48_000, 120_000)
	params := DefaultDelayParams()
	params.Division, params.TimeMs = FreeDelay, 1
	params.Feedback = .95
	delay.SetParams(params)
	delay.Reset()
	if count := testing.AllocsPerRun(1000, func() { delay.Process(1, -1) }); count != 0 {
		t.Fatalf("delay process allocated %g times", count)
	}
	for frame := 0; frame < 100_000; frame++ {
		left, right := delay.Process(1, -1)
		if !finite(float64(left)) || !finite(float64(right)) || math.Abs(float64(left)) > 3 || math.Abs(float64(right)) > 3 {
			t.Fatalf("feedback unstable at frame %d: (%g,%g)", frame, left, right)
		}
	}
	if _, _ = delay.Process(float32(math.NaN()), 0); !delay.Fault() {
		t.Fatal("nonfinite input did not fault")
	}
	delay.Reset()
	if delay.Fault() {
		t.Fatal("reset retained fault")
	}
}
