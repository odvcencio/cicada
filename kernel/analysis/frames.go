package analysis

// Frame is the analysis for one video frame.
type Frame struct {
	Bands          [Bands]float32
	Correlation    float64
	HasCorrelation bool
}

// Frames analyzes stereo PCM once per video frame. It returns floor(len x fps
// / sampleRate) frames; frame i describes the audio that ends at sample
// (i+1) x sampleRate / fps. It is a pure function of its input, so repeated
// calls give identical bits. It returns nil for a sample rate below 8 kHz, a
// non-positive fps or unequal channel lengths.
func Frames(left, right []float32, sampleRate, fps int) []Frame {
	if fps <= 0 || len(left) != len(right) {
		return nil
	}
	spec, err := NewSpectrum(sampleRate)
	if err != nil {
		return nil
	}
	corr, _ := NewCorrelation(sampleRate)
	count := int(int64(len(left)) * int64(fps) / int64(sampleRate))
	frames := make([]Frame, count)
	pos := 0
	for i := range frames {
		end := int(int64(i+1) * int64(sampleRate) / int64(fps))
		end = min(end, len(left))
		spec.Process(left[pos:end], right[pos:end])
		corr.Process(left[pos:end], right[pos:end])
		pos = end
		spec.Bands(&frames[i].Bands)
		frames[i].Correlation, frames[i].HasCorrelation = corr.Value()
	}
	return frames
}
