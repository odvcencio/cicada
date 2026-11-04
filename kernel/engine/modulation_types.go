package engine

// Modulator kinds in a ModulatorSpec.
const (
	ModKindLFO uint8 = iota
	ModKindEnvelope
	ModKindSway
	ModKindContour
)

// LFO waves in a ModulatorSpec, in kernel/modulate order.
const (
	ModWaveSine uint8 = iota
	ModWaveTriangle
	ModWaveSquare
	ModWaveSampleHold
)

// Contour segment shapes in a ContourPoint, in kernel/modulate order.
const (
	ModShapeLinear uint8 = iota
	ModShapeStep
	ModShapeSmooth
	ModShapeExponential
)

// Limits shared with the notation and the image.
const (
	MaxModulators    = 32
	MaxModRoutes     = 64
	MaxContourPoints = 64
)

// ContourPoint is a breakpoint: At is a Q32 fraction of the period (0 to
// 1<<32) and Value a Q30 level.
type ContourPoint struct {
	At    uint64
	Value int32
	Shape uint8
}

// ModulatorSpec is one control source. A synced source sets RateTicks, a
// free-rate source RateMicroHz. Attack and Release are Q32 one-pole
// coefficients per control-grid point. Loop continue fits whole cycles to the
// song loop so the value does not jump at the wrap.
type ModulatorSpec struct {
	Kind        uint8
	Wave        uint8
	Octaves     uint8
	Continue    bool
	Source      uint8 // envelope: the followed track
	RateTicks   uint32
	RateMicroHz uint64
	PhaseQ32    uint32
	Attack      uint32
	Release     uint32
	Points      []ContourPoint `json:",omitempty"`
}

// RouteSpec sends a source to one track parameter. Routes apply in the order
// of Config.Routes, after automation and scene settings, then clamp once.
type RouteSpec struct {
	Source   uint8
	Track    uint8
	Param    uint16
	Multiply bool
	Depth    float32 // add: base units of the parameter; multiply: a factor
}

// MacroMapping maps a named macro's normalized value into one parameter range.
type MacroMapping struct {
	Macro       uint8
	Track       uint8
	Param       uint16
	Min         float32
	Max         float32
	Exponential bool
}
