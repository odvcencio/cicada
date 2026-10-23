package main

import (
	"errors"
	"strconv"

	"m31labs.dev/gosx/action"
)

func (s *studioApp) checkpointSource(ctx *action.Context) error {
	if err := actionValues(ctx); err != nil {
		return err
	}
	if s.notes == nil {
		return action.Error(503, "Code recovery is unavailable. Keep this window open.")
	}
	f := ctx.FormData
	id := f["recoveryId"]
	sequence, err := strconv.ParseUint(f["recoverySequence"], 10, 64)
	if err != nil || sequence == 0 || !validNoteID(id) || !validSourceFile(f["file"]) {
		return action.Validation("Invalid Code recovery receipt.", nil, nil)
	}
	owner := workspaceOwner(ctx.Request)
	if f["intent"] == "clear" {
		err = s.notes.closeSource(owner, id, f["file"], f["recoveryParentId"], f["recoveryParentHash"])
	} else {
		_, err = s.notes.saveSource(owner, savedSource{ID: id, Sequence: sequence, File: f["file"], Revision: f["fileRevision"], Source: f["content"], ParentID: f["recoveryParentId"], ParentHash: f["recoveryParentHash"]})
	}
	if errors.Is(err, errSourceCheckpoint) {
		return action.Error(409, err.Error())
	}
	if err != nil {
		return action.Error(503, "Code recovery could not save. Keep this window open: "+err.Error())
	}
	ctx.FormData = nil
	return ctx.Success("Recovery copy kept. The score file is unchanged.", map[string]any{"id": id, "sequence": sequence})
}
