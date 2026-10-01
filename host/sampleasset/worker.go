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

const (
	initialRetryDelay = 25 * time.Millisecond
	maximumRetryDelay = time.Second
)

type pageFailure struct {
	asset   uint32
	index   int64
	err     error
	retryAt time.Time
	delay   time.Duration
	order   uint64
}

// Worker has two fixed goroutines: serial I/O and cancellation monitoring. A
// stalled, uncooperative source occupies at most one arena page and one reader
// goroutine; it cannot stall Render or spawn an unbounded backlog of seeks.
// Sources remain caller-owned and may close only after Done has closed.
type Worker struct {
	cache        *stream.Cache
	sources      map[uint32]PageSource
	ctx          context.Context
	cancel       context.CancelFunc
	done         chan struct{}
	mu           sync.Mutex // worker/control only; never reached by Render
	pending      stream.Work
	readCancel   context.CancelFunc
	failures     []pageFailure // at most one entry per admitted arena page
	failureOrder uint64
}

func StartWorker(parent context.Context, cache *stream.Cache, sources map[uint32]PageSource) *Worker {
	ctx, cancel := context.WithCancel(parent)
	w := &Worker{cache: cache, sources: make(map[uint32]PageSource, len(sources)), ctx: ctx, cancel: cancel, done: make(chan struct{}),
		failures: make([]pageFailure, int(cache.Stats().ArenaBytes/stream.PageBytes))}
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

// LastError reports the newest unresolved error for a demanded missing page.
// Successful publication retires its error; seeks retire obsolete demand.
func (w *Worker) LastError() error { return w.pageError(nil) }

func (w *Worker) pageError(reader *stream.Reader) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	var err error
	var order uint64
	for _, f := range w.failures {
		if f.err == nil || f.order < order {
			continue
		}
		needed := false
		if reader == nil {
			needed = w.cache.Wanted(f.asset, f.index)
		} else {
			needed = reader.Needs(f.asset, f.index)
		}
		if needed && !w.cache.Ready(f.asset, f.index) {
			err, order = f.err, f.order
		}
	}
	return err
}

func (w *Worker) eligible(asset uint32, index int64) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, f := range w.failures {
		if f.err != nil && f.asset == asset && f.index == index {
			return !time.Now().Before(f.retryAt)
		}
	}
	return true
}

// recordFailureLocked keeps retry/error state bounded across seek storms. It
// reclaims retired demand first; if mailbox snapshots are all uncertain at
// capacity, the oldest record is replaced rather than growing a history queue.
func (w *Worker) recordFailureLocked(asset uint32, index int64, err error) {
	slot, oldest := -1, 0
	for i, f := range w.failures {
		if f.err != nil && f.asset == asset && f.index == index {
			slot = i
			break
		}
		if f.err == nil || !w.cache.Wanted(f.asset, f.index) {
			w.failures[i] = pageFailure{}
			if slot < 0 {
				slot = i
			}
		}
		if f.order < w.failures[oldest].order {
			oldest = i
		}
	}
	if slot < 0 {
		slot = oldest
	}
	f := w.failures[slot]
	delay := initialRetryDelay
	if f.err != nil && f.asset == asset && f.index == index {
		delay = min(f.delay*2, maximumRetryDelay)
	}
	w.failureOrder++
	w.failures[slot] = pageFailure{asset: asset, index: index, err: err, retryAt: time.Now().Add(delay), delay: delay, order: w.failureOrder}
}

func (w *Worker) clearFailureLocked(asset uint32, index int64) {
	for i, f := range w.failures {
		if f.err != nil && f.asset == asset && f.index == index {
			w.failures[i] = pageFailure{}
			return
		}
	}
}

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
		work, ok := w.cache.NextEligible(w.eligible)
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
				w.mu.Lock()
				work.Fail()
				w.recordFailureLocked(work.Asset.ID, work.Index, err)
				w.mu.Unlock()
			} else {
				w.mu.Lock()
				if work.Publish() {
					w.clearFailureLocked(work.Asset.ID, work.Index)
				}
				w.mu.Unlock()
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
		if err := w.pageError(reader); err != nil {
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
