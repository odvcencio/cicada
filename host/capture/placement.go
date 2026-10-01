package capture

import "errors"

// TimingConfidence describes the mapping, separately from residual calibration.
type TimingConfidence uint8

const (
	TimingUnavailable TimingConfidence = iota
	TimingReportedLatency
	TimingTimestamps
)

// Placement is saved beside the raw take. Source publication must retain these
// fields: raw PCM is never shifted, trimmed, resampled or overwritten here.
type Placement struct {
	RawEngineFrame    int64            `json:"rawEngineFrame"`
	MappedEngineFrame int64            `json:"mappedEngineFrame"`
	CorrectionFrames  int64            `json:"correctionFrames"`
	EngineFrame       int64            `json:"engineFrame"`
	Confidence        TimingConfidence `json:"timingConfidence"`
	Calibrated        bool             `json:"calibrated"`
}

// Place first maps the capture timestamp to the render cursor and then applies
// only the remaining calibrated offset. Reported RTT is used ONLY when first
// frame timestamps are unavailable. Clock observations need an estimated output
// lead and therefore never claim first-frame timestamp confidence.
func Place(b Block) Placement {
	p := Placement{RawEngineFrame: b.EngineFrame, MappedEngineFrame: b.EngineFrame}
	if b.SampleRate <= 0 {
		p.EngineFrame = p.MappedEngineFrame
		return p
	}
	in, out := b.InputTime, b.OutputTime
	if in.Valid && out.Valid && in.Domain != ClockUnknown && in.Domain == out.Domain && in.Reference == ReferenceFirstFrame {
		switch out.Reference {
		case ReferenceFirstFrame:
			p.MappedEngineFrame += nanoFrames(in.Nano-out.Nano, b.SampleRate)
			p.Confidence = TimingTimestamps
		case ReferenceClockObservation:
			if b.OutputLatencyValid {
				p.MappedEngineFrame += nanoFrames(in.Nano-out.Nano-b.OutputLatencyNano, b.SampleRate)
				p.Confidence = TimingReportedLatency
			}
		}
	} else if b.InputLatencyValid && b.OutputLatencyValid {
		p.MappedEngineFrame -= nanoFrames(b.InputLatencyNano+b.OutputLatencyNano, b.SampleRate)
		p.Confidence = TimingReportedLatency
	}
	if b.Calibration.Valid && b.DeviceEpoch != 0 && b.Calibration.DeviceEpoch == b.DeviceEpoch && p.Confidence != TimingUnavailable {
		p.CorrectionFrames = -b.Calibration.RemainingFrames
		p.Calibrated = true
	}
	p.EngineFrame = p.MappedEngineFrame + p.CorrectionFrames
	return p
}

// Round after converting the full signed delta, avoiding per-timestamp rounding
// and floating point precision loss at large host-clock epochs.
func nanoFrames(nano int64, rate int) int64 {
	seconds, remainder := nano/1_000_000_000, nano%1_000_000_000
	fraction := remainder * int64(rate)
	if fraction < 0 {
		fraction -= 500_000_000
	} else {
		fraction += 500_000_000
	}
	return seconds*int64(rate) + fraction/1_000_000_000
}

// Calibrate derives the remaining offset from a known loopback event in a raw
// capture block, after applying exactly the same mapping used for placement.
func Calibrate(b Block, inputFrame int, expectedEngineFrame int64) (Calibration, error) {
	b.Calibration = Calibration{}
	p := Place(b)
	if p.Confidence == TimingUnavailable || b.DeviceEpoch == 0 || inputFrame < 0 || inputFrame >= b.Frames || b.Flags != 0 || b.GapFrames != 0 {
		return Calibration{}, errors.New("calibration needs a continuous, clock-mapped loopback frame")
	}
	return Calibration{DeviceEpoch: b.DeviceEpoch, RemainingFrames: p.MappedEngineFrame + int64(inputFrame) - expectedEngineFrame, Valid: true}, nil
}
