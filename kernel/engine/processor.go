package engine

// StereoProcessor is host-prepared audio state. Constructors and asset loading
// run before Engine.New. Process, Reset and Fault must allocate no memory and
// must not perform I/O, lock, or call back into the host. One engine owns each
// processor instance. Project images deliberately reject prepared processors;
// WASM hosts load the optional mix/convolution companion modules separately.
type StereoProcessor interface {
	Process(float32, float32) (float32, float32)
	Reset()
	Fault() bool
	LatencyFrames() int
}

// MasterProcessorLatencyFrames is the extra latency introduced by an opt-in
// prepared master processor. Existing track insert and limiter latency is
// independent and unchanged.
func (e *Engine) MasterProcessorLatencyFrames() int {
	if e.masterProcessor == nil {
		return 0
	}
	return e.masterProcessor.LatencyFrames()
}
