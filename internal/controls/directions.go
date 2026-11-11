package controls

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// Properties a direction can name. They are the per-track fields of
// cicada audition --json an agent steers by.
const (
	PropertyLevel      = "level"      // rms_dbfs
	PropertyBrightness = "brightness" // centroid_hz
	PropertyAttack     = "attack"     // attack_ms
	PropertyDecay      = "decay"      // decay20_ms
)

// Bases of a direction.
const (
	BasisStated   = "stated"   // certain from the signal path, such as an output gain
	BasisMeasured = "measured" // read from the probe table (directions.json)
	BasisUnknown  = "unknown"
)

// Changes a direction reports when the control rises from its low value to
// its high value.
const (
	ChangeUp    = "up"
	ChangeDown  = "down"
	ChangeMixed = "mixed" // rises then falls, or falls then rises
	ChangeNone  = "none"  // stays within the threshold
)

// Direction says which way a measured property moves when the control rises.
type Direction struct {
	Property string `json:"property"`
	Change   string `json:"change"`
	Basis    string `json:"basis"`
	// At and Measured are the probed control values (low, default, high) and
	// the property at each; Unit is the property's unit.
	At       []float64 `json:"at,omitempty"`
	Measured []float64 `json:"measured,omitempty"`
	Unit     string    `json:"unit,omitempty"`
}

// Thresholds below which a probed change counts as none.
const (
	LevelThresholdDB      = 1.0  // dB
	BrightnessThreshold   = 0.05 // relative change of centroid_hz
	AttackThresholdMS     = 2.0  // ms, and 10% of the larger value
	DecayThreshold        = 0.10 // relative change of decay20_ms
	timeRelativeThreshold = 0.10
)

// MeasuredTable is the probe table checked in as directions.json.
type MeasuredTable struct {
	Schema string `json:"schema"`
	// Phrase describes what the probe rendered for each owner kind.
	Phrase   map[string]string        `json:"phrase"`
	Controls map[string]ProbedControl `json:"controls"`
}

// ProbedControl holds what the listening report measured at three values of
// one control: its low value, its default and its high value.
type ProbedControl struct {
	At         []float64 `json:"at"`
	LevelDB    []float64 `json:"level_db"`
	CentroidHz []float64 `json:"centroid_hz"`
	AttackMS   []float64 `json:"attack_ms"`
	DecayMS    []float64 `json:"decay_ms"`
}

// MeasuredSchema names directions.json's format.
const MeasuredSchema = "cicada.measured-directions.v1"

//go:embed directions.json
var measuredData []byte

// Measured returns the checked-in probe table.
func Measured() MeasuredTable {
	var table MeasuredTable
	if len(measuredData) == 0 {
		return MeasuredTable{Schema: MeasuredSchema, Phrase: map[string]string{}, Controls: map[string]ProbedControl{}}
	}
	if err := json.Unmarshal(measuredData, &table); err != nil {
		panic(fmt.Errorf("decode measured directions: %w", err))
	}
	if table.Controls == nil {
		table.Controls = map[string]ProbedControl{}
	}
	return table
}

// Directions turns one probe result into directions.
func (p ProbedControl) Directions() []Direction {
	return []Direction{
		direction(PropertyLevel, "dB", p.At, p.LevelDB, func(a, b float64) bool { return math.Abs(b-a) >= LevelThresholdDB }),
		direction(PropertyBrightness, "Hz", p.At, p.CentroidHz, func(a, b float64) bool {
			return math.Abs(b-a) >= BrightnessThreshold*math.Max(math.Abs(a), math.Abs(b))
		}),
		direction(PropertyAttack, "ms", p.At, p.AttackMS, func(a, b float64) bool {
			return math.Abs(b-a) >= AttackThresholdMS && math.Abs(b-a) >= timeRelativeThreshold*math.Max(math.Abs(a), math.Abs(b))
		}),
		direction(PropertyDecay, "ms", p.At, p.DecayMS, func(a, b float64) bool {
			return math.Abs(b-a) >= DecayThreshold*math.Max(math.Abs(a), math.Abs(b))
		}),
	}
}

// direction reads low, default and high. A step counts when significant
// says so; two counted steps with opposite signs are mixed.
func direction(property, unit string, at, values []float64, significant func(a, b float64) bool) Direction {
	d := Direction{Property: property, Basis: BasisMeasured, At: at, Measured: values, Unit: unit, Change: ChangeNone}
	if len(values) != 3 {
		return d
	}
	sign := func(a, b float64) int {
		if !significant(a, b) {
			return 0
		}
		if b > a {
			return 1
		}
		return -1
	}
	first, second, whole := sign(values[0], values[1]), sign(values[1], values[2]), sign(values[0], values[2])
	switch {
	case first*second < 0:
		d.Change = ChangeMixed
	case whole > 0 || first > 0 || second > 0:
		d.Change = ChangeUp
	case whole < 0 || first < 0 || second < 0:
		d.Change = ChangeDown
	}
	return d
}

// statedLouder reports the controls that are a plain gain on the owner's
// output, so raising them makes it louder whatever else the patch does.
func statedLouder(kind string, c Control) bool {
	switch kind {
	case KindVoice, KindPatch, KindKitLane, KindMix, KindBus, KindMaster:
	default:
		return c.Owner == "ampcab" && c.Name == "output"
	}
	if c.Type != "number" {
		return false
	}
	switch c.Name {
	case "gain", "output", "level":
		return true
	}
	return c.Owner == "drums" && strings.HasSuffix(c.Name, "_level")
}

// applyDirections attaches stated and measured directions.
func (b *builder) applyDirections() {
	kinds := map[string]string{}
	for _, owner := range b.owners {
		kinds[owner.Name] = owner.Kind
	}
	table := Measured()
	for i := range b.controls {
		c := &b.controls[i]
		if probe, ok := table.Controls[c.ID]; ok {
			c.Directions = probe.Directions()
		}
		if statedLouder(kinds[c.Owner], *c) {
			stated := Direction{Property: PropertyLevel, Change: ChangeUp, Basis: BasisStated}
			replaced := false
			for j := range c.Directions {
				if c.Directions[j].Property == PropertyLevel {
					// The probe verifies the statement; keep its numbers.
					c.Directions[j].Basis = BasisStated
					replaced = true
				}
			}
			if !replaced {
				c.Directions = append([]Direction{stated}, c.Directions...)
			}
		}
	}
}

// DirectionBasis is the best basis among a control's directions: measured,
// stated or unknown.
func DirectionBasis(c Control) string {
	basis := BasisUnknown
	for _, d := range c.Directions {
		if d.Basis == BasisMeasured {
			return BasisMeasured
		}
		if d.Basis == BasisStated {
			basis = BasisStated
		}
	}
	return basis
}
