package main

import (
	"context"
	"sync"
)

// sharedLoop runs one background loop for as many panels as need it. The first
// acquire starts the loop; the last release cancels its context. Capture state
// is one projection for the whole page (the panel application mounts it from
// the workspace engine, the /studio shell from the Takes engine), so the loop
// must exist exactly once and stop when no panel is left.
type sharedLoop struct {
	mu     sync.Mutex
	refs   int
	cancel context.CancelFunc
	start  func(ctx context.Context)
}

func (l *sharedLoop) acquire() (release func()) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.refs == 0 {
		ctx, cancel := context.WithCancel(context.Background())
		l.cancel = cancel
		l.start(ctx)
	}
	l.refs++
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			l.refs--
			if l.refs == 0 {
				l.cancel()
				l.cancel = nil
			}
		})
	}
}
