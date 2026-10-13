package project

import (
	"fmt"
	"math"

	"m31labs.dev/cicada/kernel/seq"
)

// SceneTempoPath is the scene setting that changes the song tempo at the
// scene boundary.
const SceneTempoPath = "tempo"

// CanonicalTempo rounds a tempo to the nearest thousandth of a BPM. Every
// consumer (engine, tempo map, renders, MIDI, durations) works in whole
// milli-BPM, so the project stores, serializes and compiles this one value.
func CanonicalTempo(bpm float64) float64 { return math.Round(bpm*1000) / 1000 }

// SceneTempoMilli reads a scene's tempo in thousandths of a BPM. It reports
// false when the scene does not set a tempo or the value is not in 20..300.
func SceneTempoMilli(scene Scene) (int, bool) {
	for _, setting := range scene.Settings {
		if setting.Path != SceneTempoPath || setting.Value.Number == nil {
			continue
		}
		milli := math.Round(CanonicalTempo(*setting.Value.Number) * 1000)
		if milli < 20_000 || milli > 300_000 {
			return 0, false
		}
		return int(milli), true
	}
	return 0, false
}

// SlowestTempoMilli is the lowest tempo the song reaches: the score tempo or
// any scene tempo. A synced delay must fit its buffer at this tempo.
func SlowestTempoMilli(p *Project) int {
	slowest := p.TempoMilli
	for _, scene := range p.Scenes {
		if milli, ok := SceneTempoMilli(scene); ok && milli < slowest {
			slowest = milli
		}
	}
	return slowest
}

// TempoChange is a step in the song tempo: from the start of Bar (counted from
// the first bar of the song) the tempo is BPMMilli.
type TempoChange struct {
	Bar      int
	BPMMilli int
}

// SongTempoChanges lists where one pass of the song changes tempo. A scene
// without a tempo keeps the tempo before it. A scene that repeats the current
// tempo adds no change. An arrangement has no scenes and so no changes.
func SongTempoChanges(p *Project) []TempoChange {
	if p == nil || p.Arrange != nil {
		return nil
	}
	scenes := make(map[string]int, len(p.Scenes))
	for _, scene := range p.Scenes {
		if milli, ok := SceneTempoMilli(scene); ok {
			scenes[scene.ID] = milli
		}
	}
	var changes []TempoChange
	current, bar := p.TempoMilli, 0
	for _, entry := range p.Song {
		if milli, ok := scenes[entry.Scene]; ok && milli != current {
			changes = append(changes, TempoChange{Bar: bar, BPMMilli: milli})
			current = milli
		}
		bar += int(entry.Bars)
	}
	return changes
}

// TempoMap converts between song ticks and samples across the tempo steps. It
// chains clocks the way the kernel transport does, so offline renders, the
// native player, and the kernel place every bar on the same sample.
type TempoMap struct {
	clocks []seq.Clock
}

// NewTempoMap builds the map for a score tempo and its steps in bar order.
func NewTempoMap(sampleRate int, tempoMilli int64, changes []TempoChange) (*TempoMap, error) {
	clock, err := seq.NewClock(sampleRate, tempoMilli)
	if err != nil {
		return nil, err
	}
	m := &TempoMap{clocks: []seq.Clock{clock}}
	for _, change := range changes {
		if change.Bar < 0 || change.Bar > 1<<20 {
			return nil, fmt.Errorf("tempo change bar %d is out of range", change.Bar)
		}
		if clock, err = clock.Reanchor(int64(change.Bar)*seq.TicksPerBar, int64(change.BPMMilli)); err != nil {
			return nil, err
		}
		m.clocks = append(m.clocks, clock)
	}
	return m, nil
}

// ClockAtTick is the clock that governs a tick: the latest step at or before it.
func (m *TempoMap) ClockAtTick(tick int64) seq.Clock {
	clock := m.clocks[0]
	for _, candidate := range m.clocks[1:] {
		if candidate.AnchorTick > tick {
			break
		}
		clock = candidate
	}
	return clock
}

// ClockAtSample is the clock that governs a sample.
func (m *TempoMap) ClockAtSample(sample int64) seq.Clock {
	clock := m.clocks[0]
	for _, candidate := range m.clocks[1:] {
		if candidate.AnchorSample > sample {
			break
		}
		clock = candidate
	}
	return clock
}

func (m *TempoMap) SampleAtTick(tick int64) int64 { return m.ClockAtTick(tick).SampleAtTick(tick) }
func (m *TempoMap) TickAtSample(sample int64) int64 {
	return m.ClockAtSample(sample).TickAtSample(sample)
}

// Changed reports whether the map has any step.
func (m *TempoMap) Changed() bool { return len(m.clocks) > 1 }

// ExcerptTempoChanges is the tempo state a render of bars [from, from+bars)
// needs: the tempo at its first bar, if that differs from the score tempo,
// followed by the changes inside the range. Changes before the range are not
// needed, so a short excerpt of a long song stays within a decoder's limit.
func ExcerptTempoChanges(scoreMilli int, changes []TempoChange, from, bars int) []TempoChange {
	current := scoreMilli
	var out []TempoChange
	for _, change := range changes {
		if change.Bar <= from {
			current = change.BPMMilli
		}
	}
	if current != scoreMilli {
		out = append(out, TempoChange{Bar: from, BPMMilli: current})
	}
	for _, change := range changes {
		if change.Bar > from && change.Bar < from+bars {
			out = append(out, change)
		}
	}
	return out
}

// SongSeconds is the length of the first bars of the song in seconds, summed
// across the tempo steps. It is for display; playback and renders use TempoMap.
func SongSeconds(p *Project, bars int) float64 {
	changes := SongTempoChanges(p)
	var seconds float64
	tempo, from := float64(p.TempoMilli), 0
	for _, change := range changes {
		if change.Bar >= bars {
			break
		}
		seconds += float64(change.Bar-from) * 4 * 60000 / tempo
		tempo, from = float64(change.BPMMilli), change.Bar
	}
	return seconds + float64(bars-from)*4*60000/tempo
}
