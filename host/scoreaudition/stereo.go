package scoreaudition

import "math"

// StereoReport summarizes a bar of the rendered audition master. Correlation
// is absent when either channel has no energy. MonoVsStereoDB compares the
// RMS of (L+R)/2 to the RMS across both channels, floored at -120 dB.
type StereoReport struct {
	ChannelCorrelation *float64 `json:"channel_correlation"`
	MonoVsStereoDB     float64  `json:"mono_vs_stereo_db"`
}

func measureStereo(s stereo) *StereoReport {
	var l2, r2, lr float64
	for i := range s.L {
		l, r := float64(s.L[i]), float64(s.R[i])
		l2 += l * l
		r2 += r * r
		lr += l * r
	}
	if l2+r2 == 0 {
		return nil
	}
	result := &StereoReport{}
	if l2 > 0 && r2 > 0 {
		corr := math.Max(-1, math.Min(1, lr/math.Sqrt(l2*r2)))
		result.ChannelCorrelation = &corr
	}
	monoRatio := math.Max(0, (l2+r2+2*lr)/(2*(l2+r2)))
	result.MonoVsStereoDB = 10 * math.Log10(math.Max(1e-12, monoRatio))
	return result
}
