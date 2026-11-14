package main

import (
	"context"
	"testing"
	"time"
)

func TestSamplerLoaderCoalescesWithoutStarvingSlowDecode(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started, unblock, delivered := make(chan struct{}), make(chan struct{}), make(chan uint64, 2)
	count := 0
	l := newSamplerLoader(ctx, func(context.Context, uint64) (int, error) {
		count++
		if count == 1 {
			close(started)
			<-unblock
		}
		return count, nil
	}, func(g uint64, _ int, _ error) { delivered <- g })
	l.request(false)
	samplerWait(t, started)
	for i := 0; i < 10; i++ {
		l.request(false)
	}
	close(unblock)
	select {
	case <-delivered:
	case <-time.After(time.Second):
		t.Fatal("slow decode starved")
	}
	select {
	case <-delivered:
	case <-time.After(time.Second):
		t.Fatal("coalesced refresh missing")
	}
}

func TestSamplerLoaderRejectsInvalidatedDecode(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started, unblock := make(chan struct{}), make(chan struct{})
	delivered := make(chan uint64, 2)
	l := newSamplerLoader(ctx, func(_ context.Context, g uint64) (uint64, error) {
		if g == 0 {
			close(started)
			<-unblock
		}
		return g, nil
	}, func(g uint64, _ uint64, _ error) { delivered <- g })
	l.request(false)
	samplerWait(t, started)
	l.request(true)
	close(unblock)
	select {
	case g := <-delivered:
		if g != 1 {
			t.Fatalf("stale generation delivered: %d", g)
		}
	case <-time.After(time.Second):
		t.Fatal("new generation missing")
	}
	if l.current(0) {
		t.Fatal("stale queued commit still valid")
	}
}

func TestSamplerLoaderBlockedDecodeDoesNotBlockTerminalRelease(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started, unblock, released := make(chan struct{}), make(chan struct{}), make(chan struct{})
	l := newSamplerLoader(ctx, func(context.Context, uint64) (int, error) { close(started); <-unblock; return 0, nil }, func(uint64, int, error) {})
	x := newSamplerExecutor(1, func(samplerCommand, func() bool) {}, func() bool { close(released); return false })
	l.request(false)
	samplerWait(t, started)
	x.close()
	samplerWait(t, released)
	cancel()
	close(unblock)
}

func TestSamplerLoaderQueuedCommitChecksGeneration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	delivered := make(chan uint64, 1)
	l := newSamplerLoader(ctx, func(context.Context, uint64) (int, error) { return 42, nil }, func(g uint64, _ int, _ error) { delivered <- g })
	l.request(false)
	var generation uint64
	select {
	case generation = <-delivered:
	case <-time.After(time.Second):
		t.Fatal("delivery timed out")
	}
	l.request(true)
	applied := false
	if l.commit(generation, func() { applied = true }) || applied {
		t.Fatal("stale queued result committed")
	}
}

func TestSamplerLoaderInvalidationCancelsStalledPreparation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	delivered := make(chan uint64, 1)
	l := newSamplerLoader(ctx, func(ctx context.Context, g uint64) (uint64, error) {
		if g == 0 {
			close(started)
			<-ctx.Done()
			return 0, ctx.Err()
		}
		return g, nil
	}, func(g uint64, _ uint64, _ error) { delivered <- g })
	l.request(false)
	samplerWait(t, started)
	l.request(true)
	select {
	case g := <-delivered:
		if g != 1 {
			t.Fatalf("got stale generation %d", g)
		}
	case <-time.After(time.Second):
		t.Fatal("new generation blocked behind cancelled preparation")
	}
}
