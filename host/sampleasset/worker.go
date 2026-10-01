package sampleasset

import (
	"context"
	"fmt"
	"sync"
	"time"

	"m31labs.dev/cicada/kernel/stream"
)

// PageSource is called serially outside rendering. Implementations must fill
// both channels (duplicate mono) and honor cancellation when storage permits.
type PageSource interface {
	ReadFrames(context.Context, int64, []float32, []float32) error
}

// Worker has two fixed goroutines: serial I/O and cancellation monitoring. A
// stalled, uncooperative source occupies at most one arena page and one reader
// goroutine; it cannot stall Render or spawn an unbounded backlog of seeks.
// Sources remain caller-owned and may close only after Done has closed.
type Worker struct {
	cache      *stream.Cache
	sources    map[uint32]PageSource
	ctx        context.Context
	cancel     context.CancelFunc
	done       chan struct{}
	mu         sync.Mutex // worker/control only; never reached by Render
	pending    stream.Work
	readCancel context.CancelFunc
	lastError  error
	errorAsset uint32
	errorPage  int64
}

func StartWorker(parent context.Context, cache *stream.Cache, sources map[uint32]PageSource) *Worker {
	ctx, cancel := context.WithCancel(parent)
	w := &Worker{cache: cache, sources: make(map[uint32]PageSource, len(sources)), ctx: ctx, cancel: cancel, done: make(chan struct{})}
	for id, source := range sources {
		w.sources[id] = source
	}
	go w.run()
	return w
}

// Cancel never waits for a stalled source. Wait (or Done) is a host teardown
// operation, outside rendering, before closing source files or dropping plans.
func (w *Worker) Cancel()               { w.cancel() }
func (w *Worker) Done() <-chan struct{} { return w.done }
func (w *Worker) Wait(ctx context.Context) error {
	select {
	case <-w.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (w *Worker) LastError() error { w.mu.Lock(); defer w.mu.Unlock(); return w.lastError }

func (w *Worker) watch(done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(2 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
			w.mu.Lock()
			if w.readCancel != nil && !w.pending.Wanted() {
				w.readCancel()
			}
			w.mu.Unlock()
		}
	}
}

func (w *Worker) run() {
	watchDone := make(chan struct{})
	go w.watch(watchDone)
	defer func() { w.cancel(); <-watchDone; close(w.done) }()
	ticker := time.NewTicker(2 * time.Millisecond)
	defer ticker.Stop()
	for {
		if w.ctx.Err() != nil {
			return
		}
		work, ok := w.cache.Next()
		if ok {
			ctx, cancel := context.WithCancel(w.ctx)
			w.mu.Lock()
			w.pending, w.readCancel = work, cancel
			w.mu.Unlock()
			source := w.sources[work.Asset.ID]
			var err error
			if source == nil {
				err = fmt.Errorf("missing source for streamed asset %d", work.Asset.ID)
			} else {
				err = source.ReadFrames(ctx, work.Start, work.Left(), work.Right())
			}
			cancelled := ctx.Err() != nil
			cancel()
			w.mu.Lock()
			w.readCancel = nil
			w.mu.Unlock()
			if cancelled {
				work.Cancel()
			} else if err != nil {
				work.Fail()
				w.mu.Lock()
				w.lastError = err
				w.errorAsset, w.errorPage = work.Asset.ID, work.Index
				w.mu.Unlock()
			} else {
				work.Publish()
				continue
			}
		}
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// WaitReady allows offline/control callers to prime the current window. It
// waits outside the callback and fails explicitly on source errors or deadline.
// Reader intent must remain unchanged until it returns.
func (w *Worker) WaitReady(ctx context.Context, reader *stream.Reader) error {
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if reader.WindowReady() {
			if err := ctx.Err(); err != nil {
				return err
			}
			return nil
		}
		w.mu.Lock()
		err := w.lastError
		needed := reader.Needs(w.errorAsset, w.errorPage)
		w.mu.Unlock()
		if err != nil && needed {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-w.ctx.Done():
			return w.ctx.Err()
		case <-ticker.C:
		}
	}
}
