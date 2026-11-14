package main

import (
	"context"
	"sync"
	"time"
)

// samplerLoader keeps network and decode work off the state executor. Periodic
// refreshes coalesce without revoking the currently running decode; actual
// state changes explicitly invalidate it. deliver must arrange its commit on
// the state executor and check current again immediately before applying it.
type samplerLoader[T any] struct {
	mu         sync.Mutex
	ctx        context.Context
	wake       chan struct{}
	pending    bool
	generation uint64
	cancel     context.CancelFunc
	prepare    func(context.Context, uint64) (T, error)
	deliver    func(uint64, T, error)
}

func newSamplerLoader[T any](ctx context.Context, prepare func(context.Context, uint64) (T, error), deliver func(uint64, T, error)) *samplerLoader[T] {
	l := &samplerLoader[T]{ctx: ctx, wake: make(chan struct{}, 1), prepare: prepare, deliver: deliver}
	go l.loop()
	return l
}

// request schedules at most one follow-up while preparation is running.
// Invalidate only when an edit makes an in-flight snapshot obsolete, not on a
// periodic tick: otherwise a slow decode could be starved indefinitely.
func (l *samplerLoader[T]) request(invalidate bool) {
	l.mu.Lock()
	if invalidate {
		l.generation++
		if l.cancel != nil {
			l.cancel()
		}
	}
	l.pending = true
	l.mu.Unlock()
	select {
	case l.wake <- struct{}{}:
	default:
	}
}

func (l *samplerLoader[T]) current(generation uint64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.ctx.Err() == nil && generation == l.generation
}

// commit applies only non-yielding state assignments under the generation
// lock. The caller performs DOM effects afterwards, on the state executor;
// apply must not call JavaScript or reenter the loader.
func (l *samplerLoader[T]) commit(generation uint64, apply func()) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ctx.Err() != nil || generation != l.generation {
		return false
	}
	apply()
	return true
}

func (l *samplerLoader[T]) loop() {
	for {
		select {
		case <-l.ctx.Done():
			return
		case <-l.wake:
		}
		for {
			l.mu.Lock()
			if !l.pending || l.ctx.Err() != nil {
				l.mu.Unlock()
				break
			}
			l.pending = false
			generation := l.generation
			ctx, cancel := context.WithTimeout(l.ctx, 30*time.Second)
			l.cancel = cancel
			l.mu.Unlock()
			result, err := l.prepare(ctx, generation)
			cancel()
			l.mu.Lock()
			l.cancel = nil
			l.mu.Unlock()
			if l.current(generation) {
				l.deliver(generation, result, err)
			}
		}
	}
}
