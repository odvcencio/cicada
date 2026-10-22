package musictime

import "fmt"

// TempoChange steps the tempo at a zero-based bar in a 4/4 timeline.
type TempoChange struct {
	Bar      int `json:"bar" cicada:"Zero-based bar of the tempo step" range:"0..1048576" introduced:"cicada.project/2"`
	BPMMilli int `json:"bpm_milli" cicada:"Tempo in thousandths of a BPM" unit:"milli-BPM" range:"20000..300000" introduced:"cicada.project/2"`
}

func ValidateTempoChanges(changes []TempoChange) error {
	if len(changes) > 65535 {
		return fmt.Errorf("tempo map exceeds 65535 steps")
	}
	previous := -1
	for _, change := range changes {
		if change.Bar <= previous || change.Bar > 1<<20 || change.BPMMilli < 20000 || change.BPMMilli > 300000 {
			return fmt.Errorf("tempo steps must increase by bar and stay within 20 to 300 BPM")
		}
		previous = change.Bar
	}
	return nil
}

// SecondsBetween measures a musical selection across its tempo steps. Audio
// playback uses the integer clock; this fractional value is for duration labels.
func SecondsBetween(tempoMilli int64, changes []TempoChange, start, end int64) float64 {
	if tempoMilli <= 0 || end <= start {
		return 0
	}
	from := start
	current := tempoMilli
	var seconds float64
	for _, step := range changes {
		at := int64(step.Bar) * 3840
		if at >= end {
			break
		}
		if at > start {
			seconds += float64(at-from) * 60000 / float64(current*960)
			from = at
		}
		current = int64(step.BPMMilli)
	}
	return seconds + float64(end-from)*60000/float64(current*960)
}
