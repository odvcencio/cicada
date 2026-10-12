package fx

import (
	"math"
	"testing"
)

func newPingPongTestDelay(t *testing.T, feedback, width, mix float64) *Delay {
	t.Helper()
	d, err := NewDelay(48_000, 120_000)
	if err != nil {
		t.Fatal(err)
	}
	p := DefaultDelayParams()
	p.Division, p.TimeMs, p.Feedback = FreeDelay, 10, feedback
	p.PingPong, p.Width, p.Mix, p.DampHz = true, width, mix, 16_000
	if err := d.SetParams(p); err != nil {
		t.Fatal(err)
	}
	d.Reset()
	return d
}

func TestDelayCenteredMonoPingPongAlternates(t *testing.T) {
	d := newPingPongTestDelay(t, .5, 1, 1)
	var echoes [3][2]float32
	for frame := 0; frame <= 3*480; frame++ {
		input := float32(0)
		if frame == 0 {
			input = 1
		}
		left, right := d.Process(input, input)
		if frame > 0 && frame%480 == 0 {
			echoes[frame/480-1] = [2]float32{left, right}
		}
	}
	if echoes[0] != [2]float32{1, 0} {
		t.Fatalf("centered first echo = %v, want left-only unit peak", echoes[0])
	}
	if math.Abs(float64(echoes[1][0])) > 1e-6 || echoes[1][1] < .3 {
		t.Fatalf("second echo = %v, want right-only feedback", echoes[1])
	}
	if echoes[2][0] < .1 || math.Abs(float64(echoes[2][1])) > 1e-6 {
		t.Fatalf("third echo = %v, want left-only feedback", echoes[2])
	}
}

func TestDelayPingPongWetFoldDownContract(t *testing.T) {
	for _, test := range []struct {
		name              string
		left, right, want float32
	}{
		{"centered", 1, 1, 1},
		{"left", 1, 0, .5},
		{"right", 0, 1, .5},
		{"unequal", .75, .25, .5},
		{"side-only-cancels", 1, -1, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newPingPongTestDelay(t, 0, 1, 1)
			var energy float64
			for frame := 0; frame <= 960; frame++ {
				var left, right float32
				if frame == 0 {
					left, right = test.left, test.right
				}
				l, r := d.Process(left, right)
				energy += float64(l*l + r*r)
				if frame == 480 {
					if l != test.want || r != 0 {
						t.Fatalf("first echo (%g,%g), want (%g,0)", l, r, test.want)
					}
				} else if l != 0 || r != 0 {
					t.Fatalf("unexpected echo at frame %d: (%g,%g)", frame, l, r)
				}
			}
			// This mode deliberately folds to mono; it must not compensate
			// by adding gain or silently discard anything beyond that fold.
			wantEnergy := float64(test.want * test.want)
			if energy != wantEnergy {
				t.Fatalf("wet impulse energy %g, want %g", energy, wantEnergy)
			}
		})
	}
}

func TestDelayPingPongDryAndWidth(t *testing.T) {
	for _, mix := range []float64{0, .25, 1} {
		d := newPingPongTestDelay(t, .5, 0, mix)
		for frame := 0; frame <= 960; frame++ {
			var inL, inR float32
			if frame == 0 {
				inL, inR = .75, .25
			}
			left, right := d.Process(inL, inR)
			if frame == 0 {
				if left != float32(float64(inL)*(1-mix)) || right != float32(float64(inR)*(1-mix)) {
					t.Fatalf("mix %g changed dry stereo: (%g,%g)", mix, left, right)
				}
			} else if left != right {
				t.Fatalf("mix %g width zero not mono at %d: (%g,%g)", mix, frame, left, right)
			}
			if frame == 480 && (left != float32(.25*mix) || right != float32(.25*mix)) {
				t.Fatalf("mix %g first centered echo = (%g,%g)", mix, left, right)
			}
		}
	}
	// Even a source canceled by wet mono folding retains its complete dry
	// stereo signal. Mode changes must never fold the track itself to mono.
	d := newPingPongTestDelay(t, .5, 1, 0)
	for frame := 0; frame < 2000; frame++ {
		in := float32(math.Sin(float64(frame) * .1))
		l, r := d.Process(in, -in)
		if l != in || r != -in {
			t.Fatalf("dry input changed at %d", frame)
		}
	}
}

func TestDelayStereoModeRetainsInputChannels(t *testing.T) {
	d := newPingPongTestDelay(t, 0, 1, 1)
	p := d.Params()
	p.PingPong = false
	if err := d.SetParams(p); err != nil {
		t.Fatal(err)
	}
	d.Reset()
	for frame := 0; frame <= 960; frame++ {
		var inputL, inputR float32
		if frame == 0 {
			inputL, inputR = .75, -.25
		}
		l, r := d.Process(inputL, inputR)
		if frame == 480 {
			if l != .75 || r != -.25 {
				t.Fatalf("stereo delay changed channel signals: (%g,%g)", l, r)
			}
		} else if l != 0 || r != 0 {
			t.Fatalf("unexpected stereo echo at %d: (%g,%g)", frame, l, r)
		}
	}
}

func TestDelayPingPongModeChangesAndTail(t *testing.T) {
	d := newPingPongTestDelay(t, .8, 1, 1)
	p := d.Params()
	p.PingPong = false
	if err := d.SetParams(p); err != nil {
		t.Fatal(err)
	}
	d.Reset()
	for frame := 0; frame < 4800; frame++ {
		d.Process(.3, .1)
	}
	beforeL, beforeR := d.Process(.3, .1)
	p.PingPong = true
	if err := d.SetParams(p); err != nil {
		t.Fatal(err)
	}
	afterL, afterR := d.Process(.3, .1)
	if math.Abs(float64(beforeL-afterL)) > .01 || math.Abs(float64(beforeR-afterR)) > .01 {
		t.Fatalf("mode switch clicked: (%g,%g) -> (%g,%g)", beforeL, beforeR, afterL, afterR)
	}
	if count := testing.AllocsPerRun(1000, func() { d.Process(.3, .1) }); count != 0 {
		t.Fatalf("ping-pong allocated %g times", count)
	}
	var lastL, lastR float32
	for frame := 0; frame < 2*48000; frame++ {
		if frame == 2400 {
			p.TimeMs, p.PingPong = 17, false
			if err := d.SetParams(p); err != nil {
				t.Fatal(err)
			}
		}
		if frame == 4800 {
			p.PingPong = true
			if err := d.SetParams(p); err != nil {
				t.Fatal(err)
			}
		}
		lastL, lastR = d.Process(0, 0)
		if !finite(float64(lastL)) || !finite(float64(lastR)) || math.Abs(float64(lastL)) > 3 || math.Abs(float64(lastR)) > 3 {
			t.Fatalf("unstable tail at frame %d: (%g,%g)", frame, lastL, lastR)
		}
	}
	if math.Abs(float64(lastL))+math.Abs(float64(lastR)) > 1e-7 || d.Fault() {
		t.Fatalf("tail did not decay cleanly: (%g,%g), fault=%v", lastL, lastR, d.Fault())
	}
}
