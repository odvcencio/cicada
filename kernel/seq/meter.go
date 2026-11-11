package seq

// TicksPerWhole is a whole note. Note values such as 1/16 and 1/8T are
// fractions of it in every meter. It is not a bar length: ask a MeterMap.
const TicksPerWhole int64 = PPQ * 4

// MaxBarTicks is the longest bar a meter may have: 32/4 or 16/2.
const MaxBarTicks int64 = 8 * TicksPerWhole

// maxMeterBar bounds the bar of a meter change so a bar start fits in int64.
const maxMeterBar int64 = 1 << 32

// Meter is a time signature: Beats beats of a 1/Unit note in each bar. A beat
// is one denominator note, so 6/8 has six beats of 480 ticks.
type Meter struct {
	Beats int64 // numerator, 1 to 32
	Unit  int64 // denominator: 2, 4, 8 or 16
}

// MeterChange starts a meter at a zero-based bar.
type MeterChange struct {
	Bar   int64
	Meter Meter
}

// MeterMap places bar and beat lines on the tick timeline. Bars are counted
// from zero; a score's @1.1.1 is bar 0. The zero value is one 4/4 segment from
// tick 0, the map of every song that declares no meter, and its lookups are the
// same constant-divisor arithmetic as a fixed 3,840-tick bar. Every lookup is
// integer-only and allocation-free, so the audio callback may call it.
type MeterMap struct {
	segments []meterSegment // nil for the zero value
	lookups  *meterLookups  // set with segments by NewMeterMap
}

// meterLookups holds the lookups for maps other than the zero 4/4 map. Only
// NewMeterMap refers to these functions, so a program that never builds such
// a map links none of their code: in this phase the core WebAssembly kernel
// and Studio's browser engine keep only the 4/4 paths.
type meterLookups struct {
	barAt        func(MeterMap, int64) (int64, int64)
	barStart     func(MeterMap, int64) int64
	barSegment   func(MeterMap, int64) meterSegment
	nextBarLine  func(MeterMap, int64) int64
	barsUntil    func(MeterMap, int64) int64
	barsTicks    func(MeterMap, int64, int64) int64
	wholeBars    func(MeterMap, int64, int64) (int64, bool)
	quantizeBars func(MeterMap, int64, int64) int64
	quantizeBeat func(MeterMap, int64) int64
	position     func(MeterMap, int64) (int64, int64, int64, int64)
	positionTick func(MeterMap, int64, int64, int64) (int64, bool)
}

// meterSegment is a run of equal bars that begins on a bar line.
type meterSegment struct {
	tick, bar, barTicks, beatTicks int64
}

// NewMeterMap builds a map from meter changes in increasing bar order; the
// first must be at bar 0. A map of one 4/4 change is the zero value. It
// allocates, so build maps before playback.
func NewMeterMap(changes []MeterChange) (MeterMap, error) {
	if len(changes) == 0 || changes[0].Bar != 0 {
		return MeterMap{}, validationError("a meter map must start at bar 0")
	}
	segments := make([]meterSegment, len(changes))
	for i, change := range changes {
		meter := change.Meter
		if meter.Beats < 1 || meter.Beats > 32 || meter.Unit != 2 && meter.Unit != 4 && meter.Unit != 8 && meter.Unit != 16 {
			return MeterMap{}, validationError("meter must be N/D with N from 1 to 32 and D of 2, 4, 8 or 16")
		}
		beat := TicksPerWhole / meter.Unit
		s := meterSegment{bar: change.Bar, barTicks: meter.Beats * beat, beatTicks: beat}
		if s.barTicks > MaxBarTicks {
			return MeterMap{}, validationError("a bar may last at most 30720 ticks")
		}
		if i > 0 {
			previous := segments[i-1]
			if change.Bar <= previous.bar || change.Bar > maxMeterBar {
				return MeterMap{}, validationError("meter changes must increase by bar")
			}
			s.tick = previous.tick + (change.Bar-previous.bar)*previous.barTicks
		}
		segments[i] = s
	}
	if len(segments) == 1 && segments[0].barTicks == TicksPerWhole && segments[0].beatTicks == PPQ {
		return MeterMap{}, nil
	}
	return MeterMap{segments: segments, lookups: &meterLookups{
		barAt: MeterMap.barAt, barStart: MeterMap.barStart, barSegment: MeterMap.barSegment,
		nextBarLine: MeterMap.nextBarLine, barsUntil: MeterMap.barsUntil, barsTicks: MeterMap.barsTicks,
		wholeBars: MeterMap.wholeBars, quantizeBars: MeterMap.quantizeBars, quantizeBeat: MeterMap.quantizeBeat,
		position: MeterMap.position, positionTick: MeterMap.positionTick,
	}}, nil
}

// segment returns the segment that holds a tick (byBar false) or a zero-based
// bar (byBar true). Values before the first segment use the first.
func (m MeterMap) segment(value int64, byBar bool) meterSegment {
	s := m.segments
	lo, hi := 0, len(s)
	for hi-lo > 1 {
		mid := int(uint(lo+hi) >> 1)
		start := s[mid].tick
		if byBar {
			start = s[mid].bar
		}
		if start <= value {
			lo = mid
		} else {
			hi = mid
		}
	}
	return s[lo]
}

// Each lookup below runs the constant-divisor 4/4 arithmetic it replaced for
// the zero map, and otherwise one function from the map's lookups.

// BarAt returns the zero-based bar that holds tick and the tick's offset into
// it. Negative ticks divide toward zero, like the integer division they replace.
func (m MeterMap) BarAt(tick int64) (bar, offset int64) {
	if m.segments == nil {
		return tick / TicksPerWhole, tick % TicksPerWhole
	}
	return m.lookups.barAt(m, tick)
}

func (m MeterMap) barAt(tick int64) (bar, offset int64) {
	s := m.segment(tick, false)
	d := tick - s.tick
	return s.bar + d/s.barTicks, d % s.barTicks
}

// BarStart returns the first tick of a zero-based bar.
func (m MeterMap) BarStart(bar int64) int64 {
	if m.segments == nil {
		return bar * TicksPerWhole
	}
	return m.lookups.barStart(m, bar)
}

func (m MeterMap) barStart(bar int64) int64 {
	s := m.segment(bar, true)
	return s.tick + (bar-s.bar)*s.barTicks
}

// BarTicks returns the length of a zero-based bar.
func (m MeterMap) BarTicks(bar int64) int64 {
	if m.segments == nil {
		return TicksPerWhole
	}
	return m.lookups.barSegment(m, bar).barTicks
}

// BeatTicks returns the length of one beat, one denominator note, in a
// zero-based bar.
func (m MeterMap) BeatTicks(bar int64) int64 {
	if m.segments == nil {
		return PPQ
	}
	return m.lookups.barSegment(m, bar).beatTicks
}

// BeatsInBar returns the beats in a zero-based bar: its meter's numerator.
func (m MeterMap) BeatsInBar(bar int64) int64 {
	if m.segments == nil {
		return TicksPerWhole / PPQ
	}
	s := m.lookups.barSegment(m, bar)
	return s.barTicks / s.beatTicks
}

func (m MeterMap) barSegment(bar int64) meterSegment { return m.segment(bar, true) }

// IsBarLine reports whether a bar begins at tick.
func (m MeterMap) IsBarLine(tick int64) bool {
	_, offset := m.BarAt(tick)
	return offset == 0
}

// NextBarLine returns the first bar line after tick.
func (m MeterMap) NextBarLine(tick int64) int64 {
	if m.segments == nil {
		return (tick/TicksPerWhole + 1) * TicksPerWhole
	}
	return m.lookups.nextBarLine(m, tick)
}

func (m MeterMap) nextBarLine(tick int64) int64 {
	bar, _ := m.barAt(tick)
	return m.barStart(bar + 1)
}

// BarsUntil returns how many bars begin before tick, for tick >= 0: the bars a
// span from tick 0 to tick touches, and the zero-based bar of the first bar
// line at or after tick.
func (m MeterMap) BarsUntil(tick int64) int64 {
	if m.segments == nil {
		return (tick + TicksPerWhole - 1) / TicksPerWhole
	}
	return m.lookups.barsUntil(m, tick)
}

func (m MeterMap) barsUntil(tick int64) int64 {
	bar, offset := m.barAt(tick)
	if offset > 0 {
		bar++
	}
	return bar
}

// BarsTicks returns the length of count bars that begin with the bar holding
// from: what `2bars` at a position means.
func (m MeterMap) BarsTicks(from, count int64) int64 {
	if m.segments == nil {
		return count * TicksPerWhole
	}
	return m.lookups.barsTicks(m, from, count)
}

func (m MeterMap) barsTicks(from, count int64) int64 {
	bar, _ := m.barAt(from)
	return m.barStart(bar+count) - m.barStart(bar)
}

// WholeBars returns how many bars a length spans when it begins on the bar
// line of the bar holding from, and whether it ends on a bar line.
func (m MeterMap) WholeBars(from, ticks int64) (int64, bool) {
	if m.segments == nil {
		return ticks / TicksPerWhole, ticks%TicksPerWhole == 0
	}
	return m.lookups.wholeBars(m, from, ticks)
}

func (m MeterMap) wholeBars(from, ticks int64) (int64, bool) {
	bar, _ := m.barAt(from)
	end, offset := m.barAt(m.barStart(bar) + ticks)
	return end - bar, offset == 0
}

// QuantizeBars returns the first bar line at or after tick whose zero-based
// bar is a multiple of bars, which must be positive.
func (m MeterMap) QuantizeBars(tick, bars int64) int64 {
	if m.segments == nil {
		return nextMultiple(tick, bars*TicksPerWhole)
	}
	return m.lookups.quantizeBars(m, tick, bars)
}

func (m MeterMap) quantizeBars(tick, bars int64) int64 {
	return m.barStart(nextMultiple(m.barsUntil(tick), bars))
}

// QuantizeBeat returns the first beat line at or after tick.
func (m MeterMap) QuantizeBeat(tick int64) int64 {
	if m.segments == nil {
		return nextMultiple(tick, PPQ)
	}
	return m.lookups.quantizeBeat(m, tick)
}

func (m MeterMap) quantizeBeat(tick int64) int64 {
	bar, offset := m.barAt(tick)
	return m.barStart(bar) + nextMultiple(offset, m.barSegment(bar).beatTicks)
}

// Position returns the one-based @bar.beat.step that holds a tick of zero or
// more, and the ticks after that step's start. A step is a sixteenth note,
// counted within its beat.
func (m MeterMap) Position(tick int64) (bar, beat, step, rest int64) {
	if m.segments == nil {
		// In-bar remainders: x%3840%960 is x%960 under truncated division.
		r := tick % TicksPerWhole
		return tick/TicksPerWhole + 1, r/PPQ + 1, r%PPQ/TicksPerStep + 1, r % TicksPerStep
	}
	return m.lookups.position(m, tick)
}

func (m MeterMap) position(tick int64) (bar, beat, step, rest int64) {
	index, offset := m.barAt(tick)
	beatTicks := m.barSegment(index).beatTicks
	within := offset % beatTicks
	return index + 1, offset/beatTicks + 1, within/TicksPerStep + 1, within % TicksPerStep
}

// PositionTick returns the tick of a one-based @bar.beat.step, and false when
// the beat or step is outside its bar's meter: beats run 1 to the numerator
// and steps 1 to 16 divided by the denominator.
func (m MeterMap) PositionTick(bar, beat, step int64) (int64, bool) {
	if m.segments == nil {
		if bar < 1 || uint64(beat-1) >= uint64(TicksPerWhole/PPQ) || uint64(step-1) >= uint64(PPQ/TicksPerStep) {
			return 0, false
		}
		return (bar-1)*TicksPerWhole + (beat-1)*PPQ + (step-1)*TicksPerStep, true
	}
	return m.lookups.positionTick(m, bar, beat, step)
}

func (m MeterMap) positionTick(bar, beat, step int64) (int64, bool) {
	if bar < 1 || beat < 1 || step < 1 {
		return 0, false
	}
	s := m.barSegment(bar - 1)
	if beat > s.barTicks/s.beatTicks || step > s.beatTicks/TicksPerStep {
		return 0, false
	}
	return m.barStart(bar-1) + (beat-1)*s.beatTicks + (step-1)*TicksPerStep, true
}
