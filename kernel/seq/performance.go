package seq

// Performance stores independent gates in musical time. It is prepared and
// validated before playback, then remains immutable in the audio callback.
type Performance struct {
	LengthTicks int64
	Notes       []PerformedNote
	Groups      []ControlGroup
}

const MaxPerformanceNotes = 512
const MaxPerformanceExpressions = 65536

type PerformedNote struct {
	Tick, EndTick  int64
	Note, Velocity uint8
	Expressions    []PerformedExpression
	Group          uint16
}

// PerformedExpression is a controller sample relative to its note's onset.
// Relative positions keep gestures attached when a note moves in the score.
type PerformedExpression struct {
	Tick   int64
	Params Expression
}

func (p *Performance) Validate() error {
	if p == nil || p.LengthTicks < 30 || p.LengthTicks > 1<<40 || len(p.Notes) == 0 || len(p.Notes) > MaxPerformanceNotes {
		return validationError("a performance needs 1 to 512 notes and a valid loop length")
	}
	expressions := 0
	if len(p.Groups) > MaxPerformanceNotes {
		return validationError("too many shared gesture lanes")
	}
	for i, group := range p.Groups {
		if !validGroupName(group.Name) || len(group.Points) == 0 {
			return validationError("shared gestures need a name and points")
		}
		for _, other := range p.Groups[:i] {
			if other.Name == group.Name {
				return validationError("duplicate shared gesture name")
			}
		}
		previous := int64(-1)
		for _, point := range group.Points {
			if point.Tick <= previous || point.Tick < 0 || point.Tick >= p.LengthTicks {
				return validationError("shared gestures must be ordered within their loop")
			}
			if err := point.Value.Validate(); err != nil {
				return err
			}
			previous = point.Tick
		}
		expressions += len(group.Points)
		if expressions > MaxPerformanceExpressions {
			return validationError("too many gesture samples")
		}
	}
	for i, note := range p.Notes {
		if int(note.Group) > len(p.Groups) {
			return validationError("note refers to an unknown shared gesture lane")
		}
		if note.Tick < 0 || note.EndTick <= note.Tick || note.EndTick > p.LengthTicks || note.Note > 127 || note.Velocity < 1 || note.Velocity > 127 {
			return validationError("performed note pitch, velocity or gate is invalid")
		}
		for _, other := range p.Notes[:i] {
			if other.Note == note.Note && other.Tick < note.EndTick && note.Tick < other.EndTick {
				return validationError("overlapping gates for the same performed pitch need separate tracks")
			}
		}
		previous := int64(-1)
		for _, point := range note.Expressions {
			if point.Tick <= previous || point.Tick < 0 || point.Tick > note.EndTick-note.Tick || !point.Params.Set {
				return validationError("gesture samples must be strictly ordered within the note")
			}
			if err := point.Params.Validate(); err != nil {
				return err
			}
			previous = point.Tick
		}
		expressions += len(note.Expressions)
		if expressions > MaxPerformanceExpressions {
			return validationError("too many gesture samples")
		}
	}
	return nil
}

// PerformanceEventsInBlock preserves every onset, release and velocity. Gates
// ending at the loop boundary belong to the preceding iteration. Offs precede
// ons at the same sample, including a repeated pitch across the loop boundary.
// Like the step scheduler, this function never allocates and reports overflow.
func PerformanceEventsInBlock(p *Performance, clock Clock, track, slot uint8, startTick, startSample int64, frames int, transpose int8, dst []Event, gates bool) (written int, overflow bool) {
	if p == nil || p.LengthTicks < 30 || frames < 1 {
		return 0, false
	}
	first := max(int64(0), (clock.TickAtSample(startSample)-startTick)/p.LengthTicks-1)
	last := (clock.TickAtSample(startSample+int64(frames))-startTick)/p.LengthTicks + 1
	for iteration := first; iteration <= last; iteration++ {
		base := startTick + iteration*p.LengthTicks
		for i, note := range p.Notes {
			for kind := NoteOn; kind <= NoteOff; kind++ {
				if kind == NoteOff && !gates {
					continue
				}
				tick := base + note.Tick
				if kind == NoteOff {
					tick = base + note.EndTick
				}
				if tick < clock.AnchorTick {
					continue
				}
				sample := clock.SampleAtTick(tick)
				if sample < startSample || sample >= startSample+int64(frames) {
					continue
				}
				if written == len(dst) {
					overflow = true
					continue
				}
				dst[written] = Event{Kind: kind, Group: note.Group, NoteID: iteration*MaxPerformanceNotes + int64(i) + 1,
					Tick: tick, Sample: sample, Offset: int32(sample - startSample), Track: track, Slot: slot,
					Note: uint8(int(note.Note) + int(transpose)), Velocity: note.Velocity}
				written++
			}
			// A binary search skips old controller history even in a long held
			// note. Only samples in this block enter the pre-admitted event buffer.
			noteStart := base + note.Tick
			if !gates || len(note.Expressions) == 0 || clock.SampleAtTick(noteStart) >= startSample+int64(frames) || clock.SampleAtTick(base+note.EndTick) < startSample {
				continue
			}
			lo, hi := 0, len(note.Expressions)
			for lo < hi {
				mid := lo + (hi-lo)/2
				if tick := noteStart + note.Expressions[mid].Tick; tick < clock.AnchorTick || clock.SampleAtTick(tick) < startSample {
					lo = mid + 1
				} else {
					hi = mid
				}
			}
			for point := lo; point < len(note.Expressions); point++ {
				tick := noteStart + note.Expressions[point].Tick
				sample := clock.SampleAtTick(tick)
				if sample >= startSample+int64(frames) {
					break
				}
				if written == len(dst) {
					overflow = true
					continue
				}
				dst[written] = Event{Kind: NoteExpression, NoteID: iteration*MaxPerformanceNotes + int64(i) + 1,
					Tick: tick, Sample: sample, Offset: int32(sample - startSample), Track: track, Slot: slot,
					Note: uint8(int(note.Note) + int(transpose)), ExpressionIndex: uint16(point)}
				written++
			}
		}
		if gates {
			for i, group := range p.Groups {
				if group.Points[0].Tick > 0 && base >= clock.AnchorTick {
					sample := clock.SampleAtTick(base)
					if sample >= startSample && sample < startSample+int64(frames) {
						if written == len(dst) {
							overflow = true
						} else {
							dst[written] = Event{Kind: GroupExpression, Group: uint16(i + 1), NoteID: iteration * MaxPerformanceNotes, Tick: base, Sample: sample, Offset: int32(sample - startSample), Track: track, Slot: slot, StepIndex: 1}
							written++
						}
					}
				}
				lo, hi := 0, len(group.Points)
				for lo < hi {
					mid := lo + (hi-lo)/2
					tick := base + group.Points[mid].Tick
					if tick < clock.AnchorTick || clock.SampleAtTick(tick) < startSample {
						lo = mid + 1
					} else {
						hi = mid
					}
				}
				for point := lo; point < len(group.Points); point++ {
					tick := base + group.Points[point].Tick
					sample := clock.SampleAtTick(tick)
					if sample >= startSample+int64(frames) {
						break
					}
					if written == len(dst) {
						overflow = true
						continue
					}
					dst[written] = Event{Kind: GroupExpression, Group: uint16(i + 1), NoteID: iteration * MaxPerformanceNotes, Tick: tick, Sample: sample, Offset: int32(sample - startSample), Track: track, Slot: slot, ExpressionIndex: uint16(point)}
					written++
				}
			}
		}
	}
	sortPerformanceEvents(dst[:written])
	return written, overflow
}

// Heap sorting bounds dense controller bursts without callback allocations.
func sortPerformanceEvents(events []Event) {
	for root := len(events)/2 - 1; root >= 0; root-- {
		siftPerformanceEvents(events, root, len(events))
	}
	for end := len(events) - 1; end > 0; end-- {
		events[0], events[end] = events[end], events[0]
		siftPerformanceEvents(events, 0, end)
	}
}

func siftPerformanceEvents(events []Event, root, end int) {
	for child := root*2 + 1; child < end; child = root*2 + 1 {
		if child+1 < end && eventBefore(events[child], events[child+1]) {
			child++
		}
		if !eventBefore(events[root], events[child]) {
			break
		}
		events[root], events[child] = events[child], events[root]
		root = child
	}
}

// PeakEvents admits storage before rendering. It counts a closed tick window
// conservatively, including wrapped gates and controller samples at loop ends.
// This preparation helper may allocate; PerformanceEventsInBlock never does.
func (p *Performance) PeakEvents(window int64, gates bool) int {
	if p == nil || p.LengthTicks < 30 || window < 0 {
		return 0
	}
	count := 0
	for _, note := range p.Notes {
		count += len(note.Expressions)
	}
	for _, group := range p.Groups {
		count += len(group.Points)
		if len(group.Points) > 0 && group.Points[0].Tick > 0 {
			count++
		}
	}
	if gates {
		count += len(p.Notes) * 2
	}
	if count == 0 {
		return 0
	}
	ticks := make([]int64, 0, count)
	for _, note := range p.Notes {
		if gates {
			ticks = append(ticks, note.Tick%p.LengthTicks, note.EndTick%p.LengthTicks)
		}
		for _, point := range note.Expressions {
			ticks = append(ticks, (note.Tick+point.Tick)%p.LengthTicks)
		}
	}
	for _, group := range p.Groups {
		if len(group.Points) > 0 && group.Points[0].Tick > 0 {
			ticks = append(ticks, 0)
		}
		for _, point := range group.Points {
			ticks = append(ticks, point.Tick)
		}
	}
	for root := len(ticks)/2 - 1; root >= 0; root-- {
		siftPerformanceTicks(ticks, root, len(ticks))
	}
	for end := len(ticks) - 1; end > 0; end-- {
		ticks[0], ticks[end] = ticks[end], ticks[0]
		siftPerformanceTicks(ticks, 0, end)
	}
	cycles, remainder := window/p.LengthTicks, window%p.LengthTicks
	best, first := 0, 0
	for last := 0; last < len(ticks)*2; last++ {
		at := ticks[last%len(ticks)] + int64(last/len(ticks))*p.LengthTicks
		for at-(ticks[first%len(ticks)]+int64(first/len(ticks))*p.LengthTicks) > remainder {
			first++
		}
		best = max(best, last-first+1)
	}
	return int(cycles)*len(ticks) + best
}

func siftPerformanceTicks(ticks []int64, root, end int) {
	for child := root*2 + 1; child < end; child = root*2 + 1 {
		if child+1 < end && ticks[child] < ticks[child+1] {
			child++
		}
		if ticks[root] >= ticks[child] {
			break
		}
		ticks[root], ticks[child] = ticks[child], ticks[root]
		root = child
	}
}
