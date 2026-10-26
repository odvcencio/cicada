package control

import "fmt"

// AudioRuntime contains native device reports, not loopback measurements.
// It is shared by the server-rendered panel and its live browser projection.
type AudioRuntime struct {
	Error                  string  `json:"error"`
	Armed                  bool    `json:"armed"`
	SampleRate             int     `json:"sampleRate"`
	PeriodFrames           int     `json:"periodFrames"`
	RequestedPeriodFrames  int     `json:"requestedPeriodFrames"`
	PeriodMS               float64 `json:"periodMs"`
	InputLatencyMS         float64 `json:"inputLatencyMs"`
	OutputLatencyMS        float64 `json:"outputLatencyMs"`
	InputLatencyKnown      bool    `json:"inputLatencyKnown"`
	OutputLatencyKnown     bool    `json:"outputLatencyKnown"`
	ReportedRoundTripMS    float64 `json:"reportedRoundTripMs"`
	ReportedRoundTripKnown bool    `json:"reportedRoundTripKnown"`
	MonitorFXLatencyFrames int     `json:"monitorFXLatencyFrames"`
	AudioPriority          string  `json:"audioPriority"`
	DeadlineKnown          bool    `json:"deadlineKnown"`
	CallbackBudgetPercent  float64 `json:"callbackBudgetPercent"`
	CallbackMaxUS          float64 `json:"callbackMaxUs"`
	Callbacks              uint64  `json:"callbacks"`
	Dropouts               uint64  `json:"dropouts"`
	Late                   uint64  `json:"late"`
	Warning                string  `json:"warning"`
}

type AudioRuntimeRow struct{ Key, Label, Value string }

func AudioRuntimeRows(runtime AudioRuntime, requested int) []AudioRuntimeRow {
	if requested == 0 {
		requested = 256
	}
	if runtime.RequestedPeriodFrames > 0 {
		requested = runtime.RequestedPeriodFrames
	}
	granted := "Device closed"
	if runtime.PeriodFrames > 0 && runtime.SampleRate > 0 {
		granted = fmt.Sprintf("%d frames · %.2f ms · %d Hz", runtime.PeriodFrames, float64(runtime.PeriodFrames)*1000/float64(runtime.SampleRate), runtime.SampleRate)
	}
	latency := func(value float64, known bool) string {
		if known {
			return fmt.Sprintf("%.2f ms", value)
		}
		return "Not reported"
	}
	priority := runtime.AudioPriority
	if priority == "" {
		priority = "Reported when device starts"
	}
	callback := "Waiting for callbacks"
	if runtime.Callbacks > 0 {
		callback = fmt.Sprintf("%.1f%% of buffer · %.0f μs", runtime.CallbackBudgetPercent, runtime.CallbackMaxUS)
	}
	deadline := "Not reported"
	if runtime.DeadlineKnown {
		deadline = fmt.Sprintf("Reported · %d late commits", runtime.Late)
	}
	monitorFX := "Device closed"
	if runtime.SampleRate > 0 {
		monitorFX = fmt.Sprintf("%d frames · %.2f ms", runtime.MonitorFXLatencyFrames, float64(runtime.MonitorFXLatencyFrames)*1000/float64(runtime.SampleRate))
	}
	return []AudioRuntimeRow{
		{"requested", "Requested buffer", fmt.Sprintf("%d frames", requested)},
		{"granted", "Granted buffer", granted},
		{"input", "Reported input latency", latency(runtime.InputLatencyMS, runtime.InputLatencyKnown)},
		{"output", "Reported output latency", latency(runtime.OutputLatencyMS, runtime.OutputLatencyKnown)},
		{"roundtrip", "Reported round trip", latency(runtime.ReportedRoundTripMS, runtime.ReportedRoundTripKnown)},
		{"monitorFX", "Monitor processing latency", monitorFX},
		{"priority", "Audio thread priority", priority},
		{"callback", "Peak callback", callback},
		{"deadline", "Device deadlines", deadline},
		{"dropouts", "Device dropouts", fmt.Sprint(runtime.Dropouts)},
	}
}

func AudioRuntimeWarning(runtime AudioRuntime) string {
	if runtime.Error != "" {
		return runtime.Error
	}
	return runtime.Warning
}
