package main

import (
	"context"
	"testing"
)

func TestSharedLoopStartsOnceAndStopsWithTheLastPanel(t *testing.T) {
	var starts int
	var current context.Context
	loop := &sharedLoop{start: func(ctx context.Context) { starts++; current = ctx }}
	releaseWorkspace := loop.acquire()
	releaseTakes := loop.acquire()
	if starts != 1 {
		t.Fatalf("two panels started %d loops, want 1", starts)
	}
	releaseWorkspace()
	releaseWorkspace() // a second release of the same handle is harmless
	if current.Err() != nil {
		t.Fatal("the loop stopped while the Takes panel still needed it")
	}
	releaseTakes()
	if current.Err() == nil {
		t.Fatal("the loop kept running after the last panel unmounted")
	}
	first := current
	releaseAgain := loop.acquire()
	defer releaseAgain()
	if starts != 2 || current == first || current.Err() != nil {
		t.Fatalf("a remount must start a fresh loop: starts=%d", starts)
	}
}
