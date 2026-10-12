package liveplay

import (
	"errors"
	"sync/atomic"

	"m31labs.dev/cicada/kernel/engine"
)

// Every admitted engine reserves an entry before it can reach the callback.
// The queue holds the whole registry, so retirement never needs to wait for
// file cleanup or DSP worker joins. Slow cleanup backpressures Offer instead.
const retiredEngineCapacity = 32

var errRetirementBusy = errors.New("audio engine cleanup is busy; retry the score edit")
var errRetirementInvariant = errors.New("audio engine retirement ownership failed")

type retirementEntry struct {
	engine  atomic.Pointer[engine.Engine]
	retired atomic.Bool
}

type engineRetirement struct {
	entries     [retiredEngineCapacity]retirementEntry
	queue       chan *retirementEntry
	done        chan struct{}
	closeEngine func(*engine.Engine)
}

func newEngineRetirement(closeEngine func(*engine.Engine)) *engineRetirement {
	r := &engineRetirement{queue: make(chan *retirementEntry, retiredEngineCapacity), done: make(chan struct{}), closeEngine: closeEngine}
	go r.run()
	return r
}
func (r *engineRetirement) run() {
	defer close(r.done)
	for entry := range r.queue {
		if e := entry.engine.Load(); e != nil {
			r.close(e)
			entry.engine.Store(nil)
		}
	}
}
func (r *engineRetirement) close(e *engine.Engine) {
	if r.closeEngine != nil {
		r.closeEngine(e)
	} else {
		_ = e.Close()
	}
}
func (r *engineRetirement) find(e *engine.Engine) *retirementEntry {
	for i := range r.entries {
		if r.entries[i].engine.Load() == e {
			return &r.entries[i]
		}
	}
	return nil
}

// register/releaseUnused run only on the serialized host control path.
func (r *engineRetirement) register(e *engine.Engine) bool {
	for i := range r.entries {
		entry := &r.entries[i]
		if entry.engine.CompareAndSwap(nil, e) {
			entry.retired.Store(false)
			return true
		}
	}
	return false
}
func (r *engineRetirement) releaseUnused(e *engine.Engine) {
	r.close(e)
	if entry := r.find(e); entry != nil {
		entry.engine.Store(nil)
	}
}

// retire runs on the audio reader. It neither allocates, waits nor joins.
func (r *engineRetirement) retire(e *engine.Engine) bool {
	entry := r.find(e)
	if entry == nil {
		return false
	}
	if !entry.retired.CompareAndSwap(false, true) {
		return true
	}
	e.Retire()
	select {
	case r.queue <- entry:
		return true
	default:
		// Admission bounds make this unreachable. The registry still retains
		// ownership; the player faults and shutdown closes every remaining entry.
		return false
	}
}

// shutdown runs after the reader stops and all offers are excluded.
func (r *engineRetirement) shutdown() {
	close(r.queue)
	<-r.done
	for i := range r.entries {
		if e := r.entries[i].engine.Load(); e != nil {
			r.close(e)
			r.entries[i].engine.Store(nil)
		}
	}
}
func (p *Player) retireEngine(e *engine.Engine) {
	if e != nil && !p.retirement.retire(e) {
		p.fault = errRetirementInvariant
		p.retirementFault.Store(true)
	}
}
