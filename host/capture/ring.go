package capture

import (
	"errors"
	"sync/atomic"
)

var ErrRingFormat = errors.New("capture ring needs positive slots and frames with mono or stereo input")

type slot struct {
	block Block
	pcm   [2][]float32
}

// Ring is single producer/single consumer. Push copies borrowed input; Consume
// lends a slot until its callback returns. Allocate the ring before starting the
// device. A writer may block; the audio producer always drops instead of waiting.
type Ring struct {
	slots            []slot
	frames, channels int
	write, read      atomic.Uint64
	overruns         atomic.Uint64
	lostFrames       atomic.Uint64
	invalid          atomic.Uint64
	pendingGap       uint64 // producer owned
	pendingFlags     Discontinuity
}

func NewRing(slots, frames, channels int) (*Ring, error) {
	if slots <= 0 || frames <= 0 || channels < 1 || channels > 2 {
		return nil, ErrRingFormat
	}
	r := &Ring{slots: make([]slot, slots), frames: frames, channels: channels}
	for i := range r.slots {
		for ch := 0; ch < channels; ch++ {
			r.slots[i].pcm[ch] = make([]float32, frames)
		}
	}
	return r, nil
}

// Push never allocates or blocks, including malformed input and full queues.
// Gaps are attached to the next accepted block; Stats also retains a trailing
// gap when no subsequent block arrives. Samples are never silently discarded.
func (r *Ring) Push(block Block, input [][]float32) bool {
	valid := block.Frames > 0 && block.Frames <= r.frames && len(input) == r.channels && int(block.Layout) == r.channels
	if valid {
		for _, ch := range input {
			if len(ch) < block.Frames {
				valid = false
			}
		}
	}
	if !valid {
		r.invalid.Add(1)
		r.lose(block, InvalidBlock)
		return false
	}
	write := r.write.Load()
	if write-r.read.Load() >= uint64(len(r.slots)) {
		r.overruns.Add(1)
		r.lose(block, QueueOverrun)
		return false
	}
	s := &r.slots[write%uint64(len(r.slots))]
	block.GapFrames += r.pendingGap
	block.Flags |= r.pendingFlags
	s.block = block
	for ch := 0; ch < r.channels; ch++ {
		copy(s.pcm[ch], input[ch][:block.Frames])
	}
	r.pendingGap, r.pendingFlags = 0, 0
	r.write.Store(write + 1)
	return true
}

func (r *Ring) lose(block Block, flag Discontinuity) {
	frames := uint64(max(0, block.Frames))
	r.pendingGap += frames + block.GapFrames
	r.pendingFlags |= block.Flags | flag
	r.lostFrames.Add(frames)
}

// Consume calls write on the consumer goroutine. Input slices must not escape.
func (r *Ring) Consume(write func(Block, [][]float32)) bool {
	read := r.read.Load()
	if read == r.write.Load() {
		return false
	}
	s := &r.slots[read%uint64(len(r.slots))]
	var input [2][]float32
	for ch := 0; ch < r.channels; ch++ {
		input[ch] = s.pcm[ch][:s.block.Frames]
	}
	write(s.block, input[:r.channels])
	r.read.Store(read + 1)
	return true
}

type RingStats struct {
	Overruns, LostFrames, InvalidBlocks uint64
}

func (r *Ring) Stats() RingStats {
	return RingStats{r.overruns.Load(), r.lostFrames.Load(), r.invalid.Load()}
}
