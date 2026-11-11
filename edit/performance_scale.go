package edit

import (
	"fmt"

	"m31labs.dev/cicada/kernel/seq"
)

// ScalePerformance changes musical timing while keeping pitches and controls.
// Every tick is multiplied by numerator/denominator, so 2/1 plays at half
// speed and 1/2 at double speed.
//
// Example: {"kind":"scaleperformance","entity":"pattern:phrase","numerator":1,"denominator":2}
type ScalePerformance struct {
	Entity      EntityID `json:"entity" cicada:"required"`      // the performance pattern, as pattern:<name>
	Numerator   int      `json:"numerator" cicada:"required"`   // 1–16
	Denominator int      `json:"denominator" cicada:"required"` // 1–16
}

func (ScalePerformance) Kind() string { return "scaleperformance" }

func init() {
	Register("scaleperformance", func() Intent { return &ScalePerformance{} })
	Handle("scaleperformance", editPerformance)
}

// The caller owns a deep copy. Round absolute time once so note-relative
// controls remain aligned with the shared lanes, including fractional ratios.
// Samples sharing a resulting tick retain the later controller value.
func scalePerformance(p *seq.Performance, numerator, denominator int) error {
	if numerator < 1 || numerator > 16 || denominator < 1 || denominator > 16 {
		return fmt.Errorf("time scale needs a numerator and denominator from 1 to 16")
	}
	if err := p.Validate(); err != nil {
		return err
	}
	scale := func(tick int64) int64 {
		return (tick*int64(numerator) + int64(denominator)/2) / int64(denominator)
	}
	length := scale(p.LengthTicks)
	if length < 30 || length > 1<<40 {
		return fmt.Errorf("scaled loop must be 30 to %d ticks", int64(1<<40))
	}
	for i := range p.Groups {
		group := &p.Groups[i]
		points := group.Points[:0]
		for _, point := range group.Points {
			point.Tick = min(scale(point.Tick), length-1)
			if last := len(points) - 1; last >= 0 && points[last].Tick == point.Tick {
				points[last] = point
			} else {
				points = append(points, point)
			}
		}
		group.Points = points
	}
	for i := range p.Notes {
		note := &p.Notes[i]
		start := min(scale(note.Tick), length-1)
		end := max(start+1, scale(note.EndTick))
		points := note.Expressions[:0]
		for _, point := range note.Expressions {
			point.Tick = min(max(0, scale(note.Tick+point.Tick)-start), end-start)
			if last := len(points) - 1; last >= 0 && points[last].Tick == point.Tick {
				points[last] = point
			} else {
				points = append(points, point)
			}
		}
		note.Tick, note.EndTick, note.Expressions = start, end, points
	}
	p.LengthTicks = length
	if err := p.Validate(); err != nil {
		return fmt.Errorf("this time scale cannot retain the notes: %w; choose a longer duration", err)
	}
	return nil
}
