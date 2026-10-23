package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"m31labs.dev/cicada/workstation/control"
	"m31labs.dev/gosx"
	"m31labs.dev/gosx/action"
	"m31labs.dev/gosx/session"
)

func (s *studioApp) savedNoteState(w http.ResponseWriter, r *http.Request) {
	if s.notes == nil {
		http.NotFound(w, r)
		return
	}
	saved, err := s.notes.get(workspaceOwner(r), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.NotFound(w, r)
		} else {
			http.Error(w, "Saved note recovery could not be read; its file is retained.", 503)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(saved)
}

func newNoteID() string {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(data[:])
}

func noteDraft(saved savedNotes, owner string) draft {
	data, _ := json.Marshal(saved.Takes)
	return draft{Source: string(data), Revision: saved.Revision, SourceRevision: saved.SourceRevision, Owner: owner, Kind: "notes", SavedNoteID: saved.ID, Expires: time.Now().Add(30 * time.Minute)}
}

func (s *studioApp) cacheNoteDraft(saved savedNotes, owner string) {
	s.draftMu.Lock()
	defer s.draftMu.Unlock()
	if s.drafts == nil {
		s.drafts = map[string]draft{}
	}
	s.drafts[saved.ID] = noteDraft(saved, owner)
}

func (s *studioApp) saveNoteCheckpoint(ctx *action.Context, state string, takes []control.Recording) (savedNotes, error) {
	if s.notes == nil {
		return savedNotes{}, action.Error(503, "Note recovery is unavailable. Keep this window open; your browser copy is retained.")
	}
	if err := control.ValidateRecordings(takes); err != nil {
		return savedNotes{}, action.Validation(err.Error(), nil, nil)
	}
	id := ctx.FormData["takeId"]
	sequence, err := strconv.ParseUint(ctx.FormData["sequence"], 10, 64)
	if id == "" && state == "ready" {
		id, sequence = newNoteID(), 1
		err = nil
	}
	if err != nil {
		return savedNotes{}, action.Validation("Invalid note checkpoint sequence.", nil, nil)
	}
	saved, err := s.notes.save(workspaceOwner(ctx.Request), savedNotes{ID: id, Sequence: sequence, Revision: ctx.FormData["revision"], SourceRevision: ctx.FormData["source_revision"], State: state, Takes: takes})
	if errors.Is(err, errNoteCheckpoint) {
		return saved, action.Error(409, err.Error())
	}
	if err != nil {
		return saved, action.Error(503, "Note recovery could not save. Your browser copy is retained: "+err.Error())
	}
	return saved, nil
}

func (s *studioApp) checkpointNotes(ctx *action.Context) error {
	if err := actionValues(ctx); err != nil {
		return err
	}
	var takes []control.Recording
	if err := json.Unmarshal([]byte(ctx.FormData["recordings"]), &takes); err != nil {
		return action.Validation("Invalid note checkpoint.", nil, nil)
	}
	saved, err := s.saveNoteCheckpoint(ctx, "recording", takes)
	if err != nil {
		return err
	}
	ctx.FormData = nil
	return ctx.Success("Performance checkpoint saved.", map[string]any{"takeId": saved.ID, "sequence": saved.Sequence})
}

func (s *studioApp) restoreNotes(ctx *action.Context) error {
	if err := actionValues(ctx); err != nil {
		return err
	}
	if s.notes == nil {
		return action.Validation("Note recovery is unavailable.", nil, nil)
	}
	owner := workspaceOwner(ctx.Request)
	saved, err := s.notes.get(owner, ctx.FormData["takeId"])
	if err != nil {
		return action.Validation("This saved performance could not be opened. Its file is retained.", nil, nil)
	}
	if ctx.FormData["intent"] == "discard" {
		if err = s.notes.archive(owner, saved.ID, "discarded"); err != nil {
			return err
		}
		ctx.FormData = nil
		ctx.RedirectBackWithMessage("/?panel=live", "Saved checkpoint dismissed. The score is unchanged.")
		return nil
	}
	if saved.State != "ready" {
		// Review a copy of the last complete checkpoint. Another window may
		// still be recording the original; recovery never steals its writer.
		saved.ID, saved.Sequence, saved.State = newNoteID(), 1, "ready"
		if saved, err = s.notes.save(owner, saved); err != nil {
			return err
		}
	}
	s.cacheNoteDraft(saved, owner)
	session.Current(ctx.Request).Set("note-preview", saved.ID)
	s.checkNoteDraft(ctx, saved.ID, ctx.FormData["revision"], ctx.FormData["source_revision"], saved.Takes)
	ctx.FormData = nil
	ctx.RedirectBackWithMessage("/?panel=live", "Saved performance opened for review. The score is unchanged.")
	return nil
}

func (s *studioApp) noteRecovery(v workspace, csrf, owner, current string, history bool) gosx.Node {
	if s.notes == nil {
		return gosx.Fragment()
	}
	saved, err := s.notes.listNotes(owner, history)
	if err != nil {
		return failure(fmt.Errorf("Note recovery could not read a saved performance. Its file is retained: %w", err))
	}
	var rows []gosx.Node
	for _, take := range saved {
		if take.ID == current {
			continue
		}
		count := 0
		for _, part := range take.Takes {
			count += len(part.Notes)
		}
		state := "Ready for review"
		if take.State == "recording" {
			state = "Last recording checkpoint"
		}
		if take.State == "applied" {
			state = "Applied to patterns"
		}
		if take.State == "discarded" {
			state = "Dismissed checkpoint"
		}
		label := fmt.Sprintf("%s · %s · %s", noteCount(count), state, take.Updated.Local().Format("Jan 2, 15:04"))
		controls := []gosx.Node{hidden("takeId", take.ID), submit("intent", "review", "Review saved performance")}
		if take.State == "recording" || take.State == "ready" {
			controls = append(controls, submit("intent", "discard", "Dismiss saved checkpoint"))
		}
		rows = append(rows, gosx.El("li", gosx.Attrs(gosx.Attr("class", "strip")), gosx.El("p", gosx.Text(label)), s.form(v, csrf, "live", "note-restore", append([]gosx.Node{hidden("source_revision", v.SourceRevision)}, controls...)...)))
	}
	link := gosx.El("a", gosx.Attrs(gosx.Attr("href", "/?panel=live&noteHistory=on")), gosx.Text("Previous note performances"))
	if !history && len(rows) == 0 {
		return gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), link)
	}
	if history {
		link = gosx.El("a", gosx.Attrs(gosx.Attr("href", "/?panel=live")), gosx.Text("Back to current performances"))
		if len(rows) == 0 {
			rows = append(rows, gosx.El("li", gosx.Text("No saved note performances in this private session yet.")))
		}
	}
	return gosx.El("section", gosx.Attrs(gosx.Attr("class", "note-recovery"), gosx.Attr("aria-label", "Saved note performances")), gosx.El("h3", gosx.Text("Saved note performances")), gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Your private note performances are saved on this computer. Checkpoints and unapplied takes survive Studio restarts. Opening a checkpoint reviews a copy; it does not change your score.")), link, gosx.El("ul", gosx.Attrs(gosx.Attr("class", "take-list")), gosx.Fragment(rows...)))
}
