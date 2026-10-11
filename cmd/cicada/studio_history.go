package main

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/edit/editlog"
	"m31labs.dev/cicada/host/takejournal"
	"m31labs.dev/cicada/project"
)

//go:embed studio-history.js
var studioHistoryScript []byte

func (s *studio) historyScript(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(studioHistoryScript)
}

const (
	studioEditHistoryLimit      = 512
	studioTransportHistoryLimit = 512
)

type studioHistoryEntry struct {
	ID             uint64    `json:"id"`
	At             time.Time `json:"at"`
	Label          string    `json:"label"`
	RevisionBefore string    `json:"revisionBefore"`
	RevisionAfter  string    `json:"revisionAfter"`
	Diff           string    `json:"diff"`
	files          []studioAuxiliaryFile
	beforeSource   []byte
	afterSource    []byte
}

type studioTransportEvent struct {
	Seq    uint64    `json:"seq"`
	At     time.Time `json:"at"`
	Bar    int64     `json:"bar"`
	Step   int64     `json:"step"`
	Kind   string    `json:"kind"`
	Detail string    `json:"detail"`
}

type studioHistory struct {
	mu                  sync.Mutex
	edits               []studioHistoryEntry
	events              []studioTransportEvent
	undoStack           []uint64
	redoStack           []uint64
	nextEditID          uint64
	nextEventSeq        uint64
	lastSource          []byte
	lastRevision        string
	pendingTakeRevision string
}

type studioHistorySnapshot struct {
	Edits    []studioHistoryEntry   `json:"edits"`
	Events   []studioTransportEvent `json:"events"`
	CanUndo  bool                   `json:"canUndo"`
	CanRedo  bool                   `json:"canRedo"`
	Revision string                 `json:"revision"`
}

type studioHistoryWriteKind uint8

const (
	studioHistoryWriteNew studioHistoryWriteKind = iota
	studioHistoryWriteUndo
	studioHistoryWriteRedo
	studioHistoryWriteRevert
	studioHistoryWriteExternal
)

func newStudioHistory(source []byte) *studioHistory {
	return &studioHistory{lastSource: bytes.Clone(source), lastRevision: studioRevision(source)}
}

// expectRecordedTake arms the playback handoff before source exchange. The
// returned cancellation restores the prior handoff if the commit fails.
func (h *studioHistory) expectRecordedTake(revision string) func() {
	if revision == "" {
		return nil
	}
	h.mu.Lock()
	previous := h.pendingTakeRevision
	h.pendingTakeRevision = revision
	h.mu.Unlock()
	return func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.pendingTakeRevision == revision {
			h.pendingTakeRevision = previous
		}
	}
}

func (h *studioHistory) consumeRecordedTake(revision string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if revision == "" || h.pendingTakeRevision != revision {
		return false
	}
	h.pendingTakeRevision = ""
	return true
}

// record keeps transport activity in its own bounded ring. It never changes
// the source edit history or its undo stack.
func (h *studioHistory) record(kind, detail string, bar, step int64, _ string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextEventSeq++
	h.events = append(h.events, studioTransportEvent{
		Seq: h.nextEventSeq, At: time.Now().UTC(), Bar: bar, Step: step, Kind: kind, Detail: detail,
	})
	if len(h.events) > studioTransportHistoryLimit {
		h.events = append([]studioTransportEvent(nil), h.events[len(h.events)-studioTransportHistoryLimit:]...)
	}
}

func (h *studioHistory) observe(source []byte, _ int64, _ int64) bool {
	revision := studioRevision(source)
	h.mu.Lock()
	defer h.mu.Unlock()
	if revision == h.lastRevision {
		return false
	}
	h.recordSourceWriteLocked(h.lastSource, source, "External file change", studioHistoryWriteExternal, 0)
	return true
}

func (h *studioHistory) recordSourceWrite(before, after []byte, label string, kind studioHistoryWriteKind, targetID uint64) studioHistoryEntry {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.recordSourceWriteLocked(before, after, label, kind, targetID)
}

func (h *studioHistory) recordSourceWriteLocked(before, after []byte, label string, kind studioHistoryWriteKind, targetID uint64) studioHistoryEntry {
	h.nextEditID++
	entry := studioHistoryEntry{
		ID: h.nextEditID, At: time.Now().UTC(), Label: normalizeStudioHistoryLabel(label),
		RevisionBefore: studioRevision(before), RevisionAfter: studioRevision(after),
		Diff: studioUnifiedDiff(before, after), beforeSource: bytes.Clone(before), afterSource: bytes.Clone(after),
	}
	h.edits = append(h.edits, entry)
	if len(h.edits) > studioEditHistoryLimit {
		h.edits = append([]studioHistoryEntry(nil), h.edits[len(h.edits)-studioEditHistoryLimit:]...)
	}
	h.lastSource = bytes.Clone(after)
	h.lastRevision = entry.RevisionAfter

	switch kind {
	case studioHistoryWriteUndo:
		h.popID(&h.undoStack, targetID)
		h.redoStack = append(h.redoStack, targetID)
	case studioHistoryWriteRedo:
		h.popID(&h.redoStack, targetID)
		h.undoStack = append(h.undoStack, targetID)
	case studioHistoryWriteRevert:
		h.undoStack = append(h.undoStack, entry.ID)
		h.redoStack = nil
	case studioHistoryWriteNew, studioHistoryWriteExternal:
		h.undoStack = append(h.undoStack, entry.ID)
		h.redoStack = nil
	}
	h.pruneStacksLocked()
	return cloneStudioHistoryEntry(entry)
}

func (h *studioHistory) popID(stack *[]uint64, id uint64) {
	current := *stack
	if len(current) != 0 && current[len(current)-1] == id {
		*stack = current[:len(current)-1]
		return
	}
	for i := len(current) - 1; i >= 0; i-- {
		if current[i] == id {
			*stack = append(current[:i], current[i+1:]...)
			return
		}
	}
}

func (h *studioHistory) pruneStacksLocked() {
	oldest := uint64(0)
	if len(h.edits) > 0 {
		oldest = h.edits[0].ID
	}
	prune := func(ids []uint64) []uint64 {
		result := ids[:0]
		for _, id := range ids {
			if id >= oldest && oldest != 0 {
				result = append(result, id)
			}
		}
		return result
	}
	h.undoStack = prune(h.undoStack)
	h.redoStack = prune(h.redoStack)
}

func (h *studioHistory) entry(id uint64) (studioHistoryEntry, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := len(h.edits) - 1; i >= 0; i-- {
		if h.edits[i].ID == id {
			return cloneStudioHistoryEntry(h.edits[i]), true
		}
	}
	return studioHistoryEntry{}, false
}

func (h *studioHistory) undoEntry() (studioHistoryEntry, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.undoStack) == 0 {
		return studioHistoryEntry{}, false
	}
	return h.entryLocked(h.undoStack[len(h.undoStack)-1])
}

func (h *studioHistory) redoEntry() (studioHistoryEntry, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.redoStack) == 0 {
		return studioHistoryEntry{}, false
	}
	return h.entryLocked(h.redoStack[len(h.redoStack)-1])
}

func (h *studioHistory) entryLocked(id uint64) (studioHistoryEntry, bool) {
	for i := len(h.edits) - 1; i >= 0; i-- {
		if h.edits[i].ID == id {
			return cloneStudioHistoryEntry(h.edits[i]), true
		}
	}
	return studioHistoryEntry{}, false
}

func (h *studioHistory) snapshot() []studioTransportEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	result := make([]studioTransportEvent, len(h.events))
	for i := range h.events {
		result[i] = h.events[len(h.events)-1-i]
	}
	return result
}

func (h *studioHistory) historySnapshot() studioHistorySnapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	result := studioHistorySnapshot{
		Edits:   make([]studioHistoryEntry, len(h.edits)),
		Events:  make([]studioTransportEvent, len(h.events)),
		CanUndo: len(h.undoStack) != 0, CanRedo: len(h.redoStack) != 0,
		Revision: h.lastRevision,
	}
	for i := range h.edits {
		result.Edits[i] = cloneStudioHistoryEntry(h.edits[len(h.edits)-1-i])
	}
	for i := range h.events {
		result.Events[i] = h.events[len(h.events)-1-i]
	}
	return result
}

func cloneStudioHistoryEntry(entry studioHistoryEntry) studioHistoryEntry {
	entry.files = cloneStudioFiles(entry.files)
	entry.beforeSource = bytes.Clone(entry.beforeSource)
	entry.afterSource = bytes.Clone(entry.afterSource)
	return entry
}

func normalizeStudioHistoryLabel(label string) string {
	label = strings.Join(strings.Fields(label), " ")
	if label == "" {
		return "Source edited"
	}
	runes := []rune(label)
	if len(runes) > 160 {
		label = string(runes[:160])
	}
	return label
}

func studioEditLabel(edit studioEdit) string {
	if edit.Label != "" {
		return edit.Label
	}
	if edit.Action == "project" {
		return "Project title, tempo, and key changed"
	}
	if edit.Action == "audio-track" {
		return "Audio track added · " + edit.NewName
	}
	if edit.Action == "clip-settings" {
		return "Audio region edited · " + edit.Pattern
	}
	if edit.Action == "clip-bind" {
		return fmt.Sprintf("Audio clip · %s assigned to %s / %s", edit.Pattern, edit.Scene, edit.Track)
	}
	if edit.Action == "automation-set" {
		return fmt.Sprintf("Automation · %s / %s set", edit.Scene, edit.Path)
	}
	if edit.Action == "automation-remove" {
		return fmt.Sprintf("Automation · %s / %s removed", edit.Scene, edit.Path)
	}
	if edit.Action == "undo" || edit.Action == "redo" {
		return edit.Label
	}
	if edit.Action == "revert" {
		return "History reverted"
	}
	if edit.Action == "record" {
		if edit.Pattern != "" {
			return fmt.Sprintf("Take committed · %s · %d notes", edit.Pattern, edit.Count)
		}
		return fmt.Sprintf("Take committed · %d notes across %d patterns", edit.Count, edit.PatternCount)
	}
	if edit.Action == "move" {
		return fmt.Sprintf("Song block %d moved to %d", edit.Index+1, edit.Target+1)
	}
	if edit.Action == "bars" {
		return fmt.Sprintf("Song block %d set to %d bars", edit.Index+1, edit.Bars)
	}
	if edit.Action == "append" {
		return fmt.Sprintf("Arrangement · %s added for %d bars", edit.Scene, edit.Bars)
	}
	if edit.Action == "delete" || edit.Action == "scene" || edit.Action == "duplicate" && edit.Pattern == "" {
		return fmt.Sprintf("Arrangement · block %d · %s", edit.Index+1, edit.Action)
	}
	if edit.Action == "duplicate" {
		return fmt.Sprintf("Pattern · %s duplicated as %s", edit.Pattern, edit.NewName)
	}
	if edit.Action == "settings" {
		return fmt.Sprintf("Pattern · %s timing and transpose changed", edit.Pattern)
	}
	if edit.Action == "bind" {
		return fmt.Sprintf("Scene · %s / %s assigned %s", edit.Scene, edit.Track, edit.Pattern)
	}
	if edit.Action == "step" {
		return fmt.Sprintf("Pattern · %s / %s step %d edited", edit.Pattern, edit.Lane, edit.Step+1)
	}
	if edit.Action == "range" && edit.Range != nil {
		return fmt.Sprintf("Pattern · %s / %s steps %d–%d · %s", edit.Pattern, edit.Lane, edit.Range.First+1, edit.Range.Last+1, edit.Range.Operation)
	}
	if edit.Action == "resize" {
		return fmt.Sprintf("Pattern · %s resized to %d steps", edit.Pattern, edit.Length)
	}
	if edit.Pattern != "" {
		what := "toggled"
		if edit.Pitch != nil {
			what = "pitch changed"
		} else if edit.Modifier != "" {
			what = edit.Modifier + " changed"
		}
		if edit.Lane != "" {
			return fmt.Sprintf("Grid · %s / %s step %d %s", edit.Pattern, edit.Lane, edit.Step+1, what)
		}
		return fmt.Sprintf("Grid · %s step %d %s", edit.Pattern, edit.Step+1, what)
	}
	return "Source saved"
}

func (s *studio) observeHistory() {
	s.mu.Lock()
	defer s.mu.Unlock()
	source, err := os.ReadFile(s.path)
	if err != nil || s.history == nil {
		return
	}
	s.history.observe(source, 0, 0)
}

func (s *studio) historyPosition() (bar, step int64) {
	if s.transport == nil {
		return 0, 0
	}
	return historyPosition(s.transport.snapshot())
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
	studioJSON(w, http.StatusOK, s.history.historySnapshot())
}

func (s *studio) undo(w http.ResponseWriter, r *http.Request) {
	s.applyHistoryRequest(w, r, "undo", 0)
}

func (s *studio) redo(w http.ResponseWriter, r *http.Request) {
	s.applyHistoryRequest(w, r, "redo", 0)
}

func (s *studio) revertHistory(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil || id == 0 {
		studioJSON(w, http.StatusBadRequest, map[string]any{"error": "history id must be a positive integer"})
		return
	}
	s.applyHistoryRequest(w, r, "revert", id)
}

func (s *studio) applyHistoryRequest(w http.ResponseWriter, r *http.Request, operation string, id uint64) {
	request, ok := studioRequest(w, r)
	if !ok {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := studioRecoveryConflict(s.path); err != nil {
		studioJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
		return
	}
	current, err := os.ReadFile(s.path)
	if err != nil {
		studioJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	bar, step := s.historyPosition()
	s.history.observe(current, bar, step)
	currentRevision := studioRevision(current)
	if currentRevision != request.Revision {
		studioJSON(w, http.StatusConflict, map[string]any{"error": "score changed on disk; reload before changing history", "revision": currentRevision})
		return
	}
	var target studioHistoryEntry
	var found bool
	switch operation {
	case "undo":
		target, found = s.history.undoEntry()
	case "redo":
		target, found = s.history.redoEntry()
	case "revert":
		target, found = s.history.entry(id)
	}
	if !found {
		studioJSON(w, http.StatusConflict, map[string]any{"error": "there is no matching history edit"})
		return
	}
	from, to := target.beforeSource, target.afterSource
	if operation == "undo" || operation == "revert" {
		from, to = target.afterSource, target.beforeSource
	}
	updated, err := studioApplySourceDiff(current, from, to)
	if err != nil {
		studioJSON(w, http.StatusConflict, map[string]any{"error": "history edit no longer matches the score on disk; nothing was changed"})
		return
	}
	request.historyFiles = cloneStudioFiles(target.files)
	if operation == "undo" || operation == "revert" {
		for i := range request.historyFiles {
			file := &request.historyFiles[i]
			file.Before, file.After = file.After, file.Before
		}
	}
	request.Action = operation
	request.Label = strings.ToUpper(operation[:1]) + operation[1:] + " · " + target.Label
	kind := studioHistoryWriteUndo
	switch operation {
	case "redo":
		kind = studioHistoryWriteRedo
	case "revert":
		kind = studioHistoryWriteRevert
	}
	s.commitSourceLocked(w, request, current, updated, nil, kind, target.ID)
}

func (s *studio) commitSourceLocked(w http.ResponseWriter, edit studioEdit, current, updated []byte, beforeSwap func(), historyKind studioHistoryWriteKind, targetID uint64) {
	outcome := s.commitMutationLocked(edit, current, studioMutation{Source: updated, Files: edit.historyFiles}, beforeSwap, historyKind, targetID, commitHook{historyError: true})
	studioJSON(w, outcome.Status, outcome.Response)
}

type commitHook struct {
	checkpoint func(takejournal.Stage) // take journal boundary; nil for ordinary edits
	envelope   *edits.Envelope         // logged after a successful write; nil logs an empty intent list
	// historyError keeps the undo, redo and revert error text for a failed
	// auxiliary write ("history file write failed"), which clients already see.
	historyError bool
	// compiled is the project already compiled from exactly the mutation's
	// source and files; the tail then skips its own compile.
	compiled *project.Project
}

type commitOutcome struct {
	Status   int
	Response map[string]any
	Written  bool
}

// commitMutationLocked is the single commit tail: compile with auxiliary
// overrides, re-check the revision, exchange the score atomically, write
// auxiliary files with rollback, record history, append the edit log.
// Callers hold s.mu and have already run studioRecoveryConflict. It never
// writes an HTTP response itself.
func (s *studio) commitMutationLocked(edit studioEdit, current []byte, mutation studioMutation, beforeSwap func(), historyKind studioHistoryWriteKind, targetID uint64, hook commitHook) commitOutcome {
	fail := func(status int, body map[string]any) commitOutcome {
		return commitOutcome{Status: status, Response: body}
	}
	updated := mutation.Source
	overrides := map[string][]byte{}
	for _, file := range mutation.Files {
		overrides[file.Path] = file.After
	}
	p := hook.compiled
	if p == nil {
		var err error
		if p, err = studioCompile(s.path, updated, overrides); err != nil {
			return fail(http.StatusUnprocessableEntity, map[string]any{"error": err.Error()})
		}
	}
	latest, err := os.ReadFile(s.path)
	if err != nil {
		return fail(http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}
	if studioRevision(latest) != edit.Revision {
		return fail(http.StatusConflict, map[string]any{"error": "score changed during validation; reload before saving"})
	}
	if bytes.Equal(current, updated) {
		s.lastGoodSource, s.lastGoodProject = bytes.Clone(current), p
		response := map[string]any{"revision": studioRevision(current), "valid": true, "source": string(current), "unchanged": true}
		for key, value := range mutation.Response {
			response[key] = value
		}
		response["playingRevision"] = studioRevision(s.lastGoodSource)
		return commitOutcome{Status: http.StatusOK, Response: response}
	}
	info, err := os.Stat(s.path)
	committed, preserved := false, ""
	if err == nil {
		committed, preserved, err = studioWriteTakeIfRevision(s.path, updated, info.Mode().Perm(), edit.Revision, beforeSwap, hook.checkpoint)
	}
	if err != nil {
		if errors.Is(err, errStudioSwapUnavailable) {
			return fail(http.StatusConflict, map[string]any{"error": err.Error()})
		}
		if preserved != "" {
			return fail(http.StatusConflict, map[string]any{"error": err.Error(), "preserved": preserved})
		}
		return fail(http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}
	if !committed {
		return fail(http.StatusConflict, map[string]any{"error": "score changed during commit; reload before saving"})
	}
	if err := studioWriteAuxiliaryFiles(mutation.Files); err != nil {
		_, _, rollbackErr := studioWriteIfRevision(s.path, current, info.Mode().Perm(), studioRevision(updated), nil)
		message := "source auxiliary write failed: " + err.Error()
		if rollbackErr != nil {
			message += "; source rollback failed: " + rollbackErr.Error()
		}
		if hook.historyError {
			message = fmt.Sprintf("history file write failed: %v; rollback: %v", err, rollbackErr)
		}
		return fail(http.StatusConflict, map[string]any{"error": message})
	}
	s.lastGoodSource, s.lastGoodProject = bytes.Clone(updated), p
	if mutation.HistoryDetail != "" {
		edit.Label = mutation.HistoryDetail
	}
	if s.history != nil {
		entry := s.history.recordSourceWrite(current, updated, studioEditLabel(edit), historyKind, targetID)
		s.history.attachFiles(entry.ID, mutation.Files)
	}
	response := map[string]any{"revision": studioRevision(updated), "valid": true, "source": string(updated), "preserved": preserved}
	for key, value := range mutation.Response {
		response[key] = value
	}
	response["playingRevision"] = studioRevision(s.lastGoodSource)
	if err := s.appendEditLog(edit, hook.envelope, current, updated); err != nil {
		response["editLogError"] = err.Error() // the score is already committed; report, do not fail
	}
	return commitOutcome{Status: http.StatusOK, Response: response, Written: true}
}

// appendEditLog records one committed edit in .cicada/edits.jsonl. envelope is
// nil for writers that are not intents yet; they log an empty intent list.
func (s *studio) appendEditLog(edit studioEdit, envelope *edits.Envelope, current, updated []byte) error {
	env := edits.Envelope{Version: edits.EnvelopeVersion, Author: edit.Author, Session: edit.Session}
	if envelope != nil {
		env = *envelope
	}
	if env.Author == "" {
		env.Author = "local"
	}
	if env.Session == "" {
		env.Session = s.sessionID
	}
	record, err := editlog.Commit(env, studioRevision(current), studioRevision(updated), studioEditLabel(edit), time.Now().UTC())
	if err == nil {
		err = editlog.AppendCommit(editlog.Path(s.path), record)
	}
	return err
}

//go:embed studio-workspace.js
var studioWorkspaceScript []byte

func (s *studio) workspaceScript(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(studioWorkspaceScript)
}

func cloneStudioFiles(files []studioAuxiliaryFile) []studioAuxiliaryFile {
	result := make([]studioAuxiliaryFile, len(files))
	for i, file := range files {
		file.Before = bytes.Clone(file.Before)
		file.After = bytes.Clone(file.After)
		result[i] = file
	}
	return result
}
func (h *studioHistory) attachFiles(id uint64, files []studioAuxiliaryFile) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := range h.edits {
		if h.edits[i].ID == id {
			h.edits[i].files = cloneStudioFiles(files)
			return
		}
	}
}
