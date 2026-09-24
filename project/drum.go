package project

import (
	"fmt"
	"strings"

	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/notation"
)

// CompileDrumParams applies authored values to the six built-in M0 voices.
func CompileDrumParams(track notation.Track) ([drum.LaneCount]drum.Params, error) {
	var values [drum.LaneCount]drum.Params
	for lane := drum.Lane(0); lane < drum.LaneCount; lane++ {
		values[lane] = drum.DefaultParams(lane)
	}
	for _, source := range track.Params {
		parts := strings.SplitN(source.Name, "_", 2)
		if len(parts) != 2 {
			return values, fmt.Errorf("invalid drum parameter %s", source.Name)
		}
		lane, ok := drumLane(parts[0])
		if !ok {
			return values, fmt.Errorf("unsupported drum lane %s", parts[0])
		}
		if !validDrumParam(lane, parts[1]) {
			return values, fmt.Errorf("unsupported drum parameter %s", source.Name)
		}
		if parts[1] == "metal" {
			switch source.Value {
			case "on", "true":
				values[lane].Metal = true
			case "off", "false":
				values[lane].Metal = false
			default:
				return values, fmt.Errorf("%s requires on or off", source.Name)
			}
			continue
		}
		number, unit, err := parseBaseValue(source.Value)
		if err != nil {
			return values, fmt.Errorf("%s: %w", source.Name, err)
		}
		if err := applyDrumParam(&values[lane], lane, parts[1], number, unit); err != nil {
			return values, fmt.Errorf("%s: %w", source.Name, err)
		}
	}
	for lane := drum.Lane(0); lane < drum.LaneCount; lane++ {
		if err := values[lane].Validate(lane); err != nil {
			return values, fmt.Errorf("%s: %w", drum.Names[lane], err)
		}
	}
	return values, nil
}

func drumParamsFromValues(source map[string]Value) ([drum.LaneCount]drum.Params, error) {
	var values [drum.LaneCount]drum.Params
	for lane := drum.Lane(0); lane < drum.LaneCount; lane++ {
		values[lane] = drum.DefaultParams(lane)
	}
	for _, name := range sortedKeys(source) {
		parts := strings.SplitN(name, "_", 2)
		if len(parts) != 2 {
			return values, fmt.Errorf("invalid drum parameter %s", name)
		}
		lane, ok := drumLane(parts[0])
		if !ok {
			return values, fmt.Errorf("unsupported drum lane %s", parts[0])
		}
		if !validDrumParam(lane, parts[1]) {
			return values, fmt.Errorf("unsupported drum parameter %s", name)
		}
		value := source[name]
		if parts[1] == "metal" {
			if value.Unit != "enum" {
				return values, fmt.Errorf("%s requires on or off", name)
			}
			switch value.Text {
			case "on", "true":
				values[lane].Metal = true
			case "off", "false":
				values[lane].Metal = false
			default:
				return values, fmt.Errorf("%s requires on or off", name)
			}
			continue
		}
		if value.Number == nil {
			return values, fmt.Errorf("%s requires a number", name)
		}
		if err := applyDrumParam(&values[lane], lane, parts[1], *value.Number, value.Unit); err != nil {
			return values, fmt.Errorf("%s: %w", name, err)
		}
	}
	for lane := drum.Lane(0); lane < drum.LaneCount; lane++ {
		if err := values[lane].Validate(lane); err != nil {
			return values, fmt.Errorf("%s: %w", drum.Names[lane], err)
		}
	}
	return values, nil
}

func drumLane(name string) (drum.Lane, bool) {
	for lane, candidate := range drum.Names {
		if name == candidate {
			return drum.Lane(lane), true
		}
	}
	return 0, false
}

func validDrumParam(lane drum.Lane, name string) bool {
	switch lane {
	case drum.BD:
		switch name {
		case "tune", "decay", "sweep", "sweep_time", "click", "drive":
			return true
		}
	case drum.SD:
		switch name {
		case "tune", "tone", "mix", "snappy", "decay":
			return true
		}
	case drum.CH, drum.OH:
		switch name {
		case "tune", "decay", "tone", "metal":
			return true
		}
	case drum.CP:
		switch name {
		case "tone", "decay", "spread":
			return true
		}
	case drum.RS:
		switch name {
		case "tune", "decay":
			return true
		}
	}
	return false
}

func applyDrumParam(p *drum.Params, lane drum.Lane, name string, number float64, unit string) error {
	want := "unit"
	switch name {
	case "tune":
		if lane == drum.BD {
			want = "hz"
		}
	case "tone":
		if lane == drum.CH || lane == drum.OH || lane == drum.CP {
			want = "hz"
		}
	case "decay", "sweep_time", "snappy", "spread":
		want = "ms"
	case "sweep", "click", "drive", "mix":
	default:
		return fmt.Errorf("unknown drum parameter %s", name)
	}
	if unit != want {
		return fmt.Errorf("requires %s, got %s", want, unit)
	}
	switch name {
	case "tune":
		p.Tune = number
	case "tone":
		p.Tone = number
	case "decay":
		p.Decay = number / 1000
	case "sweep_time":
		p.SweepTime = number / 1000
	case "snappy":
		p.Snappy = number / 1000
	case "spread":
		p.Spread = number / 1000
	case "sweep":
		p.Sweep = number
	case "click":
		p.Click = number
	case "drive":
		p.Drive = number
	case "mix":
		p.Mix = number
	}
	return nil
}
