package stream

import "sync/atomic"

// Reader renders unity-rate clips in either direction. SeekFrame, Render, Stop,
// Position and Stats belong to one audio owner. WindowReady is worker-safe.
// Future SRC callers can use ReadFrame with the same guarded page ownership.
type Reader struct {
	cache                             *Cache
	asset                             Asset
	intent                            intent
	page                              *page
	position                          int64
	direction, fadeFrames             int
	missing, ended, underrun          bool
	fade, recovery                    int
	lastL, lastR, heldL, heldR        float32
	misses, missingFrames, recoveries atomic.Uint64
}

type ReaderStats struct{ Misses, MissingFrames, Recoveries uint64 }

func (r *Reader) Stats() ReaderStats {
	return ReaderStats{r.misses.Load(), r.missingFrames.Load(), r.recoveries.Load()}
}
func (r *Reader) Position() int64 { return r.position }

func (r *Reader) release() {
	if r.page != nil {
		r.page.state.Add(^uint32(pin - 1))
		r.page = nil
	}
}

func (r *Reader) announce(frame int64) {
	current := frame / PageFrames
	low, high := current-1, current+int64(r.cache.config.AheadPages)
	if r.direction < 0 {
		low, high = current-int64(r.cache.config.AheadPages), current+1
	}
	r.intent.sequence.Add(1)
	r.intent.asset.Store(r.asset.ID)
	r.intent.current.Store(current)
	r.intent.low.Store(max(int64(0), low))
	r.intent.high.Store(min((r.asset.Frames-1)/PageFrames, high))
	r.intent.direction.Store(int32(r.direction))
	r.intent.sequence.Add(1)
}

// SeekFrame replaces the mailbox in constant space, releases the old page, and never
// waits for cancellation or storage. End is allowed for an empty forward tail.
func (r *Reader) SeekFrame(frame int64, direction int) error {
	if frame < 0 || frame > r.asset.Frames || (direction != 1 && direction != -1) {
		return Error("invalid streamed seek")
	}
	r.release()
	r.position, r.direction, r.ended = frame, direction, false
	if frame == r.asset.Frames {
		r.Stop()
		r.position = frame
		return nil
	}
	r.announce(frame)
	return nil
}

// Stop retires demand and releases the pin; it does not wait for the worker.
func (r *Reader) Stop() {
	r.release()
	r.intent.sequence.Add(1)
	r.intent.low.Store(1)
	r.intent.high.Store(0)
	r.intent.sequence.Add(1)
	r.ended = true
	r.underrun = false
}

// WindowReady checks publication without reading PCM. Offline hosts may wait
// here outside rendering, using a deadline and worker errors.
func (r *Reader) WindowReady() bool {
	w, ok := r.intent.snapshot()
	if !ok {
		return false
	}
	for index := w.low; index <= w.high; index++ {
		p := r.cache.acquire(w.asset, index)
		if p == nil {
			return false
		}
		p.state.Add(^uint32(pin - 1))
	}
	return true
}

// Needs is safe for host error reporting and cancellation checks.
func (r *Reader) Needs(asset uint32, index int64) bool {
	w, ok := r.intent.snapshot()
	return ok && w.asset == asset && index >= w.low && index <= w.high
}

// ReadFrame returns false for missing PCM. It uses the currently pinned guard
// for neighboring frames; a different page is pinned before returning samples.
// Samples are copied by value, so callers cannot retain a slice past eviction.
func (r *Reader) ReadFrame(frame int64) (float32, float32, bool) {
	if frame < 0 || frame >= r.asset.Frames {
		return 0, 0, true
	}
	if r.page != nil {
		offset := frame - r.page.index*PageFrames + GuardFrames
		if offset >= 0 && offset < int64(len(r.page.left)) {
			return r.page.left[offset], r.page.right[offset], true
		}
	}
	r.release()
	r.page = r.cache.acquire(r.asset.ID, frame/PageFrames)
	if r.page == nil {
		return 0, 0, false
	}
	offset := frame - r.page.index*PageFrames + GuardFrames
	return r.page.left[offset], r.page.right[offset], true
}

// Render never waits, locks, allocates or opens storage. Missing source frames
// fade the held output over 2 ms; recovery fades into the current transport
// position over 2 ms. The playhead advances through every missing frame.
func (r *Reader) Render(left, right []float32) {
	if len(left) != len(right) {
		panic("stream output channel lengths differ")
	}
	for i := range left {
		inRange := !r.ended && r.position >= 0 && r.position < r.asset.Frames
		var l, rr float32
		ok := false
		if inRange {
			if w := r.position / PageFrames; r.intent.current.Load() != w {
				r.announce(r.position)
			}
			l, rr, ok = r.ReadFrame(r.position)
		} else if !r.ended {
			r.Stop()
		}
		if !ok {
			if inRange && !r.underrun {
				r.misses.Add(1)
				r.underrun = true
			}
			if !r.missing {
				r.missing, r.fade, r.recovery = true, r.fadeFrames, 0
				r.heldL, r.heldR = r.lastL, r.lastR
			}
			if inRange {
				r.missingFrames.Add(1)
			}
			if r.fade > 0 {
				gain := float32(r.fade-1) / float32(r.fadeFrames)
				l, rr = r.heldL*gain, r.heldR*gain
				r.fade--
			}
		} else {
			if r.underrun {
				r.recoveries.Add(1)
				r.underrun = false
			}
			if r.missing {
				r.missing, r.recovery = false, r.fadeFrames
				r.heldL, r.heldR = r.lastL, r.lastR
			}
			if r.recovery > 0 {
				gain := float32(r.fadeFrames-r.recovery+1) / float32(r.fadeFrames)
				l, rr = r.heldL*(1-gain)+l*gain, r.heldR*(1-gain)+rr*gain
				r.recovery--
			}
		}
		left[i], right[i], r.lastL, r.lastR = l, rr, l, rr
		if inRange {
			r.position += int64(r.direction)
		}
	}
}
