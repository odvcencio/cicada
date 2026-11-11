package main

import "testing"

// Two reasons held in either order, released in either order: the lock holds
// until both are gone and nothing else is remembered. The earlier design saved
// each lock's disabled=true as the button's original state and restored it.
func TestHistoryLockReasonsAreIndependent(t *testing.T) {
	orders := [][4]uint32{
		{historyQueueBlocked, historyRecording, historyQueueBlocked, historyRecording},
		{historyQueueBlocked, historyRecording, historyRecording, historyQueueBlocked},
		{historyRecording, historyQueueBlocked, historyQueueBlocked, historyRecording},
		{historyRecording, historyQueueBlocked, historyRecording, historyQueueBlocked},
	}
	for _, o := range orders {
		var l historyLock
		l.Set(o[0], true)
		l.Set(o[1], true)
		if !l.Locked() || l.Names() != "queue recording" {
			t.Fatalf("%v: both reasons held, got %q", o, l.Names())
		}
		l.Set(o[2], false)
		if !l.Locked() {
			t.Fatalf("%v: one reason left but unlocked", o)
		}
		l.Set(o[3], false)
		if l.Locked() || l.Names() != "" {
			t.Fatalf("%v: all reasons released but locked: %q", o, l.Names())
		}
		l.Set(o[3], false)
		if l.Locked() {
			t.Fatalf("%v: releasing twice locked", o)
		}
	}
}
