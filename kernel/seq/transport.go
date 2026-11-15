package seq

import "m31labs.dev/cicada/kernel/cmd"

// Transport keeps the musical tick fixed while stopped, even as audio blocks
// continue to pass. Tempo changes are applied on a bar boundary of its meter
// map, which is 4/4 until SetMeterMap gives it another.
type Transport struct {
	clock        Clock
	sample       int64
	tick         int64
	playing      bool
	pendingTempo int64
	applyTick    int64
	meters       MeterMap
	// The bar that held the tick when Bar last looked: bar barIndex, ticks
	// barStart up to barEnd. Empty (barEnd 0) until Bar fills it.
	barIndex, barStart, barEnd int64
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
func (t *Transport) Clock() Clock    { return t.clock }
func (t *Transport) PendingTempo() (int64, int64) {
	return t.pendingTempo, t.applyTick
}

// MeterMap is the map that places the transport's bar lines.
func (t *Transport) MeterMap() MeterMap { return t.meters }

// SetMeterMap replaces the meter map. Call it before playback.
func (t *Transport) SetMeterMap(m MeterMap) { t.meters, t.barStart, t.barEnd = m, 0, 0 }

// Bar returns the zero-based bar that holds the tick and the tick's offset
// into it, as MeterMap().BarAt(Tick()) does. The engine asks once per audio
// frame, so the bar is kept and the map is asked only when the tick leaves it.
func (t *Transport) Bar() (bar, offset int64) {
	if t.tick < t.barStart || t.tick >= t.barEnd {
		bar, offset := t.meters.BarAt(t.tick)
		t.barIndex, t.barStart, t.barEnd = bar, t.tick-offset, t.meters.NextBarLine(t.tick)
	}
	return t.barIndex, t.tick - t.barStart
}

// NextBarLine returns the first bar line after the tick, as
// MeterMap().NextBarLine(Tick()) does.
func (t *Transport) NextBarLine() int64 {
	t.Bar()
	return t.barEnd
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
		return validationError("seek tick cannot be negative")
	}
	t.tick = tick
	t.clock.AnchorSample = t.sample
	t.clock.AnchorTick = tick
	if t.pendingTempo != 0 {
		t.applyTick = t.meters.QuantizeBars(tick, 1)
	}
	return nil
}

// SetTempoNow changes the tempo at the current position. The clock anchors at
// the present sample and tick, so every later tick maps with the new tempo and
// earlier ticks keep the mapping they were played with. Scene tempo uses it,
// because a scene launch is already on a bar boundary.
//
// The caller has already checked the range; scene tempos are validated when the
// engine loads.
func (t *Transport) SetTempoNow(bpmMilli int64) {
	t.clock.AnchorSample = t.sample
	t.clock.AnchorTick = t.tick
	t.clock.BPMMilli = bpmMilli
}

func (t *Transport) QueueTempo(bpmMilli int64) error {
	if bpmMilli < minBPMMilli || bpmMilli > maxBPMMilli {
		return validationError("tempo must be 20 to 300 BPM")
	}
	t.pendingTempo = bpmMilli
	t.applyTick = t.meters.QuantizeBars(t.tick, 1)
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

// QuantizeTick returns the first requested boundary at or after tick, on the
// map's beat and bar lines. Pattern-end quantization uses the active pattern length.
func (m MeterMap) QuantizeTick(tick int64, quantize cmd.Quantize, patternLength uint8) (int64, error) {
	if tick < 0 {
		return 0, validationError("quantize tick cannot be negative")
	}
	switch quantize {
	case 0:
		return tick, nil
	case 1:
		return m.QuantizeBeat(tick), nil
	case 2:
	case 3:
		if patternLength < 1 || patternLength > 64 {
			return 0, validationError("pattern length must be 1 to 64")
		}
		return nextMultiple(tick, int64(patternLength)*TicksPerStep), nil
	default:
		if quantize < 5 || quantize > 20 {
			return 0, validationError("quantize value is out of range")
		}
	}
	// One bar (2), or quantize-4 bars (5 to 20).
	bars := int64(1)
	if quantize > 4 {
		bars = int64(quantize - 4)
	}
	return m.QuantizeBars(tick, bars), nil
}

func nextMultiple(value, quantum int64) int64 {
	quotient := value / quantum
	if value%quantum != 0 {
		quotient++
	}
	return quotient * quantum
}
