package paramdefs

import (
	"fmt"
	"strings"
)

// InstrumentParamSlots is how many parameters of one authored instrument can
// change while it plays. The registry rows instrument.p1 to instrument.p8
// carry them: the instrument's i-th declared parameter uses row i. The rows
// are slots, not paths, and have no voices, so no score path names them; a
// score names the parameter (bass.cutoff).
const InstrumentParamSlots = 8

// IsInstrumentParamSlot reports whether a registry row is one of the slots.
func IsInstrumentParamSlot(d Descriptor) bool { return strings.HasPrefix(d.ID, "instrument.p") }

// InstrumentParamDescriptor describes an authored instrument parameter as a
// live track setting in slot slot (0 for instrument.p1). unit is the
// instrument's unit word (hz, ms, db or unit). The parameter declares no
// range, so the range comes from its unit and always contains its default.
func InstrumentParamDescriptor(slot int, name, unit string, def float64) Descriptor {
	d := Descriptor{
		ID: fmt.Sprintf("instrument.p%d", slot+1), Path: name, Source: name, Scope: "track",
		Voices: []string{"instrument"}, Default: def, Curve: "linear", DisplayStep: 0.01,
		SmoothingMS: 20, Live: true, Automatable: true, Persist: "source",
	}
	switch unit {
	case "hz":
		d.Unit, d.Min, d.Max, d.Curve, d.DisplayStep = "Hz", 1, 20000, "log", 1
	case "ms":
		d.Unit, d.Min, d.Max, d.DisplayStep = "ms", 0, 60000, 1
	case "db":
		d.Unit, d.Min, d.Max, d.DisplayStep = "dB", -60, 24, 0.1
	default:
		d.Unit, d.Min, d.Max = "", -1000, 1000
	}
	if def < d.Min {
		d.Min = def
	}
	if def > d.Max {
		d.Max = def
	}
	if d.Curve == "log" && d.Min <= 0 {
		d.Curve = "linear" // a log curve needs positive values
	}
	return d
}
