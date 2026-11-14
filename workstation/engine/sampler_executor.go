package main

import "sync"

// samplerCommand contains only copied event data. Browser events and DOM nodes
// must not be retained after their callback returns.
type samplerCommand struct {
	kind, owner, text string
	pad, number       int
	value             float64
	flag              bool
	dirties           bool
	coalesce          bool
	generation        uint64
	epoch             uint64
}

// samplerExecutor serializes sampler state. Overflow revokes all outstanding
// commands immediately and refuses new work until the release callback finishes.
// The release callback also runs exactly once after close, even when the queue is
// full. Callbacks must check valid before every externally visible effect which
// can follow a yielding operation.
type samplerExecutor struct {
	mu              sync.Mutex
	wake            chan struct{}
	queue           []samplerCommand
	limit           int
	generation      uint64
	pendingDirty    int
	dirty           bool
	revoked, closed bool
	run             func(samplerCommand, func() bool)
	release         func() bool
	ready           func()
	after           func()
}

func newSamplerExecutor(limit int, run func(samplerCommand, func() bool), release func() bool) *samplerExecutor {
	if limit < 1 {
		panic("sampler executor requires a positive queue limit")
	}
	x := &samplerExecutor{limit: limit, wake: make(chan struct{}, 1), run: run, release: release}
	go x.loop()
	return x
}

func (x *samplerExecutor) signal() {
	select {
	case x.wake <- struct{}{}:
	default:
	}
}

// submit never waits for the executor or browser effects.
func (x *samplerExecutor) submit(c samplerCommand) bool {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.closed || x.revoked {
		return false
	}
	// Only adjacent moves from the same gesture can be replaced. A boundary
	// command between them must retain its position in the event stream.
	if c.kind == "wave-move" && len(x.queue) > 0 {
		last := &x.queue[len(x.queue)-1]
		if last.kind == c.kind && last.number == c.number && last.dirties == c.dirties {
			c.generation = last.generation
			*last = c
			return true
		}
	}
	if c.coalesce {
		for _, queued := range x.queue {
			if queued.kind == c.kind && queued.dirties == c.dirties {
				return true
			}
		}
	}
	if len(x.queue) >= x.limit {
		x.revokeLocked()
		return false
	}
	c.generation = x.generation
	if c.dirties {
		x.pendingDirty++
	}
	x.queue = append(x.queue, c)
	x.signal()
	return true
}

func (x *samplerExecutor) revokeLocked() {
	if x.revoked || x.closed {
		return
	}
	x.generation++
	x.revoked = true
	for _, queued := range x.queue {
		if queued.dirties {
			x.pendingDirty--
		}
	}
	x.queue = nil
	x.signal()
}

func (x *samplerExecutor) revoke() {
	x.mu.Lock()
	x.revokeLocked()
	x.mu.Unlock()
}

// dirtySnapshot is safe in synchronous browser callbacks: pending changes and
// fail-closed cleanup conservatively hold refresh until their outcome is known.
func (x *samplerExecutor) dirtySnapshot() bool {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.dirty || x.pendingDirty != 0 || x.revoked
}

func (x *samplerExecutor) pendingSnapshot() bool {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.pendingDirty != 0 || x.revoked
}

// publishDirty is called by the serialized state owner before a command ends.
func (x *samplerExecutor) publishDirty(dirty bool) {
	x.mu.Lock()
	x.dirty = dirty
	x.mu.Unlock()
}

func (x *samplerExecutor) close() {
	x.mu.Lock()
	if !x.closed {
		x.closed = true
		x.revoked = true
		x.generation++
		for _, queued := range x.queue {
			if queued.dirties {
				x.pendingDirty--
			}
		}
		x.queue = nil
		x.signal()
	}
	x.mu.Unlock()
}

func (x *samplerExecutor) loop() {
	for range x.wake {
		for {
			x.mu.Lock()
			if x.revoked {
				terminal := x.closed
				x.mu.Unlock()
				dirty := x.release()
				x.mu.Lock()
				x.dirty = dirty
				if terminal {
					x.mu.Unlock()
					return
				}
				if x.closed {
					x.mu.Unlock()
					continue
				}
				x.revoked = false
				ready := x.ready
				x.mu.Unlock()
				if ready != nil {
					ready()
				}
				continue
			}
			if len(x.queue) == 0 {
				x.mu.Unlock()
				break
			}
			c := x.queue[0]
			x.queue[0] = samplerCommand{}
			x.queue = x.queue[1:]
			x.mu.Unlock()
			valid := func() bool {
				x.mu.Lock()
				defer x.mu.Unlock()
				return !x.closed && !x.revoked && x.generation == c.generation
			}
			if valid() {
				func() {
					defer func() {
						if recover() != nil {
							x.revoke()
						}
					}()
					x.run(c, valid)
				}()
			}
			x.mu.Lock()
			if c.dirties {
				x.pendingDirty--
			}
			after := x.after
			x.mu.Unlock()
			if after != nil {
				after()
			}
		}
	}
}
