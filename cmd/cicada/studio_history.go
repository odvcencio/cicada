package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"
)

const studioHistoryLimit = 512

type studioHistoryEntry struct {
	Seq    uint64    `json:"seq"`
	At     time.Time `json:"at"`
	Bar    int64     `json:"bar"`
	Step   int64     `json:"step"`
	Kind   string    `json:"kind"`
	Detail string    `json:"detail"`
}

// studioHistory records the current workstation session. The score file and
// Git remain the durable source history; this log adds musical landing times.
type studioHistory struct {
	mu           sync.Mutex
	entries      []studioHistoryEntry
	nextSeq      uint64
	lastRevision string
}

func newStudioHistory(source []byte) *studioHistory {
	h := &studioHistory{lastRevision: studioRevision(source)}
	h.record("loaded", "Score loaded", 0, 0, "")
	return h
}

func (h *studioHistory) record(kind, detail string, bar, step int64, revision string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextSeq++
	h.entries = append(h.entries, studioHistoryEntry{
		Seq: h.nextSeq, At: time.Now().UTC(), Bar: bar, Step: step, Kind: kind, Detail: detail,
	})
	if len(h.entries) > studioHistoryLimit {
		h.entries = append([]studioHistoryEntry(nil), h.entries[len(h.entries)-studioHistoryLimit:]...)
	}
	if revision != "" {
		h.lastRevision = revision
	}
}

func (h *studioHistory) observe(source []byte, bar, step int64) bool {
	revision := studioRevision(source)
	h.mu.Lock()
	defer h.mu.Unlock()
	if revision == h.lastRevision {
		return false
	}
	h.lastRevision = revision
	h.nextSeq++
	h.entries = append(h.entries, studioHistoryEntry{
		Seq: h.nextSeq, At: time.Now().UTC(), Bar: bar, Step: step,
		Kind: "external", Detail: "Score changed outside Studio",
	})
	if len(h.entries) > studioHistoryLimit {
		h.entries = append([]studioHistoryEntry(nil), h.entries[len(h.entries)-studioHistoryLimit:]...)
	}
	return true
}

func (h *studioHistory) snapshot() []studioHistoryEntry {
	h.mu.Lock()
	defer h.mu.Unlock()
	result := make([]studioHistoryEntry, len(h.entries))
	for i := range h.entries {
		result[i] = h.entries[len(h.entries)-1-i]
	}
	return result
}

func (s *studio) observeHistory() {
	s.mu.Lock()
	defer s.mu.Unlock()
	source, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	position := s.transport.snapshot()
	bar, step := historyPosition(position)
	s.history.observe(source, bar, step)
}

func (s *studio) watchHistory(ctx context.Context) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.observeHistory()
		}
	}
}

func historyPosition(state transportSnapshot) (bar, step int64) {
	if !state.Playing && state.Bar == 1 && state.Landed == 0 {
		return 0, 0
	}
	return state.Bar, state.Step
}

func (s *studio) historyState(w http.ResponseWriter, _ *http.Request) {
	s.observeHistory()
	studioJSON(w, http.StatusOK, map[string]any{"events": s.history.snapshot()})
}

func (s *studio) recordEdit(edit studioEdit, revision string) {
	detail := "Source edited"
	if edit.Action == "move" {
		detail = fmt.Sprintf("Song block %d moved to %d", edit.Index+1, edit.Target+1)
	}
	if edit.Action == "bars" {
		detail = fmt.Sprintf("Song block %d set to %d bars", edit.Index+1, edit.Bars)
	}
	if edit.Pattern != "" {
		detail = fmt.Sprintf("%s step %d toggled", edit.Pattern, edit.Step+1)
		if edit.Lane != "" {
			detail = fmt.Sprintf("%s / %s step %d toggled", edit.Pattern, edit.Lane, edit.Step+1)
		}
	}
	bar, step := historyPosition(s.transport.snapshot())
	s.history.record("edit", detail, bar, step, revision)
}
