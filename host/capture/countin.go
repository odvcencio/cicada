package capture

import "m31labs.dev/cicada/kernel/seq"

// CountIn uses the engine's integer clock and clicks the beats of the meter
// map's first bar.
// DefaultCountIn is one bar; zero bars skips it.
type CountIn struct {
	Frames int64
	beats  [maxCountInClicks]int64
	count  int
	perBar int // beats in a bar; the first of each is accented
}

// maxCountInClicks is the longest count-in the language allows: 8 bars of a
// meter with the largest numerator, 32 beats.
const maxCountInClicks = 8 * 32

// DefaultCountIn is one bar of the meter map's first bar.
func DefaultCountIn(meterMap seq.MeterMap, rate int, bpmMilli int64) (CountIn, error) {
	return NewCountIn(meterMap, rate, bpmMilli, 1)
}

// NewCountIn clicks the beats of bars bars of the meter map's first bar.
func NewCountIn(meterMap seq.MeterMap, rate int, bpmMilli int64, bars int) (CountIn, error) {
	if bars < 0 || bars > 8 {
		return CountIn{}, seq.Error("count-in must be 0 to 8 bars")
	}
	clock, err := seq.NewClock(rate, bpmMilli)
	if err != nil {
		return CountIn{}, err
	}
	perBar := int(meterMap.BeatsInBar(0))
	if bars*perBar > maxCountInClicks {
		return CountIn{}, seq.Error("count-in has more clicks than a meter can have")
	}
	c := CountIn{Frames: clock.SampleAtTick(meterMap.BarStart(int64(bars))), count: bars * perBar, perBar: perBar}
	for beat := 0; beat < c.count; beat++ {
		c.beats[beat] = clock.SampleAtTick(int64(beat) * meterMap.BeatTicks(0))
	}
	return c, nil
}

// click adds a short, deterministic count-in pulse to an already cleared output.
func (c CountIn) click(output [][]float32, cursor int64, frames int) {
	for beat := 0; beat < c.count; beat++ {
		start := c.beats[beat] - cursor
		for i := max(int64(0), start); i < min(int64(frames), start+32); i++ {
			level := float32(0.12)
			if beat%c.perBar == 0 {
				level = 0.2
			}
			if (i-start)&1 != 0 {
				level = -level
			}
			for _, ch := range output {
				if i < int64(len(ch)) {
					ch[i] = level
				}
			}
		}
	}
}
