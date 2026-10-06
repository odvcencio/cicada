// Package capture transfers raw mono/stereo input and timing from an audio
// callback to a writer. Storage and asset publication belong to the writer.
package capture

// ClockDomain identifies a clock shared by input and output timestamps.
type ClockDomain uint8

const (
	ClockUnknown ClockDomain = iota
	ClockHostMonotonic
	ClockSimulation
)

type TimestampReference uint8

const (
	ReferenceUnknown TimestampReference = iota
	ReferenceFirstFrame
	ReferenceClockObservation
)

type Timestamp struct {
	Nano      int64
	Valid     bool
	Domain    ClockDomain
	Reference TimestampReference
}

type DevicePosition struct {
	Position, Frequency uint64
	Valid               bool
}

type ChannelLayout uint8

const (
	LayoutUnknown ChannelLayout = iota
	LayoutMono
	LayoutStereo
)

type Discontinuity uint8

const (
	DeviceDiscontinuity Discontinuity = 1 << iota
	QueueOverrun
	InvalidBlock
)

// Calibration describes only the residual offset AFTER clock mapping. Positive
// RemainingFrames means captured audio still arrives late and moves earlier.
// Epoch zero is uncalibrated; calibration must match the active device epoch.
type Calibration struct {
	DeviceEpoch     uint64
	RemainingFrames int64
	Valid           bool
}

// Block describes the first frame of borrowed callback input. EngineFrame is
// the render cursor, which may stay still while an armed device keeps running.
// Latencies are reported nanoseconds, never an instruction to subtract RTT.
type Block struct {
	EngineEpoch, DeviceEpoch uint64
	EngineFrame              int64
	DeviceFrame              uint64
	InputPosition            DevicePosition
	OutputPosition           DevicePosition
	InputTime, OutputTime    Timestamp
	SampleRate, Period       int
	Frames                   int
	InputLatencyNano         int64
	OutputLatencyNano        int64
	InputLatencyValid        bool
	OutputLatencyValid       bool
	Calibration              Calibration
	Layout                   ChannelLayout
	Flags                    Discontinuity
	DeviceDropouts           uint32
	GapFrames                uint64
}
