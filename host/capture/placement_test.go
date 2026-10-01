package capture

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestSimulatedTimingCompensationWithinOneSample(t *testing.T) {
	maxError := int64(0)
	for _, rate := range []int{44100, 48000, 96000} {
		for _, period := range []int{64, 128, 256, 480, 1024} {
			for _, offset := range []int64{-17, 0, 13} {
				for _, latency := range []int64{137, 637, 2401} {
					const epochNano = int64(8_000_000_000_000_000)
					b := Block{
						EngineEpoch: 2, DeviceEpoch: 3, EngineFrame: 10000, Frames: period, Period: period, SampleRate: rate,
						InputTime:        Timestamp{Nano: epochNano - int64(math.Round(float64(latency)*1e9/float64(rate))), Valid: true, Domain: ClockSimulation, Reference: ReferenceFirstFrame},
						OutputTime:       Timestamp{Nano: epochNano, Valid: true, Domain: ClockSimulation, Reference: ReferenceFirstFrame},
						InputLatencyNano: 9000000, OutputLatencyNano: 7000000, InputLatencyValid: true, OutputLatencyValid: true,
					}
					inputFrame := period / 2
					expected := b.EngineFrame - latency + int64(inputFrame) - offset
					calibration, err := Calibrate(b, inputFrame, expected)
					if err != nil || calibration.RemainingFrames != offset {
						t.Fatalf("residual offset = %+v, %v; want %d", calibration, err, offset)
					}
					b.Calibration = calibration
					p := Place(b)
					got := p.EngineFrame + int64(inputFrame)
					errorSamples := int64(math.Abs(float64(got - expected)))
					maxError = max(maxError, errorSamples)
					if errorSamples > 1 || p.MappedEngineFrame != b.EngineFrame-latency || p.CorrectionFrames != -offset || !p.Calibrated || p.Confidence != TimingTimestamps {
						t.Fatalf("rate %d period %d: %+v; event %d, expected %d", rate, period, p, got, expected)
					}
				}
			}
		}
	}
	t.Logf("placement error = %d samples across 135 simulated clock/period/calibration cases", maxError)
}

func TestPlacementConfidenceAndCalibrationEpoch(t *testing.T) {
	b := Block{DeviceEpoch: 7, EngineFrame: 1000, SampleRate: 48000, Frames: 256,
		InputTime:   Timestamp{Nano: 1000000000, Valid: true, Domain: ClockSimulation, Reference: ReferenceFirstFrame},
		OutputTime:  Timestamp{Nano: 1002000000, Valid: true, Domain: ClockSimulation, Reference: ReferenceFirstFrame},
		Calibration: Calibration{DeviceEpoch: 7, RemainingFrames: 10, Valid: true}}
	if got := Place(b); got.EngineFrame != 894 || got.CorrectionFrames != -10 {
		t.Fatalf("placement = %+v", got)
	}
	b.Calibration.DeviceEpoch = 6
	if got := Place(b); got.Calibrated || got.EngineFrame != 904 {
		t.Fatalf("stale calibration applied: %+v", got)
	}
	b.Calibration.DeviceEpoch = 7
	b.OutputTime.Domain = ClockHostMonotonic
	if got := Place(b); got.Confidence != TimingUnavailable || got.Calibrated || got.EngineFrame != 1000 {
		t.Fatalf("mixed clocks accepted: %+v", got)
	}
	b.InputTime.Valid, b.OutputTime.Valid = false, false
	b.InputLatencyNano, b.OutputLatencyNano = 2000000, 3000000
	b.InputLatencyValid, b.OutputLatencyValid = true, true
	if got := Place(b); got.Confidence != TimingReportedLatency || got.EngineFrame != 750 {
		t.Fatalf("reported mapping = %+v", got)
	}
	b.InputTime.Valid, b.OutputTime.Valid = true, true
	b.OutputTime.Domain = ClockSimulation
	b.OutputTime.Reference = ReferenceClockObservation
	if got := Place(b); got.Confidence != TimingReportedLatency || got.EngineFrame != 750 {
		t.Fatalf("observation mapping = %+v", got)
	}
	b.OutputLatencyValid = false
	if got := Place(b); got.Confidence != TimingUnavailable || got.Calibrated {
		t.Fatalf("unanchored observation accepted: %+v", got)
	}
}

func TestPlacementCorrectionIsSerializableWithoutChangingRawTiming(t *testing.T) {
	b := Block{DeviceEpoch: 1, EngineFrame: 1000, SampleRate: 48000, InputLatencyValid: true, OutputLatencyValid: true, InputLatencyNano: 1000000, OutputLatencyNano: 1000000, Calibration: Calibration{DeviceEpoch: 1, RemainingFrames: 7, Valid: true}}
	p := Place(b)
	data, err := json.Marshal(p)
	if err != nil || !strings.Contains(string(data), `"correctionFrames":-7`) || b.EngineFrame != 1000 {
		t.Fatalf("correction not source-visible: %s, %v", data, err)
	}
}

func TestCalibrationRejectsGapsAndMissingClockMapping(t *testing.T) {
	if _, err := Calibrate(Block{DeviceEpoch: 1, Frames: 256, SampleRate: 48000}, 0, 0); err == nil {
		t.Fatal("accepted unavailable mapping")
	}
}
