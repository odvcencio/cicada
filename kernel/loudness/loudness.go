// Package loudness measures stereo audio with the ITU-R BS.1770-4 and EBU
// R 128 algorithms. A Meter is single-owner state: it is safe to feed blocks
// on an audio thread and makes no allocation after New.
//
// Integrated loudness and loudness range use fixed 0.01 LU histograms spanning
// -100 through +24 LUFS (12,401 bins each). Gate boundaries and percentile
// results are therefore quantized to 0.01 LU; the histogram power sums retain
// unquantized block energy for the integrated result.
package loudness

import "math"

const (
	maxRate          = 96_000
	histMin          = -100.0
	histMax          = 24.0
	histResolution   = 100.0
	histBins         = 12_401
	truePeakTaps     = 24
	truePeakDelay    = truePeakTaps / 2
	truePeakRingSize = 32
)

// Error is a package error string. It avoids a dependency on the standard
// errors package in this real-time package.
type Error string

func (e Error) Error() string { return string(e) }

const (
	ErrSampleRate   Error = "loudness supports 44100, 48000, and 96000 Hz"
	ErrChannelCount Error = "stereo blocks must have matching channel lengths"
	ErrNonFinite    Error = "loudness input contains a non-finite sample"
	ErrFinished     Error = "loudness meter is finalized; reset it before processing more audio"
)

// Result is a snapshot of the meter. Peak values are linear amplitudes and
// their corresponding dB values use -Inf for silence. Momentary and
// short-term values are the latest valid sliding-window readings; the Max
// fields retain the highest readings since reset.
type Result struct {
	Frames           uint64
	SamplePeak       float64
	SamplePeakDBFS   float64
	TruePeak         float64
	TruePeakDBTP     float64
	MomentaryLUFS    float64
	ShortTermLUFS    float64
	IntegratedLUFS   float64
	LoudnessRange    float64
	MaxMomentaryLUFS float64
	MaxShortTermLUFS float64
	RMS              float64
	RMSDBFS          float64
	LRAStable        bool
}

type biquad struct {
	b0, b1, b2 float64
	a1, a2     float64
	x1, x2     float64
	y1, y2     float64
}

func (f *biquad) process(x float64) float64 {
	y := f.b0*x + f.b1*f.x1 + f.b2*f.x2 - f.a1*f.y1 - f.a2*f.y2
	f.x2, f.x1 = f.x1, x
	f.y2, f.y1 = f.y1, y
	return y
}

// Meter retains fixed history for the longest three-second window and bounded
// histograms for whole-program statistics. The memory use is independent of
// programme duration and all scratch is owned by New.
type Meter struct {
	sampleRate        int
	momentFrames      int
	shortFrames       int
	hopFrames         int
	window            []float64
	windowWrite       int
	frames            uint64
	momentPower       float64
	shortPower        float64
	samplePowerSum    float64
	samplePeak        float64
	maxMomentaryPower float64
	maxShortTermPower float64
	integrated        float64
	rangeLU           float64
	filterShelfL      biquad
	filterShelfR      biquad
	filterRLBL        biquad
	filterRLBR        biquad
	integratedCount   [histBins]uint64
	integratedPower   [histBins]float64
	rangeCount        [histBins]uint64
	rangePower        [histBins]float64
	integratedN       uint64
	rangeN            uint64
	truePeakCoeff     [8][truePeakTaps]float64
	truePeakRingL     [truePeakRingSize]float64
	truePeakRingR     [truePeakRingSize]float64
	truePeakFrames    uint64
	truePeakPhases    int
	truePeakValue     float64
	faulted           bool
	finished          bool
}

// New constructs a stereo meter for 44.1, 48, or 96 kHz audio. True-peak
// interpolation uses 5x, 4x, or 2x oversampling respectively, reaching at
// least 192 kHz. Call Reset to reuse the meter without allocating.
func New(sampleRate int) (*Meter, error) {
	if sampleRate != 44_100 && sampleRate != 48_000 && sampleRate != 96_000 {
		return nil, ErrSampleRate
	}
	shelfB, shelfA := resampleBiquad(
		[3]float64{1.53512485958697, -2.69169618940638, 1.19839281085285},
		[3]float64{1, -1.69065929318241, 0.73248077421585},
		48_000, sampleRate,
	)
	rlbB, rlbA := resampleBiquad(
		[3]float64{1, -2, 1},
		[3]float64{1, -1.99004745483398, 0.99007225036621},
		48_000, sampleRate,
	)
	phases := 4
	if sampleRate == 44_100 {
		phases = 5
	} else if sampleRate == 96_000 {
		phases = 2
	}
	m := &Meter{
		sampleRate:     sampleRate,
		momentFrames:   int(math.Round(float64(sampleRate) * 0.4)),
		shortFrames:    sampleRate * 3,
		hopFrames:      sampleRate / 10,
		window:         make([]float64, sampleRate*3),
		filterShelfL:   newBiquad(shelfB, shelfA),
		filterShelfR:   newBiquad(shelfB, shelfA),
		filterRLBL:     newBiquad(rlbB, rlbA),
		filterRLBR:     newBiquad(rlbB, rlbA),
		truePeakPhases: phases,
	}
	m.resetReadings()
	m.designTruePeakFilter()
	return m, nil
}

func newBiquad(b, a [3]float64) biquad {
	return biquad{b0: b[0], b1: b[1], b2: b[2], a1: a[1], a2: a[2]}
}

// resampleBiquad maps a published 48 kHz digital filter back through the
// bilinear transform and applies the same analog poles and zeros at rate.
func resampleBiquad(b, a [3]float64, sourceRate, rate int) ([3]float64, [3]float64) {
	src := float64(sourceRate)
	n0 := b[0] + b[1] + b[2]
	n1 := (b[0] - b[2]) / src
	n2 := (b[0] - b[1] + b[2]) / (4 * src * src)
	d0 := a[0] + a[1] + a[2]
	d1 := (a[0] - a[2]) / src
	d2 := (a[0] - a[1] + a[2]) / (4 * src * src)
	f := float64(rate)
	bn := [3]float64{
		n2*4*f*f + n1*2*f + n0,
		-n2*8*f*f + 2*n0,
		n2*4*f*f - n1*2*f + n0,
	}
	dn := [3]float64{
		d2*4*f*f + d1*2*f + d0,
		-d2*8*f*f + 2*d0,
		d2*4*f*f - d1*2*f + d0,
	}
	return [3]float64{bn[0] / dn[0], bn[1] / dn[0], bn[2] / dn[0]},
		[3]float64{1, dn[1] / dn[0], dn[2] / dn[0]}
}

func (m *Meter) designTruePeakFilter() {
	for phase := 0; phase < m.truePeakPhases; phase++ {
		fraction := float64(phase) / float64(m.truePeakPhases)
		sum := 0.0
		for tap := 0; tap < truePeakTaps; tap++ {
			offset := float64(tap - (truePeakTaps/2 - 1))
			x := offset - fraction
			sinc := 1.0
			if x != 0 {
				angle := math.Pi * x
				sinc = math.Sin(angle) / angle
			}
			position := float64(tap) / float64(truePeakTaps-1)
			window := 0.42 - 0.5*math.Cos(2*math.Pi*position) + 0.08*math.Cos(4*math.Pi*position)
			coefficient := sinc * window
			m.truePeakCoeff[phase][tap] = coefficient
			sum += coefficient
		}
		for tap := 0; tap < truePeakTaps; tap++ {
			m.truePeakCoeff[phase][tap] /= sum
		}
	}
}

// ProcessBlock feeds matching left and right float32 blocks. The meter keeps
// filter state across calls, so callers may use any block length. It returns
// a package Error for mismatched channels, non-finite samples, or processing
// after Finish.
func (m *Meter) ProcessBlock(left, right []float32) error {
	if len(left) != len(right) {
		return ErrChannelCount
	}
	for i := range left {
		if !m.ProcessSample(left[i], right[i]) {
			if m.finished {
				return ErrFinished
			}
			return ErrNonFinite
		}
	}
	return nil
}

// ProcessSample feeds one stereo frame without allocating. It returns false
// after a non-finite sample or after Finish; Reset clears either state.
func (m *Meter) ProcessSample(left, right float32) bool {
	if m.finished || m.faulted {
		return false
	}
	l, r := float64(left), float64(right)
	if math.IsNaN(l) || math.IsNaN(r) || math.IsInf(l, 0) || math.IsInf(r, 0) {
		m.faulted = true
		return false
	}
	m.processTruePeak(l, r)
	absL, absR := math.Abs(l), math.Abs(r)
	if absL > m.samplePeak {
		m.samplePeak = absL
	}
	if absR > m.samplePeak {
		m.samplePeak = absR
	}
	m.samplePowerSum += l*l + r*r
	yL := m.filterRLBL.process(m.filterShelfL.process(l))
	yR := m.filterRLBR.process(m.filterShelfR.process(r))
	energy := yL*yL + yR*yR
	index := m.windowWrite
	oldMoment, oldShort := 0.0, 0.0
	if m.frames >= uint64(m.momentFrames) {
		oldMoment = m.window[(index-m.momentFrames+len(m.window))%len(m.window)]
	}
	if m.frames >= uint64(m.shortFrames) {
		oldShort = m.window[(index-m.shortFrames+len(m.window))%len(m.window)]
	}
	m.window[index] = energy
	m.windowWrite++
	if m.windowWrite == len(m.window) {
		m.windowWrite = 0
	}
	m.momentPower += energy - oldMoment
	m.shortPower += energy - oldShort
	m.frames++
	if m.frames >= uint64(m.momentFrames) {
		power := m.momentPower / float64(m.momentFrames)
		if power > m.maxMomentaryPower {
			m.maxMomentaryPower = power
		}
	}
	if m.frames >= uint64(m.shortFrames) {
		power := m.shortPower / float64(m.shortFrames)
		if power > m.maxShortTermPower {
			m.maxShortTermPower = power
		}
	}
	if m.frames%uint64(m.hopFrames) == 0 {
		m.recordIntegrated()
		if m.frames >= uint64(m.shortFrames) {
			m.recordRange()
		}
	}
	return true
}

// Finish freezes the meter after ignoring incomplete true-peak FIR edge
// windows. It does not extend loudness or RMS integration windows.
func (m *Meter) Finish() error {
	if m.faulted {
		return ErrNonFinite
	}
	if m.finished {
		return nil
	}
	m.finished = true
	return nil
}

// Reset reuses the meter and clears all measurements without allocating.
func (m *Meter) Reset() {
	clear(m.window)
	clear(m.integratedCount[:])
	clear(m.integratedPower[:])
	clear(m.rangeCount[:])
	clear(m.rangePower[:])
	m.filterShelfL.x1, m.filterShelfL.x2, m.filterShelfL.y1, m.filterShelfL.y2 = 0, 0, 0, 0
	m.filterShelfR.x1, m.filterShelfR.x2, m.filterShelfR.y1, m.filterShelfR.y2 = 0, 0, 0, 0
	m.filterRLBL.x1, m.filterRLBL.x2, m.filterRLBL.y1, m.filterRLBL.y2 = 0, 0, 0, 0
	m.filterRLBR.x1, m.filterRLBR.x2, m.filterRLBR.y1, m.filterRLBR.y2 = 0, 0, 0, 0
	m.resetReadings()
}

func (m *Meter) resetReadings() {
	m.windowWrite = 0
	m.frames = 0
	m.momentPower, m.shortPower, m.samplePowerSum, m.samplePeak = 0, 0, 0, 0
	m.maxMomentaryPower, m.maxShortTermPower = 0, 0
	m.integrated, m.rangeLU = math.Inf(-1), 0
	m.integratedN, m.rangeN = 0, 0
	m.truePeakRingL, m.truePeakRingR = [truePeakRingSize]float64{}, [truePeakRingSize]float64{}
	m.truePeakFrames, m.truePeakValue = 0, 0
	m.faulted, m.finished = false, false
}

// Metrics returns the latest readings. It is safe to call at any time on the
// owning thread and does not allocate or scan the programme histograms.
func (m *Meter) Metrics() Result {
	momentary, shortTerm := math.Inf(-1), math.Inf(-1)
	maxMomentary, maxShortTerm := math.Inf(-1), math.Inf(-1)
	if m.frames >= uint64(m.momentFrames) {
		momentary = loudnessFromPower(m.momentPower / float64(m.momentFrames))
		maxMomentary = loudnessFromPower(m.maxMomentaryPower)
	}
	if m.frames >= uint64(m.shortFrames) {
		shortTerm = loudnessFromPower(m.shortPower / float64(m.shortFrames))
		maxShortTerm = loudnessFromPower(m.maxShortTermPower)
	}
	r := Result{
		Frames:           m.frames,
		SamplePeak:       m.samplePeak,
		SamplePeakDBFS:   amplitudeDB(m.samplePeak),
		TruePeak:         math.Max(m.truePeakValue, m.samplePeak),
		MomentaryLUFS:    momentary,
		ShortTermLUFS:    shortTerm,
		IntegratedLUFS:   m.integrated,
		LoudnessRange:    m.rangeLU,
		MaxMomentaryLUFS: maxMomentary,
		MaxShortTermLUFS: maxShortTerm,
		LRAStable:        m.frames >= uint64(m.sampleRate*60),
	}
	r.TruePeakDBTP = amplitudeDB(r.TruePeak)
	if m.frames > 0 {
		r.RMS = math.Sqrt(m.samplePowerSum / (2 * float64(m.frames)))
	}
	r.RMSDBFS = amplitudeDB(r.RMS)
	return r
}

func (m *Meter) processTruePeak(left, right float64) {
	n := m.truePeakFrames
	index := int(n % truePeakRingSize)
	m.truePeakRingL[index], m.truePeakRingR[index] = left, right
	if n >= truePeakTaps-1 {
		base := int64(n) - truePeakDelay
		for phase := 0; phase < m.truePeakPhases; phase++ {
			peakL, peakR := 0.0, 0.0
			if phase == 0 {
				center := int(base % truePeakRingSize)
				peakL, peakR = m.truePeakRingL[center], m.truePeakRingR[center]
			} else {
				for tap := 0; tap < truePeakTaps; tap++ {
					sampleIndex := base + int64(tap-(truePeakTaps/2-1))
					if sampleIndex < 0 {
						continue
					}
					ringIndex := int(sampleIndex % truePeakRingSize)
					coefficient := m.truePeakCoeff[phase][tap]
					peakL += m.truePeakRingL[ringIndex] * coefficient
					peakR += m.truePeakRingR[ringIndex] * coefficient
				}
			}
			peakL, peakR = math.Abs(peakL), math.Abs(peakR)
			if peakL > m.truePeakValue {
				m.truePeakValue = peakL
			}
			if peakR > m.truePeakValue {
				m.truePeakValue = peakR
			}
		}
	}
	m.truePeakFrames++
}

func (m *Meter) recordIntegrated() {
	level := loudnessFromPower(m.momentPower / float64(m.momentFrames))
	if level < -70 {
		return
	}
	index := histogramIndex(level)
	m.integratedCount[index]++
	m.integratedPower[index] += m.momentPower / float64(m.momentFrames)
	m.integratedN++
	if m.integratedN == 0 {
		m.integrated = math.Inf(-1)
		return
	}
	absPower := 0.0
	for bin := firstBinAtLeast(-70); bin < histBins; bin++ {
		absPower += m.integratedPower[bin]
	}
	absIntegrated := loudnessFromPower(absPower / float64(m.integratedN))
	threshold := math.Max(-70, absIntegrated-10)
	first := firstBinAtLeast(threshold)
	count, power := uint64(0), 0.0
	for bin := first; bin < histBins; bin++ {
		count += m.integratedCount[bin]
		power += m.integratedPower[bin]
	}
	if count == 0 {
		m.integrated = math.Inf(-1)
		return
	}
	m.integrated = loudnessFromPower(power / float64(count))
}

func (m *Meter) recordRange() {
	level := loudnessFromPower(m.shortPower / float64(m.shortFrames))
	if level < -70 {
		return
	}
	index := histogramIndex(level)
	m.rangeCount[index]++
	m.rangePower[index] += m.shortPower / float64(m.shortFrames)
	m.rangeN++
	if m.rangeN == 0 {
		m.rangeLU = 0
		return
	}
	absPower := 0.0
	for bin := firstBinAtLeast(-70); bin < histBins; bin++ {
		absPower += m.rangePower[bin]
	}
	absIntegrated := loudnessFromPower(absPower / float64(m.rangeN))
	threshold := math.Max(-70, absIntegrated-20)
	first := firstBinAtLeast(threshold)
	count := uint64(0)
	for bin := first; bin < histBins; bin++ {
		count += m.rangeCount[bin]
	}
	if count == 0 {
		m.rangeLU = 0
		return
	}
	lowRank := uint64(math.Round(float64(count-1) * 0.10))
	highRank := uint64(math.Round(float64(count-1) * 0.95))
	low, high := math.Inf(1), math.Inf(-1)
	var seen uint64
	for bin := first; bin < histBins; bin++ {
		items := m.rangeCount[bin]
		if items == 0 {
			continue
		}
		center := histMin + (float64(bin)+0.5)/histResolution
		if low == math.Inf(1) && seen+items > lowRank {
			low = center
		}
		if seen+items > highRank {
			high = center
			break
		}
		seen += items
	}
	if low != math.Inf(1) && high != math.Inf(-1) {
		m.rangeLU = high - low
	}
}

func histogramIndex(level float64) int {
	index := int((level - histMin) * histResolution)
	if index < 0 {
		return 0
	}
	if index >= histBins {
		return histBins - 1
	}
	return index
}

func firstBinAtLeast(level float64) int {
	index := int(math.Ceil((level-histMin)*histResolution - 0.5))
	if index < 0 {
		return 0
	}
	if index >= histBins {
		return histBins
	}
	return index
}

func loudnessFromPower(power float64) float64 {
	if power <= 0 {
		return math.Inf(-1)
	}
	return -0.691 + 10*math.Log10(power)
}

func amplitudeDB(amplitude float64) float64 {
	if amplitude <= 0 {
		return math.Inf(-1)
	}
	return 20 * math.Log10(amplitude)
}
