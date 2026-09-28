package liveplay

import (
	"math"
	"sync"
	"sync/atomic"
	"time"

	"m31labs.dev/cicada/kernel/loudness"
)

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

type liveLoudness struct {
	ring       loudnessRing
	reset      atomic.Uint64
	closed     chan struct{}
	closedOnce sync.Once
	done       chan struct{}
	snapshot   atomic.Pointer[LoudnessSnapshot]
	sequence   atomic.Uint64
	sampleRate int
}

func newLiveLoudness(sampleRate int) (*liveLoudness, error) {
	meter, err := loudness.New(sampleRate)
	if err != nil {
		return nil, err
	}
	live := &liveLoudness{
		closed: make(chan struct{}), done: make(chan struct{}), sampleRate: sampleRate,
	}
	live.snapshot.Store(&LoudnessSnapshot{})
	go live.measure(meter)
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

func (m *liveLoudness) close() {
	m.closedOnce.Do(func() { close(m.closed) })
	<-m.done
}

func (m *liveLoudness) measure(meter *loudness.Meter) {
	defer close(m.done)
	lastReset := m.reset.Load()
	lastPublish := time.Time{}
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
			lastReset = generation
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
			}
			m.ring.read.Store(read + 1)
			now := time.Now()
			if lastPublish.IsZero() || now.Sub(lastPublish) >= 100*time.Millisecond {
				m.publish(meter)
				lastPublish = now
			}
			continue
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
