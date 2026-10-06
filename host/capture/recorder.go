package capture

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

type RecordedBlock struct {
	Timing    Block     `json:"timing"`
	Placement Placement `json:"placement"`
	RawFrame  uint64    `json:"rawFrame"`
}

// Writer runs on a separate goroutine. Save raw PCM and its RecordedBlock before
// returning; borrowed slices expire on return. The take journal handles
// persistence and recovery.
type Writer func(RecordedBlock, [][]float32) error

type recordPlan struct {
	countIn     CountIn
	calibration Calibration
}

type writerError struct{ err error }

type Recorder struct {
	ring       *Ring
	writer     Writer
	plan       atomic.Pointer[recordPlan]
	ended      atomic.Bool
	closed     atomic.Bool
	inFlight   atomic.Int64
	stop       chan struct{}
	done       chan struct{}
	once       sync.Once
	fault      atomic.Pointer[writerError]
	written    atomic.Uint64
	incomplete atomic.Bool
	remaining  atomic.Int64
	first      atomic.Pointer[RecordedBlock]
	trailing   atomic.Pointer[RecordedBlock]
	current    *recordPlan // audio owner only
	cursor     int64       // audio owner only
}

func NewRecorder(slots, maxFrames, channels int, writer Writer) (*Recorder, error) {
	if writer == nil {
		return nil, errors.New("capture writer is required")
	}
	ring, err := NewRing(slots, maxFrames, channels)
	if err != nil {
		return nil, err
	}
	r := &Recorder{ring: ring, writer: writer, stop: make(chan struct{}), done: make(chan struct{})}
	go r.run()
	return r, nil
}

func (r *Recorder) FormatFits(frames, channels int) bool {
	return r != nil && !r.closed.Load() && frames <= r.ring.frames && channels == r.ring.channels
}

// Begin starts one take on the next capture block. A new take needs a new
// Recorder so trailing gaps and writer failures remain attached to the old take.
func (r *Recorder) Begin(countIn CountIn, calibration Calibration) error {
	if r.closed.Load() || r.ended.Load() || !r.plan.CompareAndSwap(nil, &recordPlan{countIn, calibration}) {
		return errors.New("capture take has already started or closed")
	}
	return nil
}

// Capture is called before rendering. Count-in PCM is retained as raw preroll,
// with negative engine placement; the engine itself does not advance until the
// exact end of the count-in, even when it falls inside a device period.
func (r *Recorder) Capture(b Block, input [][]float32) {
	r.inFlight.Add(1)
	defer r.inFlight.Add(-1)
	if r.closed.Load() || r.ended.Load() {
		return
	}
	if r.current == nil {
		r.current = r.plan.Load()
	}
	if r.current == nil {
		return
	}
	left := max(int64(0), r.current.countIn.Frames-r.cursor)
	r.remaining.Store(left)
	b.EngineFrame -= left
	b.Calibration = r.current.calibration
	r.ring.Push(b, input)
}

// LeadIn writes count-in output and returns how many frames precede rendering.
// Capture and LeadIn must run sequentially on the same audio owner.
func (r *Recorder) LeadIn(output [][]float32) int {
	if r.current == nil || r.ended.Load() || len(output) == 0 {
		return 0
	}
	frames := int(min(int64(len(output[0])), max(int64(0), r.current.countIn.Frames-r.cursor)))
	r.current.countIn.click(output, r.cursor, frames)
	r.cursor += int64(frames)
	r.remaining.Store(max(int64(0), r.current.countIn.Frames-r.cursor))
	return frames
}

func (r *Recorder) End() {
	if r.plan.Load() != nil {
		r.ended.Store(true)
	}
}

func (r *Recorder) MarkIncomplete() { r.incomplete.Store(true) }

// Close stops publication, waits for an in-flight producer, and drains accepted
// blocks. Only the control/writer side may wait; the device callback never does.
func (r *Recorder) Close() error {
	r.once.Do(func() {
		r.closed.Store(true)
		for r.inFlight.Load() != 0 {
			time.Sleep(time.Millisecond)
		}
		close(r.stop)
	})
	<-r.done
	if fault := r.fault.Load(); fault != nil {
		return fault.err
	}
	return nil
}

type Snapshot struct {
	Recording       bool           `json:"recording"`
	CountInFrames   int64          `json:"countInFrames"`
	RemainingFrames int64          `json:"countInRemainingFrames"`
	WrittenFrames   uint64         `json:"writtenFrames"`
	Incomplete      bool           `json:"incomplete"`
	Ring            RingStats      `json:"ring"`
	FirstBlock      *RecordedBlock `json:"firstBlock,omitempty"`
	// TrailingGap is available after Close drains the recorder. It has no PCM;
	// RawFrame marks the end of the lost interval in the raw frame domain.
	TrailingGap *RecordedBlock `json:"trailingGap,omitempty"`
	Error       string         `json:"error,omitempty"`
}

func (r *Recorder) Snapshot() Snapshot {
	s := Snapshot{Recording: r.plan.Load() != nil && !r.ended.Load() && !r.closed.Load(), RemainingFrames: r.remaining.Load(), WrittenFrames: r.written.Load(), Ring: r.ring.Stats(), FirstBlock: r.first.Load(), TrailingGap: r.trailing.Load()}
	if p := r.plan.Load(); p != nil {
		s.CountInFrames = p.countIn.Frames
	}
	s.Incomplete = r.incomplete.Load() || s.Ring.Overruns != 0 || s.Ring.InvalidBlocks != 0
	if fault := r.fault.Load(); fault != nil {
		s.Error = fault.err.Error()
	}
	return s
}

func (r *Recorder) run() {
	defer close(r.done)
	var rawFrame uint64
	var previous Block
	write := func(b Block, pcm [][]float32) {
		if previous.DeviceEpoch != 0 {
			if b.DeviceEpoch != previous.DeviceEpoch || b.EngineEpoch != previous.EngineEpoch || b.DeviceFrame < previous.DeviceFrame+uint64(previous.Frames) || b.DeviceDropouts > previous.DeviceDropouts {
				b.Flags |= DeviceDiscontinuity
			} else if gap := b.DeviceFrame - previous.DeviceFrame - uint64(previous.Frames); gap != 0 {
				b.GapFrames = max(b.GapFrames, gap)
				b.Flags |= DeviceDiscontinuity
			}
		}
		previous = b
		rawFrame += b.GapFrames
		record := RecordedBlock{Timing: b, Placement: Place(b), RawFrame: rawFrame}
		rawFrame += uint64(b.Frames)
		if b.Flags != 0 || b.GapFrames != 0 {
			r.incomplete.Store(true)
		}
		if r.first.Load() == nil {
			first := record
			r.first.Store(&first)
		}
		if err := r.writer(record, pcm); err != nil {
			r.incomplete.Store(true)
			r.fault.CompareAndSwap(nil, &writerError{err})
			return
		}
		r.written.Add(uint64(b.Frames))
	}
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		for r.ring.Consume(write) {
		}
		select {
		case <-r.stop:
			for r.ring.Consume(write) {
			}
			// Close has stopped the producer, so its pending gap is now stable.
			if gap := r.ring.pendingGap; gap != 0 {
				b := r.ring.pendingBlock
				b.Frames, b.GapFrames, b.Flags = 0, gap, r.ring.pendingFlags
				r.trailing.Store(&RecordedBlock{Timing: b, Placement: Place(b), RawFrame: rawFrame + gap})
			}
			return
		case <-ticker.C:
		}
	}
}
