package paramdefs

import "testing"

// A Hz parameter whose range reaches zero cannot use a log curve.
func TestInstrumentParamDescriptorUsesLinearBelowZero(t *testing.T) {
	if d := InstrumentParamDescriptor(0, "cutoff", "hz", 420); d.Curve != "log" || d.Min != 1 || d.Max != 20000 || d.Unit != "Hz" {
		t.Fatalf("cutoff: %+v", d)
	}
	if d := InstrumentParamDescriptor(0, "detune", "hz", 0); d.Curve != "linear" || d.Min != 0 {
		t.Fatalf("detune: %+v", d)
	}
	if d := InstrumentParamDescriptor(1, "index", "unit", 5000); d.Max != 5000 || d.ID != "instrument.p2" || d.Unit != "" {
		t.Fatalf("index: %+v", d)
	}
}
