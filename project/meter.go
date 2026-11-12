package project

import (
	"fmt"

	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
)

// Meter is the song's time signature: Numerator beats of a 1/Denominator
// note in each bar. Groups split the beats for accents; they never move a
// position. A project without a meter is 4/4.
type Meter struct {
	Numerator   int   `cicada:"Beats in a bar" range:"1..32" json:"numerator"`
	Denominator int   `cicada:"Note value of one beat: 2, 4, 8 or 16" json:"denominator"`
	Groups      []int `cicada:"Beat groups that add up to the numerator, for accents" json:"groups,omitempty"`
}

// formatAtLeast2 reports whether p uses cicada.project/2 or a later format.
func (p *Project) formatAtLeast2() bool { return p.Format == FormatID2 || p.Format == FormatID3 }

// useFormat2 moves a cicada.project/1 project to /2 and leaves a later format
// as it is.
func (p *Project) useFormat2() {
	if !p.formatAtLeast2() {
		p.Format, p.Version = FormatID2, 2
	}
}

// validMeter checks a meter the way the source parser does.
func validMeter(m *Meter) error {
	if m.Numerator < 1 || m.Numerator > 32 || m.Denominator != 2 && m.Denominator != 4 && m.Denominator != 8 && m.Denominator != 16 {
		return fmt.Errorf("CICADA-METER: meter must be N/D with N from 1 to 32 and D of 2, 4, 8 or 16")
	}
	if ticks := int64(m.Numerator) * (seq.TicksPerWhole / int64(m.Denominator)); ticks > seq.MaxBarTicks {
		return fmt.Errorf("CICADA-LIMIT: a bar of %d/%d is %d ticks; the limit is %d", m.Numerator, m.Denominator, ticks, seq.MaxBarTicks)
	}
	if len(m.Groups) == 0 {
		return nil
	}
	sum := 0
	for _, g := range m.Groups {
		if g < 1 || g > 32 {
			return fmt.Errorf("CICADA-METER: beat groups must be whole numbers from 1 to 32")
		}
		sum += g
	}
	if sum != m.Numerator {
		return fmt.Errorf("CICADA-METER: beat groups add up to %d; %d/%d has %d beats", sum, m.Numerator, m.Denominator, m.Numerator)
	}
	return nil
}

// MeterMap places the song's bar and beat lines: one segment of the song's
// meter, or the zero map (4/4) for a song without one. Bar counts, positions
// and tempo steps go through it.
func (p *Project) MeterMap() seq.MeterMap {
	var m seq.MeterMap
	if p.Meter != nil {
		if built, err := seq.NewMeterMap([]seq.MeterChange{{Meter: seq.Meter{Beats: int64(p.Meter.Numerator), Unit: int64(p.Meter.Denominator)}}}); err == nil {
			m = built
		}
	}
	return m
}

// SongEndTick is where the song section ends: the bar line after its last
// entry. Song entries count bars of the base meter.
func (p *Project) SongEndTick() int64 {
	var bars int64
	for _, entry := range p.Song {
		bars += int64(entry.Bars)
	}
	return p.MeterMap().BarStart(bars)
}

// meterWarnings notes each pattern that does not fill whole bars of the
// declared meter (it still plays, crossing bar lines), and refuses swing that
// a bar of the meter would split. A song without a meter
// declaration gets none, so check reports nothing new for it.
func meterWarnings(p *Project, score *notation.Score) []notation.Diagnostic {
	if p == nil || score == nil || score.SongMeter() == nil {
		return nil
	}
	bar := p.MeterMap().BarTicks(0)
	positions := map[string]notation.Position{}
	for _, pattern := range score.Patterns {
		positions[pattern.Name] = pattern.Position
	}
	var out []notation.Diagnostic
	for _, pattern := range p.Patterns {
		length := int64(pattern.Steps) * int64(pattern.StepTicks)
		if pattern.StepTicks == 0 {
			length = int64(pattern.Steps) * seq.TicksPerStep
		}
		if pattern.Performance != nil {
			length = pattern.Performance.LengthTicks
		}
		// Swing delays the odd cells of the pattern's grid counted from the song's
		// start (spec 2.3, rule 7). A bar that holds an odd number of cells would
		// swing different cells in every other bar, so such swing is refused.
		if pattern.Performance == nil && pattern.SwingPercent100 > 5000 {
			grid := int64(pattern.StepTicks)
			if grid == 0 {
				grid = seq.TicksPerStep
			}
			if bar%(2*grid) != 0 {
				meter := fmt.Sprintf("%d/%d", score.Meter.Beats, score.Meter.Unit)
				held := fmt.Sprintf("does not hold a whole number of its %d-tick cells", grid)
				if bar%grid == 0 {
					held = fmt.Sprintf("holds %d of its %d-tick cells, an odd number", bar/grid, grid)
				}
				out = append(out, notation.Diagnostic{Code: "CICADA-METER", Severity: "error", Position: positions[pattern.ID],
					Message: fmt.Sprintf("pattern %s swings, and a %s bar %s, so the swung cells would change from bar to bar; choose a step that fills a bar with an even number of cells, or remove swing", pattern.ID, meter, held)})
			}
		}
		if length%bar != 0 {
			out = append(out, notation.Diagnostic{Code: "CICADA-METER", Severity: "warning", Position: positions[pattern.ID],
				Message: fmt.Sprintf("pattern %s lasts %d ticks; a %s bar lasts %d, so it crosses bar lines", pattern.ID, length, fmt.Sprintf("%d/%d", score.Meter.Beats, score.Meter.Unit), bar)})
		}
	}
	return out
}
