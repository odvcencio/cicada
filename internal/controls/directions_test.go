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
