package main

import "sync/atomic"

// historyLock is the one owner of whether Undo and Redo may be pressed. Each
// reason is a bit; the buttons are locked while any bit is set. No reason
// stores the buttons' earlier state, so one reason cannot restore another's:
// the state shown is always computed from the reasons now held and the
// server's own markup.
type historyLock struct{ reasons atomic.Uint32 }

const (
	historyQueueBlocked = 1 << iota // an edit failed or a refresh is under way; the queue refuses edits
	historyRecording                // a Keep of a recorded automation pass is in flight
)

var history historyLock

// Set adds or removes one reason.
func (l *historyLock) Set(reason uint32, on bool) {
	for {
		old := l.reasons.Load()
		next := old &^ reason
		if on {
			next = old | reason
		}
		if l.reasons.CompareAndSwap(old, next) {
			return
		}
	}
}

// Locked reports whether any reason holds the buttons.
func (l *historyLock) Locked() bool { return l.reasons.Load() != 0 }

// Names lists the reasons held, for the data-history-lock attribute.
func (l *historyLock) Names() string {
	r, out := l.reasons.Load(), ""
	for _, c := range []struct {
		bit  uint32
		name string
	}{{historyQueueBlocked, "queue"}, {historyRecording, "recording"}} {
		if r&c.bit != 0 {
			if out != "" {
				out += " "
			}
			out += c.name
		}
	}
	return out
}
