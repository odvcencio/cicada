// Package patternmachine makes deterministic, editable sequencer patterns.
package patternmachine

import (
	"fmt"
	"strings"
)

// Recipe describes version 1 of the pattern machine. Seed is kept as text at
// the HTTP boundary so JavaScript never rounds a 64-bit integer.
type Recipe struct {
	Version int    `json:"version"`
	Seed    uint32 `json:"seed"`
	Bars    int    `json:"bars"`
	Root    int    `json:"root"`
	Scale   string `json:"scale"`
	Density int    `json:"density"`
	Motion  int    `json:"motion"`
}

type Step struct {
	Pitch    int
	Velocity int
}
type Pattern struct {
	Notes []Step
	Lanes map[string][]int
}

func (r Recipe) Validate() error {
	if r.Version != 1 {
		return fmt.Errorf("unsupported pattern machine version")
	}
	if r.Bars != 1 && r.Bars != 2 && r.Bars != 4 {
		return fmt.Errorf("choose 1, 2, or 4 bars")
	}
	if r.Root < 0 || r.Root > 11 {
		return fmt.Errorf("choose a valid key")
	}
	if _, ok := scales[r.Scale]; !ok {
		return fmt.Errorf("choose a supported scale")
	}
	if r.Density < 0 || r.Density > 100 || r.Motion < 0 || r.Motion > 100 {
		return fmt.Errorf("density and motion must be between 0 and 100")
	}
	return nil
}

// random is an explicitly versioned, stateless hash. Each musical dimension
// has its own stream so changing velocity never changes which notes occur.
func random(seed uint32, stream, n uint64) uint64 {
	x := uint64(seed) ^ stream*0x9e3779b97f4a7c15 ^ n*0xbf58476d1ce4e5b9
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

// Generate creates an Orbit arpeggio or a Breakbeat drum pattern. Chord tones
// include the major third of the dominant, which a global minor-scale snap
// would discard. The result contains no live randomness or source edits.
func Generate(r Recipe, kind string, lanes []string) (Pattern, error) {
	if err := r.Validate(); err != nil {
		return Pattern{}, err
	}
	length := r.Bars * 16
	out := Pattern{}
	if kind == "drums" {
		out.Lanes = make(map[string][]int, len(lanes))
		for _, lane := range lanes {
			values := make([]int, length)
			for i := range values {
				pos, bar := i%16, i/16
				n := uint64(i)
				roll := int(random(r.Seed, 20+uint64(laneStream(lane)), n) % 100)
				velocity := 0
				switch lane {
				case "bd":
					if pos == 0 {
						velocity = 9
					} else if pos == 10 || pos == 6 && (bar%2 == 0 || r.Motion > 35) {
						velocity = 7
					} else if (pos == 3 || pos == 7 || pos == 13 || pos == 15) && roll < r.Density*r.Motion/100 {
						velocity = 3 + int(random(r.Seed, 31, n)%3)
					}
				case "sd":
					if pos == 4 || pos == 12 {
						velocity = 9
					} else if (pos == 3 || pos == 10 || pos == 14 || pos == 15 && bar%4 == 3) && roll < r.Density*r.Motion/100 {
						velocity = 2 + int(random(r.Seed, 32, n)%3)
					}
				case "ch":
					if pos%2 == 0 {
						velocity = 3
						if pos%4 == 0 {
							velocity = 5
						}
					} else if roll < r.Density*r.Motion/100 {
						velocity = 2
					}
				case "oh":
					if pos == 14 && bar%2 == 1 && roll < r.Density {
						velocity = 3
					}
				case "rs":
					if (pos == 4 || pos == 12) && roll < r.Motion {
						velocity = 2
					}
				default:
					if pos == 0 && bar%2 == 0 && roll < r.Density/2 {
						velocity = 3
					}
				}
				values[i] = velocity
			}
			out.Lanes[lane] = values
		}
		return out, nil
	}
	if kind != "notes" && kind != "acid" {
		return Pattern{}, fmt.Errorf("choose an instrument or drum track")
	}
	out.Notes = make([]Step, length)
	// Circle through i, VI, iv and V. For minor, raise the dominant third;
	// unlike snapping everything to the scale, this preserves its pull home.
	degrees := scales[r.Scale]
	degree := func(i int) int { return degrees[i%len(degrees)] + 12*(i/len(degrees)) }
	positions := [4]int{0, 5, 3, 4}
	if len(degrees) < 7 {
		positions = [4]int{0, len(degrees) - 2, 2, len(degrees) - 1}
	}
	var chords [4][6]int
	for bar, pos := range positions {
		chords[bar] = [6]int{degree(pos), degree(pos + 2), degree(pos + 4), degree(pos + len(degrees)), degree(pos + len(degrees) + 2), degree(pos + len(degrees) + 1)}
		if bar == 3 && (r.Scale == "minor" || r.Scale == "harmonic") {
			chords[bar][1] = degree(pos) + 4
			chords[bar][4] = degree(pos) + 16
		}
	}
	order := [16]int{0, 1, 2, 3, 4, 3, 2, 1, 0, 2, 3, 5, 4, 3, 2, 1}
	for i := range out.Notes {
		pos, bar := i%16, i/16
		// Anchor each half-bar so the phrase remains circular even at low density.
		if pos%8 != 0 && int(random(r.Seed, 1, uint64(i))%100) >= r.Density {
			continue
		}
		index := order[pos]
		if pos%8 != 0 && int(random(r.Seed, 2, uint64(i))%100) < r.Motion/3 {
			index = (index + 1 + int(random(r.Seed, 3, uint64(i))%2)) % 6
		}
		velocity := 72 + int(random(r.Seed, 4, uint64(i))%15) - 7
		if pos%4 == 0 {
			velocity += 14
		}
		out.Notes[i] = Step{Pitch: 48 + r.Root + chords[bar%4][index], Velocity: velocity}
	}
	return out, nil
}

func laneStream(lane string) int {
	switch lane {
	case "bd":
		return 1
	case "sd":
		return 2
	case "ch":
		return 3
	case "oh":
		return 4
	case "rs":
		return 5
	}
	return 6
}

var noteNames = [12]string{"c", "c#", "d", "d#", "e", "f", "f#", "g", "g#", "a", "a#", "b"}
var scales = map[string][]int{
	"major": {0, 2, 4, 5, 7, 9, 11}, "minor": {0, 2, 3, 5, 7, 8, 10}, "dorian": {0, 2, 3, 5, 7, 9, 10},
	"phrygian": {0, 1, 3, 5, 7, 8, 10}, "harmonic": {0, 2, 3, 5, 7, 8, 11}, "mixo": {0, 2, 4, 5, 7, 9, 10},
	"pent": {0, 3, 5, 7, 10}, "blues": {0, 3, 5, 6, 7, 10},
}

func Note(pitch int) string { return fmt.Sprintf("%s%d", noteNames[pitch%12], pitch/12-1) }

// SourceRows returns ordinary score rows, preserving the generated velocities.
func (p Pattern) SourceRows(kind string, lanes []string) (map[string]string, string) {
	rows := map[string]string{}
	if kind == "drums" {
		for _, lane := range lanes {
			var b strings.Builder
			for _, v := range p.Lanes[lane] {
				if v == 0 {
					b.WriteByte('.')
				} else {
					fmt.Fprintf(&b, "x%d", v)
				}
			}
			rows[lane] = b.String()
		}
		return rows, ""
	}
	notes := make([]string, len(p.Notes))
	velocities := make([]string, len(p.Notes))
	for i, n := range p.Notes {
		if n.Velocity == 0 {
			notes[i] = "."
			velocities[i] = "."
		} else {
			notes[i] = Note(n.Pitch)
			velocities[i] = fmt.Sprint(n.Velocity)
		}
	}
	if len(velocities) > 0 && velocities[0] == "." {
		velocities[0] = "80"
	}
	rows[""] = strings.Join(notes, " ")
	return rows, strings.Join(velocities, " ")
}
