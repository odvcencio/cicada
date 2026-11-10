package liveplay

import (
	"math"
	"sync"
	"sync/atomic"
	"time"

	"m31labs.dev/cicada/kernel/analysis"
	"m31labs.dev/cicada/kernel/loudness"
)

// analysisPublishPeriod is the spectrum and correlation publish interval,
// about 30 Hz. The 100 ms loudness publish is independent of it.
const analysisPublishPeriod = 33 * time.Millisecond

const (
	loudnessBlockCount = 64
	loudnessFrames     = blockFrames
)

type loudnessBlock struct {
	left, right [loudnessFrames]float32
	frames      uint16
}

// loudnessRing is single-producer/single-consumer. The audio reader owns write,
// while the meter goroutine owns read. A slot is published only after its full
// stereo block has been copied.
type loudnessRing struct {
	blocks  [loudnessBlockCount]loudnessBlock
	write   atomic.Uint64
	read    atomic.Uint64
	dropped atomic.Uint64
}

func (r *loudnessRing) push(left, right []float32) bool {
	if len(left) == 0 || len(left) != len(right) || len(left) > loudnessFrames {
		return false
	}
	write := r.write.Load()
	read := r.read.Load()
	if write-read >= loudnessBlockCount {
		r.dropped.Add(1)
		return false
	}
	block := &r.blocks[write%loudnessBlockCount]
	copy(block.left[:], left)
	copy(block.right[:], right)
	block.frames = uint16(len(left))
	r.write.Store(write + 1)
	return true
}

type LoudnessSnapshot struct {
	Sequence       uint64
	MomentaryLUFS  float64
	ShortTermLUFS  float64
	IntegratedLUFS float64
	RangeLU        float64
	TruePeakDBTP   float64
	SamplePeakDBFS float64
	DroppedBlocks  uint64
	HasMomentary   bool
	HasShortTerm   bool
	HasIntegrated  bool
	HasRange       bool
	HasTruePeak    bool
	HasSamplePeak  bool
}

// AnalysisSnapshot is the latest master spectrum and phase correlation. The
// meter goroutine computes both from the same ring that feeds loudness.
type AnalysisSnapshot struct {
	Sequence       uint64
	Correlation    float64 // -1 (out of phase) to +1 (mono); valid when HasCorrelation
	HasCorrelation bool
	Spectrum       [analysis.Bands]float32 // dBFS per band; see analysis.BandHz
	HasSpectrum    bool
	SampleRate     int // Hz; gives the band edges through analysis.BandHz
}

type liveLoudness struct {
	ring       loudnessRing
	reset      atomic.Uint64
	closed     chan struct{}
	closedOnce sync.Once
	done       chan struct{}
	snapshot   atomic.Pointer[LoudnessSnapshot]
	sequence   atomic.Uint64
	sampleRate int

	analysisSnapshot atomic.Pointer[AnalysisSnapshot]
	analysisSequence atomic.Uint64
}

func newLiveLoudness(sampleRate int) (*liveLoudness, error) {
	meter, err := loudness.New(sampleRate)
	if err != nil {
		return nil, err
	}
	spectrum, err := analysis.NewSpectrum(sampleRate)
	if err != nil {
		return nil, err
	}
	correlation, err := analysis.NewCorrelation(sampleRate)
	if err != nil {
		return nil, err
	}
	live := &liveLoudness{
		closed: make(chan struct{}), done: make(chan struct{}), sampleRate: sampleRate,
	}
	live.snapshot.Store(&LoudnessSnapshot{})
	live.analysisSnapshot.Store(&AnalysisSnapshot{})
	go live.measure(meter, spectrum, correlation)
	return live, nil
}

func (m *liveLoudness) push(left, right []float32) {
	m.ring.push(left, right)
}

func (m *liveLoudness) resetMeter() {
	m.reset.Add(1)
}

func (m *liveLoudness) metrics() LoudnessSnapshot {
	if snapshot := m.snapshot.Load(); snapshot != nil {
		return *snapshot
	}
	return LoudnessSnapshot{}
}

func (m *liveLoudness) analysis() AnalysisSnapshot {
	if snapshot := m.analysisSnapshot.Load(); snapshot != nil {
		return *snapshot
	}
	return AnalysisSnapshot{}
}

func (m *liveLoudness) close() {
	m.closedOnce.Do(func() { close(m.closed) })
	<-m.done
}

func (m *liveLoudness) measure(meter *loudness.Meter, spectrum *analysis.Spectrum, correlation *analysis.Correlation) {
	defer close(m.done)
	lastReset := m.reset.Load()
	lastPublish := time.Time{}
	lastAnalysis := time.Time{}
	analysisDirty := false // samples arrived since the last analysis publish
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-m.closed:
			return
		default:
		}
		if generation := m.reset.Load(); generation != lastReset {
			meter.Reset()
			spectrum.Reset()
			correlation.Reset()
			lastReset = generation
			m.analysisSnapshot.Store(&AnalysisSnapshot{Sequence: m.analysisSequence.Add(1), SampleRate: m.sampleRate})
			lastAnalysis, analysisDirty = time.Now(), false
			m.ring.read.Store(m.ring.write.Load())
			now := time.Now()
			if lastPublish.IsZero() || now.Sub(lastPublish) >= 100*time.Millisecond {
				m.snapshot.Store(&LoudnessSnapshot{Sequence: m.sequence.Add(1), DroppedBlocks: m.ring.dropped.Load()})
				lastPublish = now
			}
		}
		read := m.ring.read.Load()
		if read != m.ring.write.Load() {
			block := &m.ring.blocks[read%loudnessBlockCount]
			if block.frames > 0 {
				_ = meter.ProcessBlock(block.left[:block.frames], block.right[:block.frames])
				spectrum.Process(block.left[:block.frames], block.right[:block.frames])
				correlation.Process(block.left[:block.frames], block.right[:block.frames])
				analysisDirty = true
			}
			m.ring.read.Store(read + 1)
			now := time.Now()
			if lastAnalysis.IsZero() || now.Sub(lastAnalysis) >= analysisPublishPeriod {
				m.publishAnalysis(spectrum, correlation)
				lastAnalysis, analysisDirty = now, false
			}
			if lastPublish.IsZero() || now.Sub(lastPublish) >= 100*time.Millisecond {
				m.publish(meter)
				lastPublish = now
			}
			continue
		}
		// The ring is empty: publish the tail of the last burst once, so the
		// snapshot never lags the audio by more than one period.
		if analysisDirty {
			if now := time.Now(); now.Sub(lastAnalysis) >= analysisPublishPeriod {
				m.publishAnalysis(spectrum, correlation)
				lastAnalysis, analysisDirty = now, false
			}
		}
		select {
		case <-m.closed:
			return
		case <-ticker.C:
			if generation := m.reset.Load(); generation != lastReset {
				continue
			}
			now := time.Now()
			if lastPublish.IsZero() || now.Sub(lastPublish) >= 100*time.Millisecond {
				m.publish(meter)
				lastPublish = now
			}
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

// publishAnalysis runs the FFT on the meter goroutine, only when it publishes.
func (m *liveLoudness) publishAnalysis(spectrum *analysis.Spectrum, correlation *analysis.Correlation) {
	snapshot := &AnalysisSnapshot{Sequence: m.analysisSequence.Add(1), SampleRate: m.sampleRate}
	snapshot.Correlation, snapshot.HasCorrelation = correlation.Value()
	if spectrum.Ready() {
		spectrum.Bands(&snapshot.Spectrum)
		snapshot.HasSpectrum = true
	}
	m.analysisSnapshot.Store(snapshot)
}

func (m *liveLoudness) publish(meter *loudness.Meter) {
	result := meter.Metrics()
	snapshot := &LoudnessSnapshot{Sequence: m.sequence.Add(1), DroppedBlocks: m.ring.dropped.Load()}
	snapshot.MomentaryLUFS, snapshot.HasMomentary = gatedLoudness(result.MomentaryLUFS)
	snapshot.ShortTermLUFS, snapshot.HasShortTerm = gatedLoudness(result.ShortTermLUFS)
	snapshot.IntegratedLUFS, snapshot.HasIntegrated = gatedLoudness(result.IntegratedLUFS)
	if snapshot.HasIntegrated && result.Frames >= uint64(m.sampleRate*3+m.sampleRate/10) {
		snapshot.RangeLU, snapshot.HasRange = result.LoudnessRange, true
	}
	if !math.IsInf(result.TruePeakDBTP, 0) && !math.IsNaN(result.TruePeakDBTP) {
		snapshot.TruePeakDBTP, snapshot.HasTruePeak = result.TruePeakDBTP, true
	}
	if !math.IsInf(result.SamplePeakDBFS, 0) && !math.IsNaN(result.SamplePeakDBFS) {
		snapshot.SamplePeakDBFS, snapshot.HasSamplePeak = result.SamplePeakDBFS, true
	}
	m.snapshot.Store(snapshot)
}

func gatedLoudness(value float64) (float64, bool) {
	return value, !math.IsInf(value, 0) && !math.IsNaN(value) && value >= -70
}
