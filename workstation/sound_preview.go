package main

import (
	"encoding/json"
	"errors"
	"strconv"

	"m31labs.dev/gosx/action"
)

// Sound browsing has no score mutation or navigation receipt. In particular,
// listening must leave a local Code draft and the selected placement mounted.
func (s *studioApp) soundPreviewAction(ctx *action.Context) error {
	if err := actionValues(ctx); err != nil {
		return err
	}
	f := ctx.FormData
	payload := map[string]any{"action": f["action"], "lease": f["lease"]}
	switch f["action"] {
	case "open", "state":
	case "stop", "close", "play":
		sequence, err := strconv.ParseUint(f["sequence"], 10, 64)
		if err != nil || sequence == 0 {
			return action.Validation("sound preview sequence is required", nil, f)
		}
		payload["sequence"] = sequence
		if f["action"] == "play" {
			tempo, err := integer(f, "tempo")
			if err != nil {
				return action.Validation(err.Error(), nil, f)
			}
			root, err := integer(f, "root")
			if err != nil {
				return action.Validation(err.Error(), nil, f)
			}
			payload["patch"], payload["tempo"], payload["root"], payload["scale"] = f["patch"], tempo, root, f["scale"]
		}
	default:
		return action.Validation("choose a sound preview action", nil, f)
	}
	var result json.RawMessage
	if err := s.backend.call(ctx.Request.Context(), "POST", "/api/sound-preview", payload, &result); err != nil {
		var failure *backendError
		if errors.As(err, &failure) {
			return action.Error(failure.Status, failure.Message)
		}
		return action.Error(503, "The audio service is unavailable. Your song is unchanged.")
	}
	ctx.SetResult(action.Result{OK: true, Data: result})
	return nil
}
