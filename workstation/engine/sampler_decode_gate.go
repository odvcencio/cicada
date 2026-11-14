package main

import "sync"

// Only one browser decode may be unresolved across sampler mounts. Browsers
// cannot cancel decodeAudioData, so a never-settling promise must not cause
// subsequent refreshes or remounts to accumulate callbacks indefinitely.
type samplerDecodeGate struct {
	mu   sync.Mutex
	busy bool
}

func (g *samplerDecodeGate) acquire() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.busy {
		return false
	}
	g.busy = true
	return true
}
func (g *samplerDecodeGate) release() { g.mu.Lock(); g.busy = false; g.mu.Unlock() }
