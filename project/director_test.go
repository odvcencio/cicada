package project

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"m31labs.dev/cicada/notation"
)

func TestDirectorSurfaceAndRoundTrip(t *testing.T) {
	source, err := os.ReadFile("../examples/game-director.cicada")
	if err != nil {
		t.Fatal(err)
	}
	score, ds := notation.Parse(source)
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	p, ds := FromScore(score)
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	surface, err := DirectorSurfaceOf(p, 48000)
	if err != nil {
		t.Fatal(err)
	}
	if len(surface.States) != 2 || surface.Stingers[0].Track != 2 || surface.Stingers[0].Slot != 0 || surface.Stingers[0].CrossfadeFrames != 480 || surface.Transitions[1].CrossfadeFrames != 19200 {
		t.Fatalf("surface: %+v", surface)
	}
	data, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	copy, err := DecodeJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(copy.Live, p.Live) {
		t.Fatalf("JSON lost director: %s", data)
	}
	restored, err := ToSource(copy)
	if err != nil {
		t.Fatal(err)
	}
	parsed, ds := notation.Parse(restored)
	if len(ds) != 0 {
		t.Fatalf("source round trip: %+v\n%s", ds, restored)
	}
	if !reflect.DeepEqual(liveFromScore(parsed.Live), p.Live) {
		t.Fatal("source lost director")
	}
	manifest, err := json.Marshal(surface)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("manifest bytes=%d", len(manifest))
	copy.Tracks[2].Slots[0] = nil
	if err := ValidateProject(copy); err == nil {
		t.Fatal("missing stinger slot accepted")
	}
}
