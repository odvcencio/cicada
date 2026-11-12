package control

import (
	"testing"

	"m31labs.dev/cicada/kernel/seq"
)

// The engine's grid must be the zero meter map's, position for position.
func TestGridIsTheZeroMeterMap(t *testing.T) {
	var meterMap seq.MeterMap
	if GridBarTicks() != meterMap.BarTicks(0) || GridBeatTicks() != meterMap.BeatTicks(0) {
		t.Fatalf("grid bar %d and beat %d differ from the zero map", GridBarTicks(), GridBeatTicks())
	}
	for tick := int64(0); tick < 40*meterMap.BarTicks(0); tick += 37 {
		bar, beat, step := GridPosition(tick)
		wantBar, wantBeat, wantStep, _ := meterMap.Position(tick)
		if bar != wantBar || beat != wantBeat || step != wantStep {
			t.Fatalf("tick %d: grid @%d.%d.%d, map @%d.%d.%d", tick, bar, beat, step, wantBar, wantBeat, wantStep)
		}
	}
}
