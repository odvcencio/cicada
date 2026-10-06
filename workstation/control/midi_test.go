package control

import (
	"math"
	"testing"
)

func TestControllerCurvesAndOff(t *testing.T) {
	for _, d := range []Descriptor{{Min: 0, Max: 1, Curve: "linear"}, {Min: 20, Max: 20000, Curve: "log"}, {Min: -60, Max: 6, Curve: "fader", Off: true}, {Min: 0, Max: 1, Curve: "toggle"}, {Min: 0, Max: 3, Curve: "enum", Values: []string{"a", "b", "c", "d"}}} {
		previous := d.Min
		for cc := 0; cc <= 127; cc++ {
			v, err := CCValue(d, cc)
			if err != nil {
				t.Fatal(err)
			}
			if v == nil {
				if cc != 0 || !d.Off {
					t.Fatal("unexpected off")
				}
				continue
			}
			if *v < d.Min-1e-9 || *v > d.Max+1e-9 || *v < previous-1e-9 {
				t.Fatalf("nonmonotonic or out of range %s CC %d: %g", d.Curve, cc, *v)
			}
			previous = *v
		}
		if math.Abs(previous-d.Max) > 1e-8 {
			t.Fatalf("maximum not reachable for %s", d.Curve)
		}
	}
	for _, cc := range []int{-1, 128} {
		if _, err := CCValue(Descriptor{Min: 0, Max: 1}, cc); err == nil {
			t.Fatal("accepted invalid CC")
		}
	}
}

func TestRecordedNoteBoundsAndClockExtrapolation(t *testing.T) {
	valid := []Recording{{Track: "bass", Pattern: "pulse", Notes: []Note{{Tick: 0, EndTick: 120, Note: 60, Velocity: 100}}}}
	if err := ValidateRecordings(valid); err != nil {
		t.Fatal(err)
	}
	valid[0].Notes[0].EndTick = -1
	if ValidateRecordings(valid) == nil {
		t.Fatal("accepted inverted note duration")
	}
	if Tick(2, 3, 120, 1000, true) != 2400 || Tick(2, 3, 120, 1000, false) != 2160 {
		t.Fatal("native clock extrapolation was not bounded")
	}
}
