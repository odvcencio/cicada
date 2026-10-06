package main

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"

	"m31labs.dev/cicada/host/recording"
	"m31labs.dev/cicada/workstation/ui"
	"m31labs.dev/gosx"
	"m31labs.dev/gosx/server"
)

type recordedState struct {
	Preview          *recording.Pack `json:"preview"`
	Recording        bool            `json:"recording"`
	Sampled, Modeled string
}

func (s *studioApp) recorded(ctx *server.Context, v workspace, csrf string) gosx.Node {
	var state recordedState
	if err := s.backend.call(ctx.Request.Context(), http.MethodGet, "/api/instrument-state", nil, &state); err != nil {
		return failure(err)
	}
	label := "Record microphone"
	command := "start"
	if state.Recording {
		label = "Stop and review hits"
		command = "stop"
	}
	capture := s.form(v, csrf, "instruments", "instrument-capture", hidden("action", command), field("Instrument name", textInput("newName", "recorded")), submit("", "", label))
	upload := gosx.El("form", gosx.Attrs(gosx.Attr("action", "/__instrument/import"), gosx.Attr("method", "post"), gosx.Attr("enctype", "multipart/form-data"), gosx.Attr("class", "action-form")), hidden("csrf_token", csrf), hidden("revision", v.Revision), field("Instrument name", textInput("name", "recorded")), field("Import WAV recordings", gosx.El("input", gosx.Attrs(gosx.Attr("type", "file"), gosx.Attr("name", "wav"), gosx.Attr("accept", "audio/wav,.wav"), gosx.BoolAttr("multiple"), gosx.BoolAttr("required")))), submit("", "", "Review imported hits"))
	var review []gosx.Node
	if p := state.Preview; p != nil {
		var hits []gosx.Node
		review = append(review, gosx.El("p", gosx.Attrs(gosx.Attr("role", "status")), gosx.Text(fmt.Sprintf("%d hits kept. Play each hit and remove unwanted sounds before building.", len(p.Hits)))))
		for i, h := range p.Hits {
			media := "/media/instrument-hit?sha256=" + url.QueryEscape(p.Pin) + "&hit=" + strconv.Itoa(i+1)
			hits = append(hits, gosx.El("li", gosx.Attrs(gosx.Attr("class", "arrangement-block")), gosx.El("p", gosx.Text(fmt.Sprintf("Hit %d · %.3f s · %.1f dB", i+1, float64(h.End-h.Start)/float64(h.Rate), h.LoudnessDB))), gosx.El("audio", gosx.Attrs(gosx.BoolAttr("controls"), gosx.Attr("preload", "none"), gosx.Attr("src", media), gosx.Attr("aria-label", fmt.Sprintf("Play hit %d", i+1)))), s.form(v, csrf, "instruments", "instrument-build", hidden("action", "remove"), hidden("takeId", p.Pin), hidden("index", strconv.Itoa(i)), submit("", "", "Remove hit"))))
		}
		scenes := []string{}
		if v.Project != nil {
			for _, scene := range v.Project.Scenes {
				scenes = append(scenes, scene.ID)
			}
		}
		review = append(review, gosx.El("ul", gosx.Fragment(hits...)), s.form(v, csrf, "instruments", "instrument-build", hidden("takeId", p.Pin), field("Current scene", selectInput("scene", "", scenes)), submit("", "", "Build kept hits")))
	}
	var instruments []gosx.Node
	if state.Sampled != "" {
		instruments = append(instruments, gosx.El("p", gosx.Text("Sampled selected. The sampled pack is saved and plays from its project track.")), recordedAudio(state.Sampled, "Play sampled instrument"), s.form(v, csrf, "instruments", "instrument-fit", hidden("sha256", state.Sampled), field("Hit to approximate", numberInput("hit", "1", "1", "256")), submit("", "", "Fit model (approximation)")))
	}
	if state.Modeled != "" {
		instruments = append(instruments, gosx.El("p", gosx.Text("Model approximation · saved as a separate muted track. Samples remain the default sound for taps, clicks and knocks.")), recordedAudio(state.Modeled, "Play model approximation"))
	}
	return ui.Panel(ui.PanelProps{ID: "recorded-instruments", Title: "Record an instrument", Description: "Enable input in Audio. Record several clear hits from soft to loud; the noise gate rejects quiet room sounds and repeated onsets within 120 ms."}, capture, upload, gosx.Fragment(review...), gosx.Fragment(instruments...))
}
func recordedAudio(pin, label string) gosx.Node {
	return gosx.El("audio", gosx.Attrs(gosx.BoolAttr("controls"), gosx.Attr("preload", "none"), gosx.Attr("aria-label", label), gosx.Attr("src", "/media/instrument?sha256="+url.QueryEscape(pin))))
}

func (s *studioApp) importInstrument(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		http.Error(w, "choose WAV files totalling at most 64 MiB", 400)
		return
	}
	defer r.MultipartForm.RemoveAll()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	for key, value := range map[string]string{"name": r.FormValue("name"), "action": "analyze", "revision": r.FormValue("revision")} {
		if err := form.WriteField(key, value); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	files := r.MultipartForm.File["wav"]
	if len(files) < 1 || len(files) > 32 {
		http.Error(w, "choose 1–32 WAV files", 400)
		return
	}
	var total int64
	for _, file := range files {
		total += file.Size
		if total > recording.MaxInputBytes {
			http.Error(w, "recordings exceed 64 MiB", 400)
			return
		}
		input, err := file.Open()
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		dst, err := form.CreateFormFile("wav", "recording.wav")
		if err == nil {
			_, err = io.Copy(dst, io.LimitReader(input, file.Size+1))
		}
		input.Close()
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
	}
	form.Close()
	endpoint := *s.backend.url
	endpoint.Path = "/api/instrument-record"
	request, err := http.NewRequestWithContext(r.Context(), http.MethodPost, endpoint.String(), &body)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	request.Header.Set("Content-Type", form.FormDataContentType())
	request.Header.Set("Authorization", "Bearer "+s.backend.token)
	response, err := s.backend.client.Do(request)
	if err != nil {
		http.Error(w, err.Error(), 503)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		w.WriteHeader(response.StatusCode)
		_, _ = io.Copy(w, io.LimitReader(response.Body, 16<<10))
		return
	}
	http.Redirect(w, r, "/?panel=instruments", http.StatusSeeOther)
}
func (s *studioApp) instrumentMedia(w http.ResponseWriter, r *http.Request) {
	path := "/api/instrument-hit?" + r.URL.RawQuery
	method := http.MethodGet
	var input any
	if r.URL.Path == "/media/instrument" {
		path = "/api/instrument-audition"
		method = http.MethodPost
		input = map[string]any{"sha256": r.URL.Query().Get("sha256"), "note": 60, "velocity": 100, "cycle": 0}
	}
	response, err := s.backend.request(r.Context(), method, path, input)
	if err != nil {
		http.Error(w, err.Error(), 503)
		return
	}
	defer response.Body.Close()
	w.Header().Set("Content-Type", response.Header.Get("Content-Type"))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(response.Body, 64<<20))
}
