package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/workstation/control"
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
			values := s.preserveDraft(ctx)
			if checked.Valid {
				ctx.SetResult(action.Result{OK: true, Message: "File check passed. Draft is not saved.", Values: values})
				return nil
			}
			message := checked.Error
			for _, d := range checked.Diagnostics {
				if d.Severity == "error" {
					message += fmt.Sprintf(" %d:%d %s: %s", d.Position.Line, d.Position.Column, d.Code, d.Message)
				}
			}
			return action.Validation(message, nil, values)
		}
		// Explicit redirect gives native and enhanced forms the same refreshed
		// projection. Never put full score text into a cookie-backed flash.
		if path == "/api/takes" && ctx.FormData["timeline"] == "on" && ctx.FormData["takeId"] != "" {
			// Keep an auditioned phrase selected when the refreshed projection
			// arrives, so Hear in song -> Use this range uses the same interval.
			session.Current(ctx.Request).Set("vocal-range", map[string]string{"takeId": ctx.FormData["takeId"], "start": ctx.FormData["recordStart"], "end": ctx.FormData["recordEnd"]})
			if command := ctx.FormData["action"]; command == "listen" || command == "listen-loop" {
				session.Current(ctx.Request).Set("vocal-audition", map[string]string{"revision": ctx.FormData["revision"], "takeId": ctx.FormData["takeId"], "start": ctx.FormData["recordStart"], "end": ctx.FormData["recordEnd"], "gainDB": ctx.FormData["gainDB"], "auditionMode": ctx.FormData["auditionMode"], "offsetMS": ctx.FormData["offsetMS"], "fadeMS": ctx.FormData["fadeMS"], "crossfadeMS": ctx.FormData["crossfadeMS"]})
			} else {
				session.Current(ctx.Request).Delete("vocal-audition")
			}
		}
		if path == "/api/undo" || path == "/api/redo" || path == "/api/source" {
			session.Current(ctx.Request).Delete("vocal-audition")
		}
		if path == "/api/source" || path == "/api/files" {
			if err := s.clearSourceDraft(ctx.Request, ctx.FormData["file"], ctx.FormData); err != nil {
				return action.Error(503, "The file was saved, but its recovery copy could not be retired. The copy is retained: "+err.Error())
			}
			session.Current(ctx.Request).Delete("phrase-preview")
		}
		ctx.FormData = nil
		// Transport and export update their live bindings without changing the
		// score. Acknowledgement must not navigate away from their controls or
		// replace unrelated drafts. Native forms retain redirect-after-POST.
		if (path == "/api/transport" || path == "/api/export" || path == "/api/export/midi") && action.WantsJSON(ctx.Request) {
			if path == "/api/export" {
				ctx.SetResult(action.Result{OK: true, Message: "Rendering started."})
			}
			if path == "/api/export/midi" {
				ctx.SetResult(action.Result{OK: true, Message: "MIDI is ready to download."})
			}
			return nil
		}
		ctx.RedirectBackWithMessage(shellRedirect(ctx.Request, "/"), "")
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
	if err := actionValues(ctx); err != nil {
		return err
	}
	// File selection and intent routing use the same fields for HTML forms
	// and JSON callers. Do not decode again after choosing the merge revision.
	ctx.Payload = nil
	if ctx.FormData["intent"] == "discard" {
		if err := s.clearSourceDraft(ctx.Request, ctx.FormData["file"], ctx.FormData); err != nil {
			return action.Error(503, "The recovery copy could not be discarded. It is retained: "+err.Error())
		}
		ctx.FormData = nil
		ctx.RedirectBackWithMessage(shellRedirect(ctx.Request, "/?panel=code"), "Draft discarded. The file is unchanged.")
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
		"source-checkpoint":     s.checkpointSource,
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
			if f["action"] == "automation-point-set" || f["action"] == "automation-point-remove" {
				curve := 0.0
				if f["shape"] == "curve" {
					var err error
					curve, err = finiteNumber(f, "curve")
					if err != nil || curve < -8 || curve > 8 {
						return nil, fmt.Errorf("curve tension must be −8 to +8")
					}
				}
				return map[string]any{"revision": f["revision"], "source_revision": f["source_revision"], "action": f["action"], "path": f["path"], "value": map[string]any{"oldAt": f["oldAt"], "at": f["at"], "value": f["value"], "shape": f["shape"], "curve": curve}}, nil
			}
			return map[string]any{"revision": f["revision"], "source_revision": f["source_revision"], "action": f["action"], "scene": f["scene"], "path": f["path"], "value": f["value"]}, nil
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
		"pattern":         s.patternAction,
		"sound-preview":   s.soundPreviewAction,
		"sampler":         s.samplerAction,
		"live":            s.liveAction,
		"launch":          s.launchAction,
		"note-preview":    s.previewNotes,
		"note-commit":     s.commitNotes,
		"note-checkpoint": s.checkpointNotes,
		"note-restore":    s.restoreNotes,
		"loudness-reset":  s.mutation("/api/loudness/reset", func(f map[string]string) (any, error) { return edit(f), nil }),
		"generate":        s.generatePhrase,
		"mutate":          s.mutatePhrase,
		"source":          s.saveSource,
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
			for _, name := range []string{"placement", "section", "start", "end", "mode"} {
				if f[name] != "" {
					p[name] = f[name]
				}
			}
			if f["loop"] != "" {
				value, err := strconv.ParseBool(f["loop"])
				if err != nil {
					return nil, fmt.Errorf("loop must be true or false")
				}
				p["loop"] = value
			}
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
		"arrange": s.mutation("/api/arrange", func(f map[string]string) (any, error) {
			return map[string]any{
				"revision": f["revision"], "source_revision": f["source_revision"], "action": f["action"], "name": f["name"], "newName": f["newName"],
				"placement": map[string]string{"at": f["at"], "length": f["length"], "track": f["track"], "content": f["content"]},
			}, nil
		}),
		"mixer": s.mutation("/api/mixer", func(f map[string]string) (any, error) {
			p := edit(f)
			if f["action"] == "vocal-chain" {
				p["action"], p["track"], p["confirmUpgrade"] = "vocal-chain", f["track"], f["confirmUpgrade"] == "on"
				return p, nil
			}
			if f["action"] == "chain-edit" {
				parts := strings.Fields(f["chain"])
				index, err := integer(f, "index")
				if err != nil {
					return nil, err
				}
				switch f["operation"] {
				case "up":
					if index <= 0 || index >= len(parts) {
						return nil, fmt.Errorf("choose a stage after the first")
					}
					parts[index-1], parts[index] = parts[index], parts[index-1]
				case "down":
					if index < 0 || index >= len(parts)-1 {
						return nil, fmt.Errorf("choose a stage before the last")
					}
					parts[index+1], parts[index] = parts[index], parts[index+1]
				case "remove":
					if index < 0 || index >= len(parts) {
						return nil, fmt.Errorf("choose an effect stage")
					}
					parts = append(parts[:index], parts[index+1:]...)
				case "add":
					if f["effect"] == "" {
						return nil, fmt.Errorf("choose an effect")
					}
					parts = append(parts, f["effect"])
				default:
					return nil, fmt.Errorf("choose an effect chain operation")
				}
				value := strings.Join(parts, " -> ")
				if value == "" {
					value = "none"
				}
				p["path"], p["value"] = f["track"]+".insert", value
				return p, nil
			}
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
			p["source_revision"] = f["source_revision"]
			p["action"] = f["action"]
			p["track"] = f["track"]
			p["scene"] = f["scene"]
			p["takeId"] = f["takeId"]
			if f["timeline"] == "on" {
				settings := map[string]any{"start": f["recordStart"], "end": f["recordEnd"]}
				if f["passes"] != "" {
					n, err := integer(f, "passes")
					if err != nil || n < 1 || n > 8 {
						return nil, fmt.Errorf("choose 1 to 8 recording passes")
					}
					settings["passes"] = n
				}
				for _, name := range []string{"offsetMS", "fadeMS", "crossfadeMS"} {
					if f[name] != "" {
						n, err := finiteNumber(f, name)
						lo, hi := 0., 100.
						if name == "offsetMS" {
							lo, hi = -500, 500
						}
						if err != nil || n < lo || n > hi {
							return nil, fmt.Errorf("%s must be %g to %g ms", name, lo, hi)
						}
						settings[name] = n
					}
				}
				if f["gainDB"] != "" {
					gain, err := finiteNumber(f, "gainDB")
					if err != nil || !control.ValidVocalGain(gain) {
						return nil, fmt.Errorf("phrase gain must be −24 to +18 dB")
					}
					settings["gainDB"] = gain
				}
				if mode := f["auditionMode"]; mode != "" {
					if mode != "solo" && mode != "context" {
						return nil, fmt.Errorf("choose Song or Vocal only for auditioning")
					}
					settings["auditionMode"] = mode
				}
				p["recording"] = settings
			}
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
			period := 256
			if f["periodFrames"] != "" {
				var err error
				period, err = integer(f, "periodFrames")
				if err != nil {
					return nil, fmt.Errorf("choose an audio buffer in frames")
				}
			}
			switch period {
			case 64, 128, 256, 512, 1024:
			default:
				return nil, fmt.Errorf("choose a 64, 128, 256, 512, or 1024-frame audio buffer")
			}
			gain, e := finiteNumber(f, "monitorGain")
			if e != nil {
				return nil, fmt.Errorf("monitor gain must be a number")
			}
			offset := 0.
			if f["recordingOffsetMS"] != "" {
				var err error
				offset, err = finiteNumber(f, "recordingOffsetMS")
				if err != nil || offset < -500 || offset > 500 {
					return nil, fmt.Errorf("recording timing correction must be −500 to +500 ms")
				}
			}
			return audioOptions{InputDevice: f["inputDevice"], OutputDevice: f["outputDevice"], InputEnabled: f["inputEnabled"] == "on", MonitorMuted: f["monitorMuted"] == "on", MonitorGain: gain, MonitorMode: f["monitorMode"], RecordingOffsetMS: offset, PeriodFrames: period}, nil
		}),
		"export-midi": s.mutation("/api/export/midi", func(f map[string]string) (any, error) {
			if f["source_revision"] == "" {
				return nil, fmt.Errorf("Refresh Studio to load the saved song before exporting notes.")
			}
			return map[string]string{"revision": f["revision"], "source_revision": f["source_revision"]}, nil
		}),
		"export": s.mutation("/api/export", func(f map[string]string) (any, error) {
			if f["source_revision"] == "" {
				return nil, fmt.Errorf("Refresh Studio to load the saved song before rendering.")
			}
			p := map[string]any{"revision": f["revision"], "source_revision": f["source_revision"]}
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
