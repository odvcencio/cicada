package seq

// Event is one note transition with both musical and absolute sample positions.
// On is zero so the original onset-only API remains compatible.
type EventKind uint8

const (
	NoteOn EventKind = iota
	NoteOff
)

type Event struct {
	Kind         EventKind
	NoteID       int64
	Tick         int64
	Sample       int64
	Offset       int32
	Track        uint8
	Slot         uint8
	StepIndex    uint8
	RatchetIndex uint8
	Note         uint8
	Notes        [4]uint8
	NoteCount    uint8 // zero means the unchanged mono Note field
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
	return EventsOffsetInBlock(p, clock, track, slot, 0, startSample, frames, dst)
}

// EventsOffsetInBlock starts step zero at startStep. It supports a pattern
// restart without changing the stored pattern or allocating in the callback.
func EventsOffsetInBlock(p *Pattern, clock Clock, track, slot uint8, startStep, startSample int64, frames int, dst []Event) (written int, overflow bool) {
	return EventsAtTickInBlock(p, clock, track, slot, startStep*TicksPerStep, startSample, frames, dst)
}

// EventsAtTickInBlock schedules a pattern with its first cell at an exact tick.
func EventsAtTickInBlock(p *Pattern, clock Clock, track, slot uint8, startTickOffset, startSample int64, frames int, dst []Event) (written int, overflow bool) {
	if p == nil || p.Len == 0 || frames <= 0 {
		return 0, false
	}
	grid := p.GridTicks()
	startStep := startTickOffset / grid
	offset := startTickOffset % grid
	startTick := clock.TickAtSample(startSample) - offset
	endTick := clock.TickAtSample(startSample+int64(frames)) - offset
	firstStep := startTick/grid - 1
	if firstStep < 0 {
		firstStep = 0
	}
	lastStep := endTick/grid + 1
	for absoluteStep := firstStep; absoluteStep <= lastStep; absoluteStep++ {
		localStep := absoluteStep - startStep
		if localStep < 0 {
			continue
		}
		stepIndex := uint8(localStep % int64(p.Len))
		step, err := UnpackStep(p.Steps[stepIndex])
		if err != nil || !step.Gate || step.Tie {
			continue
		}
		iteration := localStep / int64(p.Len)
		if !ProbabilityHit(step.Probability, p.Seed, track, slot, iteration, stepIndex) {
			continue
		}
		incomingSlide := false
		if localStep > 0 {
			previous := localStep - 1
			previousIndex := uint8(previous % int64(p.Len))
			previousStep, err := UnpackStep(p.Steps[previousIndex])
			if err == nil && previousStep.Gate && previousStep.Slide {
				previousIteration := previous / int64(p.Len)
				incomingSlide = ProbabilityHit(previousStep.Probability, p.Seed, track, slot, previousIteration, previousIndex)
			}
		}
		start := absoluteStep*grid + offset + swingDelayForGrid(p, absoluteStep)
		end := (absoluteStep+1)*grid + offset + swingDelayForGrid(p, absoluteStep+1)
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
				Kind: NoteOn, NoteID: absoluteStep*8 + int64(r) + 1,
				Tick: tick, Sample: sample, Offset: int32(sample - startSample),
				Track: track, Slot: slot, StepIndex: stepIndex, RatchetIndex: r,
				Note: uint8(int(step.Note) + int(p.Transpose)), Velocity: step.Velocity, Accent: step.Accent, Slide: incomingSlide && r == 0,
			}
			chord := p.Chords[stepIndex]
			dst[written].NoteCount = chord.Count
			for n := uint8(0); n < chord.Count; n++ {
				dst[written].Notes[n] = uint8(int(chord.Notes[n]) + int(p.Transpose))
			}
			written++
		}
	}
	return written, overflow
}

// EventsWithGatesInBlock emits note-on and note-off transitions into dst.
// It derives each event from absolute step position, so the result does not
// depend on the caller's block size or a previous call. A later voice engine
// can ignore an old NoteOff after a slide has started a newer note.
func EventsWithGatesInBlock(p *Pattern, clock Clock, track, slot uint8, startSample int64, frames int, dst []Event) (written int, overflow bool) {
	return EventsWithGatesOffsetInBlock(p, clock, track, slot, 0, startSample, frames, dst)
}

// EventsWithGatesOffsetInBlock emits notes and releases for a restarted
// pattern whose step zero is at startStep in the global transport grid.
func EventsWithGatesOffsetInBlock(p *Pattern, clock Clock, track, slot uint8, startStep, startSample int64, frames int, dst []Event) (written int, overflow bool) {
	return EventsWithGatesAtTickInBlock(p, clock, track, slot, startStep*TicksPerStep, startSample, frames, dst)
}

// EventsWithGatesAtTickInBlock includes releases at an exact restart tick.
func EventsWithGatesAtTickInBlock(p *Pattern, clock Clock, track, slot uint8, startTickOffset, startSample int64, frames int, dst []Event) (written int, overflow bool) {
	if p == nil || p.Len == 0 || frames <= 0 {
		return 0, false
	}
	written, overflow = EventsAtTickInBlock(p, clock, track, slot, startTickOffset, startSample, frames, dst)
	grid := p.GridTicks()
	startStep := startTickOffset / grid
	offset := startTickOffset % grid
	startTick := clock.TickAtSample(startSample) - offset
	endTick := clock.TickAtSample(startSample+int64(frames)) - offset
	firstStep := startTick/grid - int64(p.Len) - 2
	if firstStep < 0 {
		firstStep = 0
	}
	lastStep := endTick/grid + 1
	for absoluteStep := firstStep; absoluteStep <= lastStep; absoluteStep++ {
		localStep := absoluteStep - startStep
		if localStep < 0 {
			continue
		}
		stepIndex := uint8(localStep % int64(p.Len))
		step, err := UnpackStep(p.Steps[stepIndex])
		if err != nil || !step.Gate || step.Tie {
			continue
		}
		iteration := localStep / int64(p.Len)
		if !ProbabilityHit(step.Probability, p.Seed, track, slot, iteration, stepIndex) {
			continue
		}
		start := absoluteStep*grid + offset + swingDelayForGrid(p, absoluteStep)
		end := (absoluteStep+1)*grid + offset + swingDelayForGrid(p, absoluteStep+1)
		for r := uint8(0); r < step.Ratchet; r++ {
			onset := RatchetTick(start, end, step.Ratchet, r)
			segmentEnd := end
			if r+1 < step.Ratchet {
				segmentEnd = RatchetTick(start, end, step.Ratchet, r+1)
			}
			gateTicks := (segmentEnd - onset) * int64(p.GatePercent) / 100
			if gateTicks < 30 {
				gateTicks = 30
			}
			offTick := onset + gateTicks
			offSample := clock.SampleAtTick(offTick)
			if r+1 == step.Ratchet {
				offTick, offSample = extendedGateEnd(p, clock, track, slot, absoluteStep, startStep, offset, step, offTick)
			}
			if offSample < startSample || offSample >= startSample+int64(frames) {
				continue
			}
			if written == len(dst) {
				overflow = true
				continue
			}
			dst[written] = Event{
				Kind: NoteOff, NoteID: absoluteStep*8 + int64(r) + 1,
				Tick: offTick, Sample: offSample, Offset: int32(offSample - startSample),
				Track: track, Slot: slot, StepIndex: stepIndex, RatchetIndex: r,
				Note: step.Note,
			}
			chord := p.Chords[stepIndex]
			dst[written].NoteCount = chord.Count
			for n := uint8(0); n < chord.Count; n++ {
				dst[written].Notes[n] = uint8(int(chord.Notes[n]) + int(p.Transpose))
			}
			written++
		}
	}
	// The fixed destination buffer keeps this sort allocation free.
	for i := 1; i < written; i++ {
		current := dst[i]
		j := i
		for j > 0 && eventBefore(current, dst[j-1]) {
			dst[j] = dst[j-1]
			j--
		}
		dst[j] = current
	}
	return written, overflow
}

func extendedGateEnd(p *Pattern, clock Clock, track, slot uint8, absoluteStep, startStep, tickOffset int64, step Step, normalEnd int64) (int64, int64) {
	grid := p.GridTicks()
	lastEnd := normalEnd
	for offset := int64(1); offset <= int64(p.Len); offset++ {
		nextAbsolute := absoluteStep + offset
		nextLocal := nextAbsolute - startStep
		nextIndex := uint8(nextLocal % int64(p.Len))
		next, err := UnpackStep(p.Steps[nextIndex])
		if err != nil || !next.Gate || !ProbabilityHit(next.Probability, p.Seed, track, slot, nextLocal/int64(p.Len), nextIndex) {
			break
		}
		nextOnset := nextAbsolute*grid + tickOffset + swingDelayForGrid(p, nextAbsolute)
		if next.Tie {
			lastEnd = (nextAbsolute+1)*grid + tickOffset + swingDelayForGrid(p, nextAbsolute+1)
			continue
		}
		if offset == 1 && step.Slide {
			offSample := clock.SampleAtTick(nextOnset) + (clock.SampleRate+199)/200
			return clock.TickAtSample(offSample), offSample
		}
		break
	}
	if lastEnd > normalEnd {
		return lastEnd, clock.SampleAtTick(lastEnd)
	}
	return normalEnd, clock.SampleAtTick(normalEnd)
}

func eventBefore(a, b Event) bool {
	if a.Sample != b.Sample {
		return a.Sample < b.Sample
	}
	if a.Kind != b.Kind {
		return a.Kind == NoteOff
	}
	if a.Track != b.Track {
		return a.Track < b.Track
	}
	return a.NoteID < b.NoteID
}

func swingDelay(permille uint16, absoluteStep int64) int64 {
	if absoluteStep&1 == 0 {
		return 0
	}
	return SwingDelayTicks(permille)
}

func swingDelayForGrid(p *Pattern, step int64) int64 {
	if step&1 == 0 {
		return 0
	}
	return (p.GridTicks()*int64(p.SwingPermille) + 500) / 1000
}
