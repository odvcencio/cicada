package engine

import (
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
)

// OpCue has no engine behavior yet. Push must refuse it at the boundary, like
// any other invalid command. Before validation rejected it, Push returned true,
// the next Render hit the default branch of apply, and the engine stopped with
// a fault.
func TestCuePushIsRejectedAndEngineKeepsRendering(t *testing.T) {
	e, err := New(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	var left, right [128]float32
	for _, cue := range []cmd.Command{
		{Op: cmd.OpCue, Track: 0xff},
		{Op: cmd.OpCue, Track: 0xff, Arg0: 3},
		{Op: cmd.OpCue, Track: 0xff, Index: 2, Tick: 3840},
	} {
		if !e.Push(cue) {
			continue
		}
		e.Render(left[:], right[:])
		var message cmd.Message
		for e.Poll(&message) {
			if message.Kind == cmd.Fault {
				t.Fatalf("Push accepted %+v and the engine then faulted with code %d", cue, message.A)
			}
		}
		t.Fatalf("Push accepted %+v", cue)
	}
	if e.PushBatch([]cmd.Command{{Op: cmd.OpPlay, Track: 0xff}, {Op: cmd.OpCue, Track: 0xff}}) {
		t.Fatal("PushBatch accepted a batch that holds a cue")
	}
	if e.commandWrite != e.commandRead {
		t.Fatalf("a rejected push left %d queued commands", e.commandWrite-e.commandRead)
	}

	// The rejected pushes must leave the engine able to play.
	if !e.Push(cmd.Command{Op: cmd.OpNoteOn, Track: 0, Arg0: 45 | 100<<8}) {
		t.Fatal("note command rejected after a refused cue")
	}
	energy := float64(0)
	for block := 0; block < 20; block++ {
		e.Render(left[:], right[:])
		for i := range left {
			energy += float64(left[i]*left[i] + right[i]*right[i])
		}
		var message cmd.Message
		for e.Poll(&message) {
			if message.Kind == cmd.Fault {
				t.Fatalf("engine faulted after a refused cue: %+v", message)
			}
		}
	}
	if e.faulted || energy < 1e-7 {
		t.Fatalf("engine stopped after a refused cue: faulted=%v energy=%g", e.faulted, energy)
	}
}
