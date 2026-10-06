package spatial

import "math"

// Decoder uses six virtual speakers at the cardinal directions with max-rE
// weighting. Its generic analytic ear model combines fractional interaural
// delays (up to 0.65 ms), low-pass head shadow, and two-tap direction-dependent
// coloration. It contains no measured or individualized HRTF data.
// All history is inline; Init, Process, and Reset do not allocate.
type Decoder struct {
	speakers [6]speaker
	position int
}

type speaker struct {
	history [128]float32
	ear     [2]ear
}

type ear struct {
	delay, reflection int
	fraction          float32
	alpha, gain, echo float32
	low               float32
}

// Init prepares coefficients for the kernel's supported sample rates.
func (d *Decoder) Init(sampleRate int) {
	for i := range d.speakers {
		for channel := range 2 {
			e := &d.speakers[i].ear[channel]
			// Front, back, left, right, above, below.
			lateral := float64(0)
			if i == 2 {
				lateral = 1
			} else if i == 3 {
				lateral = -1
			}
			if channel == 1 {
				lateral = -lateral
			}
			delay := .000325 * (1 - lateral) * float64(sampleRate)
			e.delay, e.fraction = int(delay), float32(delay-float64(int(delay)))
			cutoff := 9000 + 6500*lateral
			e.gain = float32(.85 + .15*lateral)
			e.echo = -.12
			reflection := .00012
			switch i {
			case 1:
				cutoff *= .65
				e.echo, reflection = -.25, .0002
			case 4:
				e.echo, reflection = -.3, .00009
			case 5:
				e.echo, reflection = .2, .00018
			}
			e.reflection = max(1, int(reflection*float64(sampleRate)))
			e.alpha = float32(1 - math.Exp(-2*math.Pi*cutoff/float64(sampleRate)))
		}
	}
	d.Reset()
}

func (d *Decoder) Reset() {
	for i := range d.speakers {
		d.speakers[i].history = [128]float32{}
		for channel := range 2 {
			d.speakers[i].ear[channel].low = 0
		}
	}
	d.position = 0
}

func (d *Decoder) Process(b BFormat) (left, right float32) {
	const directional = float32(1.7320508075688772) // max-rE: sqrt(3) for SN3D
	components := [6]float32{b.X, -b.X, b.Y, -b.Y, b.Z, -b.Z}
	for i := range d.speakers {
		s := &d.speakers[i]
		s.history[d.position] = (b.W + float32(directional*components[i])) / 6
		for channel := range 2 {
			e := &s.ear[channel]
			at := (d.position - e.delay) & 127
			sample := s.history[at] + float32((s.history[(at-1)&127]-s.history[at])*e.fraction)
			reflectedAt := (at - e.reflection) & 127
			reflected := s.history[reflectedAt] + float32((s.history[(reflectedAt-1)&127]-s.history[reflectedAt])*e.fraction)
			sample += float32(reflected * e.echo)
			e.low += float32((sample - e.low) * e.alpha)
			filtered := float32(e.low * e.gain)
			if channel == 0 {
				left += filtered
			} else {
				right += filtered
			}
		}
	}
	d.position = (d.position + 1) & 127
	return left, right
}
