package control

import (
	"strings"
	"testing"
)

func TestAudioRuntimeReportsUnknownLatencyWithoutFabrication(t *testing.T) {
	rows := AudioRuntimeRows(AudioRuntime{PeriodFrames: 240, SampleRate: 48000, RequestedPeriodFrames: 64, Callbacks: 20, CallbackMaxUS: 1000, CallbackBudgetPercent: 20}, 256)
	byKey := make(map[string]string)
	for _, row := range rows {
		byKey[row.Key] = row.Value
	}
	if byKey["requested"] != "64 frames" || byKey["granted"] != "240 frames · 5.00 ms · 48000 Hz" || byKey["input"] != "Not reported" || byKey["output"] != "Not reported" || byKey["roundtrip"] != "Not reported" || !strings.Contains(byKey["callback"], "20.0%") {
		t.Fatalf("incorrect live projection: %+v", rows)
	}
	if got := AudioRuntimeWarning(AudioRuntime{Error: "Device unplugged", Warning: "Increase buffer"}); got != "Device unplugged" {
		t.Fatalf("device error hidden: %q", got)
	}
}
