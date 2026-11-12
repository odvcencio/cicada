package notation

import (
	"fmt"
	"strconv"
	"strings"

	"m31labs.dev/cicada/kernel/seq"
)

// Meter is a declared time signature: Beats beats of a 1/Unit note in each
// bar. Groups split the beats for accents and grid shading; they never move a
// position.
type Meter struct {
	Beats    int64
	Unit     int64
	Groups   []int64 `json:",omitempty"`
	Position Position
}

// String writes the meter as source: "3/4" or "7/8 2+2+3".
func (m Meter) String() string {
	text := strconv.FormatInt(m.Beats, 10) + "/" + strconv.FormatInt(m.Unit, 10)
	if len(m.Groups) == 0 {
		return text
	}
	groups := make([]string, len(m.Groups))
	for i, g := range m.Groups {
		groups[i] = strconv.FormatInt(g, 10)
	}
	return text + " " + strings.Join(groups, "+")
}

// BarTicks is the length of one bar of the meter.
func (m Meter) BarTicks() int64 { return m.Beats * (seq.TicksPerWhole / m.Unit) }

// MeterMap places the score's bar and beat lines: one segment of the declared
// meter, or the zero map (4/4) for a score that declares none or declares an
// invalid one, which Validate reports.
func (s *Score) MeterMap() seq.MeterMap { return s.meterMap }

// parseMeter reads a meter declaration's fraction and beat groups. It returns
// a diagnostic message and code for the first problem.
func parseMeter(fraction, groups string) (Meter, string, string) {
	var m Meter
	numerator, denominator, ok := strings.Cut(fraction, "/")
	beats, err1 := strconv.ParseInt(numerator, 10, 64)
	unit, err2 := strconv.ParseInt(denominator, 10, 64)
	if !ok || err1 != nil || err2 != nil || beats < 1 || beats > 32 || unit != 2 && unit != 4 && unit != 8 && unit != 16 {
		return m, "CICADA-METER", "meter must be N/D with N from 1 to 32 and D of 2, 4, 8 or 16"
	}
	m.Beats, m.Unit = beats, unit
	if ticks := m.BarTicks(); ticks > seq.MaxBarTicks {
		return m, "CICADA-LIMIT", fmt.Sprintf("a bar of %s is %d ticks; the limit is %d", fraction, ticks, seq.MaxBarTicks)
	}
	if groups == "" {
		return m, "", ""
	}
	var sum int64
	for _, part := range strings.Split(groups, "+") {
		g, err := strconv.ParseInt(part, 10, 64)
		if err != nil || g < 1 || g > 32 {
			return m, "CICADA-METER", "beat groups must be whole numbers from 1 to 32 joined by +, such as 2+2+3"
		}
		m.Groups = append(m.Groups, g)
		sum += g
	}
	if sum != beats {
		return m, "CICADA-METER", fmt.Sprintf("beat groups %s add up to %d; %s has %d beats", groups, sum, fraction, beats)
	}
	return m, "", ""
}

// resolveMeter builds the score's meter map once every declaration in every
// file is read, before anything converts a position.
func resolveMeter(s *Score) {
	if s.Meter == nil {
		return
	}
	if m, err := seq.NewMeterMap([]seq.MeterChange{{Meter: seq.Meter{Beats: s.Meter.Beats, Unit: s.Meter.Unit}}}); err == nil {
		s.meterMap = m
	}
}
