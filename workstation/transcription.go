package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"

	"m31labs.dev/cicada/host/audition"
	"m31labs.dev/cicada/host/transcription"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/workstation/ui"
	"m31labs.dev/gosx"
	"m31labs.dev/gosx/action"
	"m31labs.dev/gosx/server"
	"m31labs.dev/gosx/session"
)

const workstationTranscribeMaxBytes = (2 << 20) - (64 << 10)

func (s *studioApp) transcription(ctx *server.Context, v workspace, csrf string) gosx.Node {
	var takes struct {
		Takes []struct {
			ID         string `json:"id"`
			Frames     uint64 `json:"frames"`
			Incomplete bool   `json:"incomplete"`
		} `json:"takes"`
	}
	_ = s.backend.call(ctx.Request.Context(), http.MethodGet, "/api/takes", nil, &takes)
	choices := []gosx.Node{gosx.El("option", gosx.Attrs(gosx.Attr("value", "")), gosx.Text("Upload a WAV file"))}
	for _, take := range takes.Takes {
		if take.Frames > 0 && !take.Incomplete {
			choices = append(choices, gosx.El("option", gosx.Attrs(gosx.Attr("value", take.ID)), gosx.Text(take.ID)))
		}
	}
	form := gosx.El("form", gosx.Attrs(gosx.Attr("method", "post"), gosx.Attr("action", "/__actions/transcribe"), gosx.Attr("enctype", "multipart/form-data"), gosx.Attr("class", "generator-form action-form")),
		hidden("csrf_token", csrf), hidden("revision", v.Revision), hidden(action.ReturnTargetField, "/?panel=takes"),
		field("Recording", gosx.El("select", gosx.Attrs(gosx.Attr("name", "takeId")), gosx.Fragment(choices...))),
		field("Single melody WAV", gosx.El("input", gosx.Attrs(gosx.Attr("type", "file"), gosx.Attr("name", "wav"), gosx.Attr("accept", ".wav,audio/wav")))),
		field("Tempo (blank = automatic)", numberInput("tempo", "", "20", "300")),
		field("Key (blank = automatic)", gosx.El("input", gosx.Attrs(gosx.Attr("name", "key"), gosx.Attr("placeholder", "C major")))),
		field("Note grid", gosx.El("select", gosx.Attrs(gosx.Attr("name", "grid")),
			gosx.El("option", gosx.Attrs(gosx.Attr("value", "4")), gosx.Text("1/4")),
			gosx.El("option", gosx.Attrs(gosx.Attr("value", "8")), gosx.Text("1/8")),
			gosx.El("option", gosx.Attrs(gosx.Attr("value", "16"), gosx.BoolAttr("selected")), gosx.Text("1/16")))), submit("", "", "Transcribe melody"))
	var preview gosx.Node = gosx.Fragment()
	if d, ok := s.draft(session.Current(ctx.Request).String("transcription-preview"), workspaceOwner(ctx.Request)); ok && d.Kind == "transcription" {
		var result transcription.Result
		if json.Unmarshal([]byte(d.Source), &result) == nil {
			confidence := 0.0
			for _, note := range result.Notes {
				confidence += note.Confidence
			}
			if len(result.Notes) > 0 {
				confidence /= float64(len(result.Notes))
			}
			var warnings []gosx.Node
			for _, warning := range result.Warnings {
				warnings = append(warnings, gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text(warning)))
			}
			preview = gosx.El("section", gosx.Attrs(gosx.Attr("class", "strip"), gosx.Attr("id", "transcription-preview")), gosx.El("h3", gosx.Text("Melody preview")),
				gosx.El("p", gosx.Text(fmt.Sprintf("%d notes · %.1f BPM (%d%% confidence) · %s (%d%%) · %s (%d%%) · pitch confidence %d%%", len(result.Notes), result.Tempo, int(result.TempoConfidence*100), result.Meter, int(result.MeterConfidence*100), result.Key, int(result.KeyConfidence*100), int(confidence*100)))),
				gosx.El("p", gosx.Text(fmt.Sprintf("The note grid moved boundaries by %.1f ms on average.", result.QuantizationMS))),
				gosx.El("label", gosx.Attrs(gosx.Attr("for", "transcription-audio")), gosx.Text("Generated score preview · up to four bars")),
				gosx.El("audio", gosx.Attrs(gosx.Attr("id", "transcription-audio"), gosx.BoolAttr("controls"), gosx.Attr("preload", "none"), gosx.Attr("src", "/media/transcription"))),
				gosx.Fragment(warnings...), gosx.El("details", gosx.Attrs(gosx.BoolAttr("open")), gosx.El("summary", gosx.Text("Generated score")), gosx.El("pre", gosx.Attrs(gosx.Attr("class", "history-diff")), gosx.Text(result.Source))),
				gosx.El("p", gosx.Text("Replace score replaces the entire current score. Undo restores it. Previewing leaves the file unchanged.")),
				s.form(v, csrf, "takes", "transcription-apply", submit("", "", "Replace score")),
				s.form(v, csrf, "takes", "transcription-discard", submit("", "", "Discard preview")))
		}
	}
	return ui.Panel(ui.PanelProps{ID: "transcription", Title: "Recording to score", Description: "Sing, hum or play a solo melody. Transcribe a completed take, or upload a WAV up to 1.9 MiB. Completed takes may be up to 60 seconds. Audio is analyzed locally."}, form, preview)
}

func (s *studioApp) transcriptionAudio(w http.ResponseWriter, r *http.Request) {
	d, ok := s.draft(session.Current(r).String("transcription-preview"), workspaceOwner(r))
	var result transcription.Result
	if !ok || d.Kind != "transcription" || json.Unmarshal([]byte(d.Source), &result) != nil {
		http.Error(w, "transcribe a recording to preview its score", http.StatusNotFound)
		return
	}
	if r.Context().Err() != nil {
		return
	}
	score, _ := notation.Parse([]byte(result.Source))
	if score == nil {
		http.Error(w, "cannot render this score preview", http.StatusUnprocessableEntity)
		return
	}
	p, _ := project.FromScore(score)
	events, err := project.CompileSchedule(p)
	if err != nil {
		http.Error(w, "cannot render this score preview", http.StatusUnprocessableEntity)
		return
	}
	var duration int64
	for _, event := range events {
		duration = max(duration, event.Tick, event.EndTick)
	}
	bars := min(4, int((duration+seq.TicksPerBar-1)/seq.TicksPerBar))
	audio, err := audition.Render([]byte(result.Source), 44100, bars, .2)
	if err != nil {
		http.Error(w, "cannot render this score preview", http.StatusUnprocessableEntity)
		return
	}
	if r.Context().Err() != nil {
		return
	}
	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_ = audio.WriteWAV(w)
}

func (s *studioApp) transcribeMelody(ctx *action.Context) error {
	if err := actionValues(ctx); err != nil {
		return err
	}
	if ctx.Request.MultipartForm != nil {
		defer ctx.Request.MultipartForm.RemoveAll()
	}
	options := transcription.DefaultOptions()
	var err error
	if ctx.FormData["tempo"] != "" {
		options.Tempo, err = strconv.ParseFloat(ctx.FormData["tempo"], 64)
		if err != nil {
			return action.Validation("Tempo must be a number or left blank.", nil, nil)
		}
	}
	options.Key = ctx.FormData["key"]
	options.Grid, err = strconv.Atoi(ctx.FormData["grid"])
	if err != nil {
		return action.Validation("Choose a quarter, eighth or sixteenth note grid.", nil, nil)
	}
	var result transcription.Result
	if id := ctx.FormData["takeId"]; id != "" {
		err = s.backend.call(ctx.Request.Context(), http.MethodPost, "/api/transcribe", map[string]any{"takeId": id, "options": options}, &result)
	} else {
		files := ctx.Files("wav")
		if len(files) != 1 || files[0].Size > workstationTranscribeMaxBytes {
			return action.Validation("Choose one WAV file up to 1.9 MiB, or choose a completed recording.", nil, nil)
		}
		file, openErr := files[0].Open()
		if openErr != nil {
			return action.Validation("Cannot open recording. Choose the file again.", nil, nil)
		}
		data, readErr := io.ReadAll(io.LimitReader(file, workstationTranscribeMaxBytes+1))
		_ = file.Close()
		if readErr != nil || len(data) > workstationTranscribeMaxBytes {
			return action.Validation("Choose a WAV file up to 1.9 MiB.", nil, nil)
		}
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		for key, value := range map[string]string{"tempo": ctx.FormData["tempo"], "key": options.Key, "grid": strconv.Itoa(options.Grid)} {
			if err := writer.WriteField(key, value); err != nil {
				return err
			}
		}
		part, err := writer.CreateFormFile("wav", "melody.wav")
		if err != nil {
			return err
		}
		_, _ = part.Write(data)
		_ = writer.Close()
		u := *s.backend.url
		u.Path = "/api/transcribe"
		req, err := http.NewRequestWithContext(ctx.Request.Context(), http.MethodPost, u.String(), &body)
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", writer.FormDataContentType())
		if s.backend.token != "" {
			req.Header.Set("Authorization", "Bearer "+s.backend.token)
		}
		response, err := s.backend.client.Do(req)
		if err != nil {
			return action.Error(http.StatusServiceUnavailable, "Cannot reach the audio service. The score was not changed.")
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			var failure struct {
				Error string `json:"error"`
			}
			_ = json.NewDecoder(io.LimitReader(response.Body, 16<<10)).Decode(&failure)
			return action.Error(response.StatusCode, failure.Error)
		}
		err = json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&result)
	}
	if err != nil {
		return action.Validation(err.Error(), nil, nil)
	}
	data, err := json.Marshal(result)
	if err != nil {
		return err
	}
	id, err := s.storeDraft(string(data), ctx.FormData["revision"], workspaceOwner(ctx.Request), "transcription")
	if err != nil {
		return err
	}
	session.Current(ctx.Request).Set("transcription-preview", id)
	ctx.FormData = nil
	ctx.RedirectBackWithMessage("/?panel=takes", "Melody transcribed. Review the score before replacing it.")
	return nil
}

func (s *studioApp) applyTranscription(ctx *action.Context) error {
	if err := actionValues(ctx); err != nil {
		return err
	}
	d, ok := s.draft(session.Current(ctx.Request).String("transcription-preview"), workspaceOwner(ctx.Request))
	var result transcription.Result
	if !ok || d.Kind != "transcription" || json.Unmarshal([]byte(d.Source), &result) != nil {
		return action.Validation("The preview expired. Transcribe the recording again.", nil, nil)
	}
	if session.Current(ctx.Request).String("score-draft") != "" {
		return action.Validation("Save or discard the score draft before replacing the score.", nil, nil)
	}
	var response json.RawMessage
	if err := s.backend.call(ctx.Request.Context(), http.MethodPost, "/api/source", map[string]string{"revision": d.Revision, "source": result.Source}, &response); err != nil {
		var failure *backendError
		if errors.As(err, &failure) {
			return action.Error(failure.Status, failure.Message)
		}
		return action.Error(http.StatusServiceUnavailable, "Cannot reach the audio service. The preview is retained and the score was not changed.")
	}
	session.Current(ctx.Request).Delete("transcription-preview")
	session.Current(ctx.Request).Delete("phrase-preview")
	ctx.FormData = nil
	ctx.RedirectBackWithMessage("/?panel=takes", "Score replaced. Undo restores the previous score.")
	return nil
}

func (s *studioApp) discardTranscription(ctx *action.Context) error {
	session.Current(ctx.Request).Delete("transcription-preview")
	ctx.FormData = nil
	ctx.RedirectBackWithMessage("/?panel=takes", "Melody preview discarded.")
	return nil
}
