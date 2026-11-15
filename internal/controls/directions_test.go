package controls

import (
	"bytes"
	"testing"
)

// directions.json is generated (CICADA_UPDATE_DIRECTIONS=1 go test
// ./host/soundprobe); it must be in the form Encode writes, one line per
// control, so a regeneration changes only the lines of controls that moved.
func TestDirectionsFileIsEncoded(t *testing.T) {
	table := Measured()
	if table.Schema != MeasuredSchema {
		t.Fatalf("directions.json has schema %q, want %q", table.Schema, MeasuredSchema)
	}
	encoded, err := table.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, measuredData) {
		t.Fatal("directions.json is not in the form MeasuredTable.Encode writes; regenerate it with CICADA_UPDATE_DIRECTIONS=1 go test ./host/soundprobe -run TestMeasuredDirectionsMatchTheTable")
	}
	if lines := bytes.Count(measuredData, []byte("\n")); lines > len(table.Controls)+len(table.Phrase)+8 {
		t.Fatalf("directions.json has %d lines for %d controls", lines, len(table.Controls))
	}
}

func TestDirectionsIgnoreSilentAndUnexercised(t *testing.T) {
	p := ProbedControl{At: []float64{0, 1, 2}, LevelDB: []float64{-89, -40, -30}, CentroidHz: []float64{0, 600, 600}, AttackMS: []float64{0, 20, 20}, DecayMS: []float64{0, 400, 400}, Silent: []bool{true, false, false}}
	for _, d := range p.Directions() {
		if len(d.At) != 2 || d.Basis != BasisMeasured {
			t.Fatalf("silent point retained: %+v", d)
		}
		if d.Property != PropertyLevel && d.Change != ChangeNone {
			t.Errorf("%s: %+v", d.Property, d)
		}
	}
	p.Silent = []bool{true, true, false}
	for _, d := range p.Directions() {
		if d.Basis != BasisUnknown || d.Change != ChangeUnknown {
			t.Errorf("one audible point: %+v", d)
		}
	}
	p = ProbedControl{At: []float64{0, 1, 2}, LevelDB: []float64{-40, -40, -40}, CentroidHz: []float64{600, 600, 600}, AttackMS: []float64{20, 20, 20}, DecayMS: []float64{400, 400, 400}}
	for _, d := range p.Directions() {
		if d.Basis != BasisUnknown || d.Change != ChangeUnknown {
			t.Errorf("unexercised: %+v", d)
		}
	}
}
