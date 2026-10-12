//go:build !tinygo && !js

package engine

import (
	"m31labs.dev/cicada/kernel/cmd"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

type parallelResetGuardVoice struct {
	parallelStalledVoice
	resets atomic.Int32
}

func (v *parallelResetGuardVoice) Reset() { v.resets.Add(1) }

func TestParallelRenderFaultKeepsOutstandingWorkerOwnership(t *testing.T) {
	if runtime.GOMAXPROCS(0) < 2 {
		t.Skip("requires multiple scheduler lanes")
	}
	for _, fullMessages := range []bool{false, true} {
		e := parallelFixture(t, 8, false)
		v := &parallelResetGuardVoice{parallelStalledVoice: parallelStalledVoice{entered: make(chan struct{}), release: make(chan struct{})}}
		e.voices[1].prepared = v
		if err := e.EnableParallel(2); err != nil {
			t.Fatal(err)
		}
		e.parallel.waitFloor = time.Millisecond
		if fullMessages {
			for i := range e.messages {
				e.messages[i] = cmd.Message{Kind: cmd.NoteOn}
			}
			e.messageWrite = uint16(len(e.messages))
		}
		var l, r [128]float32
		e.Render(l[:], r[:])
		// Cleanup always releases a stalled worker, even when an assertion fails.
		t.Cleanup(func() { close(v.release); e.Close() })
		if !e.faulted || !e.parallelRetired() {
			t.Fatal("failed job was not retired")
		}
		if v.resets.Load() != 0 {
			t.Fatal("fault reset a source still owned by the worker")
		}
		e.Reset()
		if v.resets.Load() != 0 || !e.faulted {
			t.Fatal("reset resumed a retired job")
		}
		for i := range l {
			if l[i] != 0 || r[i] != 0 {
				t.Fatal("fault did not silence output")
			}
		}
		found := false
		var m cmd.Message
		for e.Poll(&m) {
			if m.Kind == cmd.Fault && m.A == FaultParallelWorker {
				found = true
			}
		}
		if !found {
			t.Fatal("parallel fault status was lost")
		}
	}
}
