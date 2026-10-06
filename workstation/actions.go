package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/gosx/action"
	"m31labs.dev/gosx/session"
)

// Action handlers adapt native HTML forms to the existing revision-checked
// domain commands. They never write scores or operate audio devices themselves.
func (s *studioApp) mutation(path string, payload func(map[string]string) (any, error)) action.Handler {
	return func(ctx *action.Context) error {
		if err := actionValues(ctx); err != nil {
			return err
		}
		data, err := payload(ctx.FormData)
		if err != nil {
			return action.Validation(err.Error(), nil, ctx.FormData)
		}
		var response json.RawMessage
		if err := s.backend.call(ctx.Request.Context(), http.MethodPost, path, data, &response); err != nil {
			var failure *backendError
			if errors.As(err, &failure) {
				result := action.Error(failure.Status, failure.Message)
				if path == "/api/source" || path == "/api/files" {
					result.Result.Values = s.preserveDraft(ctx)
				} else {
					result.Result.Values = ctx.FormData
				}
				return result
			}
			if path == "/api/source" || path == "/api/files" {
				result := action.Error(http.StatusServiceUnavailable, "The audio service is unavailable. The score was not saved.")
				result.Result.Values = s.preserveDraft(ctx)
				return result
			}
			return err
		}
		if path == "/api/files" && ctx.FormData["intent"] == "check" {
			var checked struct {
				Valid       bool                  `json:"valid"`
				Diagnostics []notation.Diagnostic `json:"diagnostics"`
				Error       string                `json:"error"`
			}
			_ = json.Unmarshal(response, &checked)
			message := "File check passed."
			if !checked.Valid {
				message = checked.Error
				for _, d := range checked.Diagnostics {
					if d.Severity == "error" {
						message += fmt.Sprintf(" %d:%d %s: %s", d.Position.Line, d.Position.Column, d.Code, d.Message)
					}
				}
			}
			return action.Validation(message, nil, s.preserveDraft(ctx))
		}
		// Explicit redirect gives native and enhanced forms the same refreshed
		// projection. Never put full score text into a cookie-backed flash.
		ctx.FormData = nil
		if path == "/api/source" || path == "/api/files" {
			session.Current(ctx.Request).Delete("score-draft")
			session.Current(ctx.Request).Delete("phrase-preview")
		}
		// Transport has no source projection to refresh. Its GoSX live binding
		// updates in place, so a completed Play/Stop cannot redirect over a
		// newer panel navigation. Native forms still use redirect-after-POST.
		if path == "/api/transport" && action.WantsJSON(ctx.Request) {
			return nil
		}
		ctx.RedirectBackWithMessage("/", "")
		return nil
	}
}

// Native/managed forms supply FormData; Go/WASM engines invoke the same
// actions with JSON. GoSX keeps JSON in Payload for explicitly typed decoding.
func actionValues(ctx *action.Context) error {
	if len(ctx.Payload) == 0 {
		return nil
	}
	var values map[string]string
	if err := json.Unmarshal(ctx.Payload, &values); err != nil {
		return action.Validation("Action fields must be strings.", nil, nil)
	}
	ctx.FormData = values
	return nil
}

func integer(form map[string]string, key string) (int, error) {
	value, err := strconv.Atoi(form[key])
	if err != nil {
		return 0, fmt.Errorf("%s must be a whole number", key)
	}
	return value, nil
}

func finiteNumber(form map[string]string, key string) (float64, error) {
	n, err := strconv.ParseFloat(form[key], 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
		return 0, fmt.Errorf("%s must be a finite number", key)
	}
	return n, nil
}

func (s *studioApp) saveSource(ctx *action.Context) error {
	if ctx.FormData["intent"] == "discard" {
		session.Current(ctx.Request).Delete("score-draft")
		ctx.FormData = nil
		ctx.RedirectBackWithMessage("/?panel=code", "Draft discarded. The file is unchanged.")
		return nil
	}
	if ctx.FormData["intent"] == "merge" {
		// Use the revision displayed beside this draft. The domain service
		// rejects the write again if another editor saves in the meantime.
		ctx.FormData["fileRevision"] = ctx.FormData["diskRevision"]
	}
	path := "/api/source"
	if ctx.FormData["file"] != "" {
		path = "/api/files"
	}
	return s.mutation(path, func(f map[string]string) (any, error) {
		revision := f["fileRevision"]
		if revision == "" {
			revision = f["revision"]
		}
		payload := map[string]any{"revision": revision, "source": f["content"]}
		if path == "/api/files" {
			payload["file"] = f["file"]
			if f["intent"] == "check" {
				payload["action"] = "check"
			}
		}
		return payload, nil
	})(ctx)
}

func (s *studioApp) fileHistory(command string) action.Handler {
	return func(ctx *action.Context) error {
		if err := actionValues(ctx); err != nil {
			return err
		}
		path := "/api/" + command
		if ctx.FormData["file"] != "" {
			path = "/api/files"
		}
		return s.mutation(path, func(f map[string]string) (any, error) {
			revision := f["revision"]
			if f["file"] != "" && f["fileRevision"] != "" {
				revision = f["fileRevision"]
			}
			return map[string]any{"revision": revision, "file": f["file"], "action": command}, nil
		})(ctx)
	}
}

func (s *studioApp) actions() map[string]action.Handler {
	edit := func(f map[string]string) map[string]any { return map[string]any{"revision": f["revision"]} }
	return map[string]action.Handler{
		"transcribe":            s.transcribeMelody,
		"transcription-apply":   s.applyTranscription,
		"transcription-discard": s.discardTranscription,
		"sample-pack":           s.downloadSamplePack,
		"instrument-capture": s.mutation("/api/instrument-capture", func(f map[string]string) (any, error) {
			return map[string]any{"revision": f["revision"], "action": f["action"], "newName": f["newName"]}, nil
		}),
		"instrument-build": s.mutation("/api/instrument-build", func(f map[string]string) (any, error) {
			p := map[string]any{"revision": f["revision"], "action": f["action"], "takeId": f["takeId"], "scene": f["scene"]}
			if f["action"] == "remove" {
				i, err := integer(f, "index")
				if err != nil {
					return nil, err
				}
				p["index"] = i
			}
			return p, nil
		}),
		"instrument-fit": s.mutation("/api/instrument-fit", func(f map[string]string) (any, error) {
			hit, err := integer(f, "hit")
			if err != nil {
				return nil, err
			}
			return map[string]any{"revision": f["revision"], "sha256": f["sha256"], "hit": hit}, nil
		}),
		"instrument": s.mutation("/api/instrument", func(f map[string]string) (any, error) {
			return map[string]any{"revision": f["revision"], "action": f["action"], "pattern": f["pattern"], "newName": f["newName"], "track": f["track"], "value": f["value"]}, nil
		}),
		"automation": s.mutation("/api/automation", func(f map[string]string) (any, error) {
			return map[string]any{"revision": f["revision"], "action": f["action"], "scene": f["scene"], "path": f["path"], "value": f["value"]}, nil
		}),
		"clip": s.mutation("/api/clip", func(f map[string]string) (any, error) {
			payload := map[string]any{"revision": f["revision"], "action": f["action"], "pattern": f["pattern"], "newName": f["newName"], "scene": f["scene"], "track": f["track"]}
			if f["action"] == "clip-settings" {
				settings := map[string]any{}
				for _, name := range []string{"start", "end", "fadeIn", "fadeOut"} {
					value, err := integer(f, name)
					if err != nil {
						return nil, err
					}
					settings[name] = value
				}
				gain, err := finiteNumber(f, "gainDB")
				if err != nil {
					return nil, err
				}
				settings["gainDB"] = gain
				payload["clipSettings"] = settings
			}
			return payload, nil
		}),
		"project": s.mutation("/api/project", func(f map[string]string) (any, error) {
			tempo, err := finiteNumber(f, "tempo")
			if err != nil || tempo < 20 || tempo > 300 || math.Abs(tempo*1000-math.Round(tempo*1000)) > 1e-6 {
				return nil, fmt.Errorf("tempo must be 20–300 BPM with up to three decimal places")
			}
			return map[string]any{"revision": f["revision"], "metadata": map[string]any{"title": f["title"], "tempoMilli": int(math.Round(tempo * 1000)), "root": f["root"], "scale": f["scale"]}}, nil
		}),
		"pattern":        s.patternAction,
		"live":           s.liveAction,
		"launch":         s.launchAction,
		"note-preview":   s.previewNotes,
		"note-commit":    s.commitNotes,
		"loudness-reset": s.mutation("/api/loudness/reset", func(f map[string]string) (any, error) { return edit(f), nil }),
		"generate":       s.generatePhrase,
		"mutate":         s.mutatePhrase,
		"source":         s.saveSource,
		"toggle": s.mutation("/api/toggle", func(f map[string]string) (any, error) {
			step, err := integer(f, "step")
			if err != nil {
				return nil, err
			}
			p := edit(f)
			p["step"] = step
			p["pattern"] = f["pattern"]
			p["lane"] = f["lane"]
			if f["modifier"] != "" {
				p["modifier"] = f["modifier"]
			}
			if f["pitch"] != "" {
				pitch, err := integer(f, "pitch")
				if err != nil {
					return nil, err
				}
				p["pitch"] = pitch
			}
			return p, nil
		}),
		"transport": s.mutation("/api/transport", func(f map[string]string) (any, error) {
			p := edit(f)
			p["action"] = f["action"]
			p["scene"] = f["scene"]
			p["track"] = f["track"]
			p["pattern"] = f["pattern"]
			if f["entry"] != "" {
				v, e := integer(f, "entry")
				if e != nil {
					return nil, e
				}
				p["entry"] = v
			}
			if f["quantize"] != "" {
				v, e := integer(f, "quantize")
				if e != nil {
					return nil, e
				}
				p["quantize"] = v
			}
			return p, nil
		}),
		"song": s.mutation("/api/song", func(f map[string]string) (any, error) {
			p := edit(f)
			p["action"] = f["action"]
			p["scene"] = f["scene"]
			for _, key := range []string{"index", "target", "bars"} {
				if f[key] != "" {
					v, e := integer(f, key)
					if e != nil {
						return nil, e
					}
					p[key] = v
				}
			}
			return p, nil
		}),
		"mixer": s.mutation("/api/mixer", func(f map[string]string) (any, error) {
			p := edit(f)
			p["confirmUpgrade"] = f["confirmUpgrade"] == "on"
			p["path"] = f["path"]
			var value any
			switch f["valueType"] {
			case "number":
				v, e := finiteNumber(f, "value")
				if e != nil {
					return nil, fmt.Errorf("value must be a number")
				}
				value = v
			case "bool":
				v, e := strconv.ParseBool(f["value"])
				if e != nil {
					return nil, fmt.Errorf("choose true or false")
				}
				value = v
			default:
				value = f["value"]
			}
			p["value"] = value
			return p, nil
		}),
		"take": s.mutation("/api/takes", func(f map[string]string) (any, error) {
			p := edit(f)
			p["action"] = f["action"]
			p["track"] = f["track"]
			p["scene"] = f["scene"]
			p["takeId"] = f["takeId"]
			return p, nil
		}),
		"undo": s.fileHistory("undo"),
		"redo": s.fileHistory("redo"),
		"revert": func(ctx *action.Context) error {
			id, err := strconv.ParseUint(ctx.FormData["id"], 10, 64)
			if err != nil || id == 0 {
				return action.Validation("Choose a history entry.", nil, nil)
			}
			return s.mutation(fmt.Sprintf("/api/history/%d/revert", id), func(f map[string]string) (any, error) { return edit(f), nil })(ctx)
		},
		"audio": s.mutation("/api/audio/config", func(f map[string]string) (any, error) {
			gain, e := finiteNumber(f, "monitorGain")
			if e != nil {
				return nil, fmt.Errorf("monitor gain must be a number")
			}
			return audioOptions{InputDevice: f["inputDevice"], OutputDevice: f["outputDevice"], InputEnabled: f["inputEnabled"] == "on", MonitorMuted: f["monitorMuted"] == "on", MonitorGain: gain, MonitorMode: f["monitorMode"]}, nil
		}),
		"export": s.mutation("/api/export", func(f map[string]string) (any, error) {
			p := map[string]any{"revision": f["revision"]}
			for _, key := range []string{"target_lufs", "true_peak_max", "tolerance"} {
				v, e := finiteNumber(f, key)
				if e != nil {
					return nil, fmt.Errorf("%s must be a number", key)
				}
				p[key] = v
			}
			for _, key := range []string{"rate", "bits"} {
				v, e := integer(f, key)
				if e != nil {
					return nil, e
				}
				p[key] = v
			}
			return p, nil
		}),
	}
}
