package main

import (
	"testing"
	"time"
)

func samplerWait(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("sampler executor timed out")
	}
}

func TestSamplerExecutorOverflowRevokesUntilRelease(t *testing.T) {
	entered, unblock, released, finishRelease := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	validResult := make(chan bool, 1)
	x := newSamplerExecutor(1, func(c samplerCommand, valid func() bool) {
		close(entered)
		<-unblock
		validResult <- valid()
	}, func() bool {
		select {
		case <-released:
		default:
			close(released)
			<-finishRelease
		}
		return true
	})
	if !x.submit(samplerCommand{dirties: true}) {
		t.Fatal("first command rejected")
	}
	samplerWait(t, entered)
	if !x.dirtySnapshot() {
		t.Fatal("in-flight edit must be dirty")
	}
	if !x.submit(samplerCommand{dirties: true}) {
		t.Fatal("queued command rejected")
	}
	if x.submit(samplerCommand{}) {
		t.Fatal("overflow accepted")
	}
	if !x.dirtySnapshot() {
		t.Fatal("overflow must remain conservatively dirty")
	}
	close(unblock)
	if <-validResult {
		t.Fatal("in-flight generation remained valid")
	}
	samplerWait(t, released)
	if x.submit(samplerCommand{}) {
		t.Fatal("accepted during cleanup")
	}
	close(finishRelease)
	x.close()
}

func TestSamplerExecutorDirtyAndTerminalRelease(t *testing.T) {
	entered, proceed, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var x *samplerExecutor
	x = newSamplerExecutor(2, func(c samplerCommand, valid func() bool) {
		close(entered)
		<-proceed
		if valid() {
			x.publishDirty(false)
		}
	}, func() bool { close(done); return true })
	x.publishDirty(true)
	if !x.submit(samplerCommand{dirties: true}) {
		t.Fatal("command rejected")
	}
	samplerWait(t, entered)
	if !x.dirtySnapshot() {
		t.Fatal("in-flight save reported clean")
	}
	x.close()
	close(proceed)
	samplerWait(t, done)
	if x.submit(samplerCommand{}) {
		t.Fatal("closed executor accepted work")
	}
}

func TestSamplerExecutorCloseDuringOverflowRunsTerminalCleanup(t *testing.T) {
	entered, unblock, cleanupEntered, cleanupContinue, terminal := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	calls := 0
	x := newSamplerExecutor(1, func(samplerCommand, func() bool) { close(entered); <-unblock }, func() bool {
		calls++
		if calls == 1 {
			close(cleanupEntered)
			<-cleanupContinue
		} else {
			close(terminal)
		}
		return true
	})
	x.submit(samplerCommand{})
	samplerWait(t, entered)
	x.submit(samplerCommand{})
	if x.submit(samplerCommand{}) {
		t.Fatal("overflow accepted")
	}
	close(unblock)
	samplerWait(t, cleanupEntered)
	x.close()
	close(cleanupContinue)
	samplerWait(t, terminal)
	if !x.dirtySnapshot() {
		t.Fatal("cleanup's dirty result was lost")
	}
}

func TestSamplerExecutorCoalescesClockWithoutOverflow(t *testing.T) {
	entered, unblock, released := make(chan struct{}), make(chan struct{}), make(chan struct{})
	x := newSamplerExecutor(1, func(c samplerCommand, _ func() bool) {
		if c.kind == "block" {
			close(entered)
			<-unblock
		}
	}, func() bool { close(released); return false })
	x.submit(samplerCommand{kind: "block"})
	samplerWait(t, entered)
	for i := 0; i < 100; i++ {
		if !x.submit(samplerCommand{kind: "clock", coalesce: true}) {
			t.Fatal("coalesced clock overflowed")
		}
	}
	x.close()
	close(unblock)
	samplerWait(t, released)
}

func TestSamplerExecutorCoalescesOnlyAdjacentGestureMoves(t *testing.T) {
	entered, unblock, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var seen []samplerCommand
	x := newSamplerExecutor(4, func(c samplerCommand, _ func() bool) {
		if c.kind == "block" {
			close(entered)
			<-unblock
			return
		}
		seen = append(seen, c)
		if c.kind == "wave-end" {
			close(done)
		}
	}, func() bool { return false })
	x.submit(samplerCommand{kind: "block"})
	samplerWait(t, entered)
	for i := 0; i < 100; i++ {
		if !x.submit(samplerCommand{kind: "wave-move", number: 7, value: float64(i)}) {
			t.Fatal("move overflowed")
		}
	}
	if !x.submit(samplerCommand{kind: "wave-end", number: 7}) {
		t.Fatal("end rejected")
	}
	close(unblock)
	samplerWait(t, done)
	if len(seen) != 2 || seen[0].kind != "wave-move" || seen[0].value != 99 || seen[1].kind != "wave-end" {
		t.Fatalf("lost final move or ordering: %+v", seen)
	}
	x.close()
}

func TestSamplerExecutorPanicStillRunsCleanup(t *testing.T) {
	released := make(chan struct{})
	x := newSamplerExecutor(1, func(samplerCommand, func() bool) { panic("failed browser effect") }, func() bool {
		select {
		case <-released:
		default:
			close(released)
		}
		return true
	})
	if !x.submit(samplerCommand{kind: "start", dirties: true}) {
		t.Fatal("command rejected")
	}
	samplerWait(t, released)
	if !x.dirtySnapshot() {
		t.Fatal("cleanup dirty result lost")
	}
	x.close()
}
