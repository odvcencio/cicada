package main

import (
	"fmt"
	"strconv"

	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/project"
	"m31labs.dev/gosx"
)

func patternGridTiming(pattern project.Pattern) gosx.Node {
	step := int64(pattern.StepTicks)
	if step == 0 {
		step = seq.TicksPerStep
	}
	length := pattern.DurationTicks()
	duration := patternBeatLength(length)
	if length%seq.TicksPerBar == 0 {
		duration = fmt.Sprintf("%d bars", length/seq.TicksPerBar)
		if length == seq.TicksPerBar {
			duration = "1 bar"
		}
	}
	steps := fmt.Sprintf("%d steps", pattern.Steps)
	if pattern.Steps == 1 {
		steps = "1 step"
	}
	return gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted"), gosx.Attr("data-pattern-grid", pattern.ID), gosx.Attr("data-pattern-step-ticks", strconv.FormatInt(step, 10)), gosx.Attr("data-pattern-loop-ticks", strconv.FormatInt(length, 10))), gosx.Text(fmt.Sprintf("%s · %s · %s per loop", steps, patternGridName(step), duration)))
}

func patternGridName(ticks int64) string {
	for i, name := range []string{"whole", "half", "quarter", "eighth", "sixteenth", "thirty-second", "sixty-fourth", "128th"} {
		base := seq.TicksPerBar >> i
		switch {
		case ticks == base:
			return name + " notes"
		case ticks*3 == base*2:
			return name + "-note triplets"
		case ticks*2 == base*3:
			return "dotted " + name + " notes"
		}
	}
	return patternBeatLength(ticks) + " per step"
}

// Keep nonstandard grids exact instead of displaying rounded decimal beats.
func patternBeatLength(ticks int64) string {
	a, b := ticks, int64(seq.PPQ)
	for b != 0 {
		a, b = b, a%b
	}
	numerator, denominator := ticks/a, int64(seq.PPQ)/a
	if denominator == 1 {
		if numerator == 1 {
			return "1 beat"
		}
		return fmt.Sprintf("%d beats", numerator)
	}
	unit := "beats"
	if numerator < denominator {
		unit = "beat"
	}
	return fmt.Sprintf("%d/%d %s", numerator, denominator, unit)
}
