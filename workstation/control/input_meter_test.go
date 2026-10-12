package control

import (
	"math"
	"strings"
	"testing"
)

func TestInputMeterSeparatesRecordedLevelFromMonitorAndRetainsOverload(t *testing.T) {
	for _, v := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		if MeterDB(v) != -60 || MeterText(v) != "−∞ dBFS" {
			t.Fatal("invalid or silent input reached the meter")
		}
	}
	if math.Abs(MeterDB(.5)+6.0206) > .001 || !strings.HasPrefix(MeterText(2), "6.0") {
		t.Fatal("dBFS conversion hides an overload")
	}
	if MeterText(.999969) != "0.0 dBFS" {
		t.Fatal("full-scale input displays negative zero")
	}
	m := InputMeterState{Enabled: true, Active: true, Channels: 1, PeakL: .25, HeldPeakL: .999969, Clipped: true}
	status, hint := m.Status()
	if status != "Clipped input" || !strings.Contains(hint, "microphone volume") {
		t.Fatal("held clipping was hidden by a quieter current block")
	}
	m.Active = false
	if status, _ = m.Status(); status != "Clipped input" {
		t.Fatal("stopping a take hid its clipping")
	}
	m.Clipped = false
	if status, _ = m.Status(); status != "Ready to check input" {
		t.Fatal("idle input looks live")
	}
	m.Enabled = false
	if status, _ = m.Status(); status != "Microphone off" {
		t.Fatal("disabled input looks available")
	}
}
