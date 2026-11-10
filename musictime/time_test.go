package musictime

import (
	"testing"

	"m31labs.dev/cicada/kernel/seq"
)

func TestExactMusicalTicks(t *testing.T) {
	for _, tc := range []struct {
		value    string
		position bool
		want     int64
	}{
		{"@1.1.1", true, 0},
		{"@2.3.4", true, 6480},
		{"@2345624805922.4.4", true, 9007199254740240},
		{"0ticks", true, 0},
		{"9007199254740991ticks", true, 9007199254740991},
		{"9007199254740991ticks", false, 9007199254740991},
		{"1bar", false, 3840},
		{"1.5bars", false, 5760},
		{"1/16", false, 240},
		{"1/3", false, 1280},
		{"1/3840", false, 1},
		{"1/12", false, 320},
		{"7ticks", true, 7},
	} {
		got, err := MusicalTicks(seq.MeterMap{}, tc.value, tc.position)
		if err != nil || got != tc.want {
			t.Errorf("%s position=%t: got %d, %v; want %d", tc.value, tc.position, got, err, tc.want)
		}
	}
}

func TestMusicalTicksRejectsUnrepresentableValues(t *testing.T) {
	for _, value := range []string{"", "soon", "-1ticks", "0.5ticks", "3/2ticks", "1/7", "1/0", "NaNbars", "9007199254740992ticks", "999999999999999999999999999999bars", "@0.1.1", "@1.5.1", "@1.1.5", "@1.1", "@2345624805923.1.1", "@1.1.1.1", "@a.1.1"} {
		for _, position := range []bool{false, true} {
			if _, err := MusicalTicks(seq.MeterMap{}, value, position); err == nil {
				t.Errorf("accepted %q position=%t", value, position)
			}
		}
	}
	for _, value := range []string{"0ticks", "0bars", "@1.1.1"} {
		if _, err := MusicalTicks(seq.MeterMap{}, value, false); err == nil {
			t.Errorf("accepted zero/position duration %q", value)
		}
	}
}

// The notation/musicaltime copy rejected these; the merged parser must too.
func TestMusicalTicksRejectsPositionsAsDurations(t *testing.T) {
	for _, tc := range []struct {
		value    string
		position bool
	}{{"@1.1.1", false}, {"1/8", true}, {"0ticks", false}} {
		if _, err := MusicalTicks(seq.MeterMap{}, tc.value, tc.position); err == nil {
			t.Errorf("accepted %q position=%t", tc.value, tc.position)
		}
	}
}

// Positions and bar counts follow the meter map; note divisions do not.
func TestMusicalTicksFollowsTheMeterMap(t *testing.T) {
	waltz, err := seq.NewMeterMap([]seq.MeterChange{{Bar: 0, Meter: seq.Meter{Beats: 3, Unit: 4}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		value    string
		position bool
		want     int64
	}{{"@2.3.1", true, 4800}, {"@1.3.4", true, 2640}, {"1bar", false, 2880}, {"2bars", true, 5760}, {"1/4", false, 960}, {"1/8", false, 480}} {
		if got, err := MusicalTicks(waltz, tc.value, tc.position); err != nil || got != tc.want {
			t.Errorf("3/4 %q: got %d, %v; want %d", tc.value, got, err, tc.want)
		}
	}
	if _, err := MusicalTicks(waltz, "@1.4.1", true); err == nil {
		t.Error("accepted beat 4 of a 3/4 bar")
	}
}
