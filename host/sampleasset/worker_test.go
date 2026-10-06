package sampleasset

import (
	"context"
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"m31labs.dev/cicada/kernel/stream"
)

type countedPageSource struct {
	err   error
	reads atomic.Int64
}

func (s *countedPageSource) ReadFrames(ctx context.Context, first int64, left, right []float32) error {
	s.reads.Add(1)
	if s.err != nil {
		return s.err
	}
	return (constantSource{}).ReadFrames(ctx, first, left, right)
}

func stopWorker(t *testing.T, w *Worker) {
	t.Helper()
	w.Cancel()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := w.Wait(ctx); err != nil {
		t.Error(err)
	}
}

func TestFailingCurrentPageDoesNotStarveHealthyPrefetch(t *testing.T) {
	assets := []stream.Asset{{ID: 1, Frames: 48000 * 3600, RateHz: 48000, Channels: 2}, {ID: 2, Frames: 48000 * 3600, RateHz: 48000, Channels: 2}}
	cache, err := stream.New(stream.Config{Pages: 12, Readers: 2, AheadPages: 3}, assets)
	if err != nil {
		t.Fatal(err)
	}
	badReader, _ := cache.NewReader(1)
	healthyReader, _ := cache.NewReader(2)
	badReader.SeekFrame(0, 1)
	healthyReader.SeekFrame(0, 1)
	bad := &countedPageSource{err: errors.New("permanent storage failure")}
	healthy := &countedPageSource{}
	w := StartWorker(context.Background(), cache, map[uint32]PageSource{1: bad, 2: healthy})
	defer stopWorker(t, w)
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if err := w.WaitReady(ctx, healthyReader); err != nil {
		t.Fatalf("healthy prefetch starved: %v; failed reads=%d healthy reads=%d", err, bad.reads.Load(), healthy.reads.Load())
	}
	if err := w.WaitReady(ctx, badReader); !errors.Is(err, bad.err) {
		t.Fatal("failed reader lost its storage error", err)
	}
	if healthy.reads.Load() != 4 {
		t.Fatalf("healthy current/prefetch pages=%d, want 4", healthy.reads.Load())
	}
	t.Logf("failed reads=%d healthy ready pages=%d", bad.reads.Load(), healthy.reads.Load())
}

type recoveringPageSource struct {
	failure           error
	currentReads      int
	prefetchStarted   bool
	prefetch, release chan struct{}
}

func (s *recoveringPageSource) ReadFrames(ctx context.Context, first int64, left, right []float32) error {
	if first == 0 {
		s.currentReads++
		if s.currentReads == 1 {
			return s.failure
		}
	} else {
		if !s.prefetchStarted {
			s.prefetchStarted = true
			close(s.prefetch)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.release:
		}
	}
	return (constantSource{}).ReadFrames(ctx, first, left, right)
}

func TestWaitReadyClearsRecoveredPageErrorWhilePrefetchPending(t *testing.T) {
	cache, err := stream.New(stream.Config{Pages: 3, Readers: 1, AheadPages: 0}, []stream.Asset{{ID: 1, Frames: 48000 * 3600, RateHz: 48000, Channels: 2}})
	if err != nil {
		t.Fatal(err)
	}
	reader, _ := cache.NewReader(1)
	reader.SeekFrame(0, 1)
	s := &recoveringPageSource{failure: errors.New("transient storage failure"), prefetch: make(chan struct{}), release: make(chan struct{})}
	w := StartWorker(context.Background(), cache, map[uint32]PageSource{1: s})
	defer stopWorker(t, w)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	// With no ahead pages, recovery cannot be held behind a stalled prefetch.
	// Advance the window only after publication: it still needs the recovered
	// page behind it, plus the deliberately blocked next page.
	for {
		l, rr, ok := reader.ReadFrame(0)
		if ok {
			if l != .25 || rr != -.25 {
				t.Fatal("recovered PCM differs")
			}
			break
		}
		if ctx.Err() != nil {
			t.Fatal("current page has not recovered")
		}
		runtime.Gosched()
	}
	reader.SeekFrame(stream.PageFrames, 1)
	waitSignal(t, s.prefetch)
	result := make(chan error, 1)
	go func() { result <- w.WaitReady(ctx, reader) }()
	select {
	case err := <-result:
		t.Fatalf("WaitReady returned before pending prefetch: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	if err := w.LastError(); err != nil {
		t.Fatal("success retained an obsolete page error", err)
	}
	close(s.release)
	if err := <-result; err != nil {
		t.Fatal("recovered window did not become ready", err)
	}
}

type switchablePageSource struct {
	err     error
	healthy atomic.Bool
}

func (s *switchablePageSource) ReadFrames(ctx context.Context, first int64, left, right []float32) error {
	if !s.healthy.Load() {
		return s.err
	}
	return (constantSource{}).ReadFrames(ctx, first, left, right)
}

func TestPageErrorsRemainIndependentAcrossAssetRecovery(t *testing.T) {
	assets := []stream.Asset{{ID: 1, Frames: 48000 * 3600, RateHz: 48000, Channels: 2}, {ID: 2, Frames: 48000 * 3600, RateHz: 48000, Channels: 2}}
	cache, err := stream.New(stream.Config{Pages: 6, Readers: 2, AheadPages: 0}, assets)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := cache.NewReader(1)
	b, _ := cache.NewReader(2)
	a.SeekFrame(0, 1)
	b.SeekFrame(0, 1)
	first := &switchablePageSource{err: errors.New("first asset failure")}
	second := &countedPageSource{err: errors.New("second asset failure")}
	w := StartWorker(context.Background(), cache, map[uint32]PageSource{1: first, 2: second})
	defer stopWorker(t, w)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := w.WaitReady(ctx, b); !errors.Is(err, second.err) {
		t.Fatal("second page error was not reported", err)
	}
	if err := w.WaitReady(ctx, a); !errors.Is(err, first.err) {
		t.Fatal("second failure overwrote the first page error", err)
	}
	first.healthy.Store(true)
	for !cache.Ready(1, 0) {
		if ctx.Err() != nil {
			t.Fatal("first asset did not recover")
		}
		runtime.Gosched()
	}
	if err := w.WaitReady(ctx, a); err != nil {
		t.Fatal("recovered asset retained its page error", err)
	}
	if err := w.LastError(); !errors.Is(err, second.err) {
		t.Fatal("one recovery cleared another asset's error", err)
	}
	var l, r [128]float32
	if allocations := testing.AllocsPerRun(1000, func() { a.Render(l[:], r[:]); b.Render(l[:], r[:]) }); allocations != 0 {
		t.Fatalf("failure backoff reached callback allocations: %g", allocations)
	}
}
