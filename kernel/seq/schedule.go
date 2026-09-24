package seq

// Event is one note onset with both musical and absolute sample positions.
// Gate-off scheduling and voice rendering are later M0 work.
type Event struct {
	Tick         int64
	Sample       int64
	Offset       int32
	Track        uint8
	Slot         uint8
	StepIndex    uint8
	RatchetIndex uint8
	Note         uint8
	Velocity     uint8
	Accent       bool
	Slide        bool
}

// SwingDelayTicks returns the delay of an odd grid step. For example,
// 80 permille corresponds to notation swing=54 and delays by 19 ticks.
func SwingDelayTicks(permille uint16) int64 {
	return (TicksPerStep*int64(permille) + 500) / 1000
}

// SwingFromPercent100 maps the notation's hundredths of a percent to the
// packed pattern field: 5000=50%, 5400=54%, 7500=75%.
func SwingFromPercent100(percent100 uint16) (uint16, error) {
	if percent100 < 5000 || percent100 > 7500 {
		return 0, Error("swing percent must be 50 to 75")
	}
	return (percent100 - 5000 + 2) / 5, nil
}

// RatchetTick divides a step into n parts. Any remainder is left in the
// final part, matching the documented 7-way 34/34/34/34/34/34/36 split.
func RatchetTick(start, end int64, n, index uint8) int64 {
	return start + int64(index)*((end-start)/int64(n))
}

// ProbabilityHit makes the same decision regardless of block size or host.
// SplitMix64 is the fixed hash64 implementation for this foundation.
func ProbabilityHit(probability uint8, seed uint32, track, slot uint8, iteration int64, step uint8) bool {
	if probability == 0 {
		return false
	}
	if probability >= 100 {
		return true
	}
	x := uint64(seed) ^ uint64(track)<<32 ^ uint64(slot)<<40 ^ uint64(step)<<48
	x ^= uint64(iteration) * 0x9e3779b97f4a7c15
	x += 0x9e3779b97f4a7c15
	x = (x ^ x>>30) * 0xbf58476d1ce4e5b9
	x = (x ^ x>>27) * 0x94d049bb133111eb
	u := (x ^ x>>31) >> 40
	return u*100 < uint64(probability)<<24
}

// EventsInBlock writes note onsets for [startSample, startSample+frames).
// It never allocates. overflow is true if dst could not hold every event.
// The caller must retry with a larger preallocated buffer before rendering.
func EventsInBlock(p *Pattern, clock Clock, track, slot uint8, startSample int64, frames int, dst []Event) (written int, overflow bool) {
	if p == nil || p.Len == 0 || frames <= 0 {
		return 0, false
	}
	startTick := clock.TickAtSample(startSample)
	endTick := clock.TickAtSample(startSample + int64(frames))
	firstStep := startTick/TicksPerStep - 1
	if firstStep < 0 {
		firstStep = 0
	}
	lastStep := endTick/TicksPerStep + 1
	for absoluteStep := firstStep; absoluteStep <= lastStep; absoluteStep++ {
		stepIndex := uint8(absoluteStep % int64(p.Len))
		step, err := UnpackStep(p.Steps[stepIndex])
		if err != nil || !step.Gate || step.Tie {
			continue
		}
		iteration := absoluteStep / int64(p.Len)
		if !ProbabilityHit(step.Probability, p.Seed, track, slot, iteration, stepIndex) {
			continue
		}
		incomingSlide := false
		if absoluteStep > 0 {
			previous := absoluteStep - 1
			previousIndex := uint8(previous % int64(p.Len))
			previousStep, err := UnpackStep(p.Steps[previousIndex])
			if err == nil && previousStep.Gate && previousStep.Slide {
				previousIteration := previous / int64(p.Len)
				incomingSlide = ProbabilityHit(previousStep.Probability, p.Seed, track, slot, previousIteration, previousIndex)
			}
		}
		start := absoluteStep*TicksPerStep + swingDelay(p.SwingPermille, absoluteStep)
		end := (absoluteStep+1)*TicksPerStep + swingDelay(p.SwingPermille, absoluteStep+1)
		for r := uint8(0); r < step.Ratchet; r++ {
			tick := RatchetTick(start, end, step.Ratchet, r)
			sample := clock.SampleAtTick(tick)
			if sample < startSample || sample >= startSample+int64(frames) {
				continue
			}
			if written == len(dst) {
				overflow = true
				continue
			}
			dst[written] = Event{
				Tick: tick, Sample: sample, Offset: int32(sample - startSample),
				Track: track, Slot: slot, StepIndex: stepIndex, RatchetIndex: r,
				Note: step.Note, Velocity: step.Velocity, Accent: step.Accent, Slide: incomingSlide && r == 0,
			}
			written++
		}
	}
	return written, overflow
}

func swingDelay(permille uint16, absoluteStep int64) int64 {
	if absoluteStep&1 == 0 {
		return 0
	}
	return SwingDelayTicks(permille)
}
