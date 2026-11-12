package project

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

const cacheScore = `title "Cache"
tempo 120
instrument tone {
  voice mono {
    out = sine(pitch) * env(gate, 200ms) * velocity * 0.3
  }
}
track lead tone {}
pattern line notes steps=4 { c4 . e4 . }
scene main { lead=line }
song { main }
`

// A state compiles each instrument once, and the state With derives reuses
// every instrument whose declaration it did not change.
func TestStateCompilesEachInstrumentOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.cicada")
	if err := os.WriteFile(path, []byte(cacheScore), 0o600); err != nil {
		t.Fatal(err)
	}
	state := NewState(path, []byte(cacheScore), nil, StateOptions{})
	if _, err := state.Plan(); err != nil {
		t.Fatal(err)
	}
	// FromScore's ValidateProject is the only check of this state; its one
	// instrument is compiled once.
	if len(state.instruments.entries) != 1 {
		t.Fatalf("%d instruments remembered, want 1", len(state.instruments.entries))
	}
	var program any
	for _, result := range state.instruments.entries {
		program = result.program
	}
	tempo, _ := state.With(map[string][]byte{path: bytes.Replace([]byte(cacheScore), []byte("tempo 120"), []byte("tempo 121"), 1)})
	next := tempo.(*State)
	if _, err := next.Plan(); err != nil {
		t.Fatal(err)
	}
	for _, result := range next.instruments.entries {
		if any(result.program) != program {
			t.Fatal("an unchanged instrument was compiled again")
		}
	}
	// A state between two intents of a batch is parsed but not compiled; the
	// state after it still reuses the instrument.
	between, _ := next.With(map[string][]byte{path: bytes.Replace([]byte(cacheScore), []byte("tempo 120"), []byte("tempo 122"), 1)})
	if _, _, err := between.Score(); err != nil {
		t.Fatal(err)
	}
	after, _ := between.With(map[string][]byte{path: bytes.Replace([]byte(cacheScore), []byte("tempo 120"), []byte("tempo 123"), 1)})
	if _, err := after.Plan(); err != nil {
		t.Fatal(err)
	}
	for _, result := range after.(*State).instruments.entries {
		if any(result.program) != program {
			t.Fatal("an instrument was compiled again after a state that only parsed")
		}
	}
	louder, _ := next.With(map[string][]byte{path: bytes.Replace([]byte(cacheScore), []byte("* 0.3"), []byte("* 0.4"), 1)})
	changed := louder.(*State)
	if _, err := changed.Plan(); err != nil {
		t.Fatal(err)
	}
	if len(changed.instruments.entries) != 1 {
		t.Fatalf("%d instruments remembered, want only the changed one", len(changed.instruments.entries))
	}
	for _, result := range changed.instruments.entries {
		if any(result.program) == program {
			t.Fatal("a changed instrument reused the old program")
		}
	}
	kept := changed.Kept()
	if kept.instruments.previous != nil || len(kept.instruments.entries) != 1 {
		t.Fatal("a kept state still links to the states before it")
	}
}
