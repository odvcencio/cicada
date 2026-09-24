package seq

import "m31labs.dev/cicada/kernel/cmd"

// Transport keeps the musical tick fixed while stopped, even as audio blocks
// continue to pass. Tempo changes are applied on a bar boundary.
type Transport struct {
	clock        Clock
	sample       int64
	tick         int64
	playing      bool
	pendingTempo int64
	applyTick    int64
}

func NewTransport(sampleRate int, bpmMilli int64) (Transport, error) {
	clock, err := NewClock(sampleRate, bpmMilli)
	if err != nil {
		return Transport{}, err
	}
	return Transport{clock: clock}, nil
}

func (t *Transport) Tick() int64     { return t.tick }
func (t *Transport) Sample() int64   { return t.sample }
func (t *Transport) Playing() bool   { return t.playing }
func (t *Transport) BPMMilli() int64 { return t.clock.BPMMilli }
func (t *Transport) PendingTempo() (int64, int64) {
	return t.pendingTempo, t.applyTick
}

func (t *Transport) Play() {
	if t.playing {
		return
	}
	t.clock.AnchorSample = t.sample
	t.clock.AnchorTick = t.tick
	t.playing = true
}

func (t *Transport) Stop() { t.playing = false }

func (t *Transport) SeekTick(tick int64) error {
	if tick < 0 {
		return Error("seek tick cannot be negative")
	}
	t.tick = tick
	t.clock.AnchorSample = t.sample
	t.clock.AnchorTick = tick
	if t.pendingTempo != 0 {
		t.applyTick = nextMultiple(tick, TicksPerBar)
	}
	return nil
}

func (t *Transport) QueueTempo(bpmMilli int64) error {
	if bpmMilli < minBPMMilli || bpmMilli > maxBPMMilli {
		return Error("tempo must be 20 to 300 BPM")
	}
	t.pendingTempo = bpmMilli
	t.applyTick = nextMultiple(t.tick, TicksPerBar)
	return nil
}

// Advance consumes an audio block. Tick values are half-open: an event at
// endTick belongs to the next block.
func (t *Transport) Advance(frames int) (startTick, endTick int64) {
	startTick = t.tick
	if frames <= 0 {
		return startTick, startTick
	}
	endSample := t.sample + int64(frames)
	if !t.playing {
		t.sample = endSample
		return startTick, startTick
	}
	if t.pendingTempo != 0 && t.applyTick >= t.tick {
		boundarySample := t.clock.SampleAtTick(t.applyTick)
		if boundarySample < t.sample {
			boundarySample = t.sample
		}
		if boundarySample <= endSample {
			t.clock.AnchorSample = boundarySample
			t.clock.AnchorTick = t.applyTick
			t.clock.BPMMilli = t.pendingTempo
			t.pendingTempo = 0
			t.applyTick = 0
		}
	}
	t.sample = endSample
	t.tick = t.clock.TickAtSample(endSample)
	return startTick, t.tick
}

// QuantizeTick returns the first requested boundary at or after tick.
// Pattern-end quantization uses the active pattern length.
func QuantizeTick(tick int64, quantize cmd.Quantize, patternLength uint8) (int64, error) {
	if tick < 0 {
		return 0, Error("quantize tick cannot be negative")
	}
	switch quantize {
	case 0:
		return tick, nil
	case 1:
		return nextMultiple(tick, PPQ), nil
	case 2:
		return nextMultiple(tick, TicksPerBar), nil
	case 3:
		if patternLength < 1 || patternLength > 64 {
			return 0, Error("pattern length must be 1 to 64")
		}
		return nextMultiple(tick, int64(patternLength)*TicksPerStep), nil
	default:
		if quantize < 5 || quantize > 20 {
			return 0, Error("quantize value is out of range")
		}
		return nextMultiple(tick, int64(quantize-4)*TicksPerBar), nil
	}
}

func nextMultiple(value, quantum int64) int64 {
	quotient := value / quantum
	if value%quantum != 0 {
		quotient++
	}
	return quotient * quantum
}
