package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
)

type exportView struct {
	ID                    string `json:"id"`
	Revision              string `json:"revision"`
	SourceRevision        string `json:"source_revision"`
	CurrentSourceRevision string `json:"current_source_revision"`
	State                 string `json:"state"`
	Path                  string `json:"path,omitempty"`
	FileName              string `json:"fileName"`
	Error                 string `json:"error"`
	Pass                  int    `json:"pass"`
	PassLimit             int    `json:"pass_limit"`
	Report                *struct {
		Target   float64 `json:"target_lufs"`
		Achieved float64 `json:"achieved_lufs"`
		TruePeak float64 `json:"true_peak_dbtp"`
		Gain     float64 `json:"applied_gain_db"`
		Limiter  float64 `json:"largest_limiter_reduction_db"`
	} `json:"report"`
	Busy           bool   `json:"busy"`
	DownloadHidden bool   `json:"downloadHidden"`
	DownloadURL    string `json:"downloadURL"`
	DownloadLabel  string `json:"downloadLabel"`
	Quality        string `json:"quality"`
	Progress       string `json:"progress"`
	Version        string `json:"version"`
}

func (v *exportView) project() {
	// The private render service needs a local path, but the browser only
	// needs the delivered file name. Do not expose workstation directories.
	v.FileName = ""
	if v.Path != "" {
		v.FileName = path.Base(strings.ReplaceAll(v.Path, "\\", "/"))
		v.Path = ""
	}
	v.Busy = v.State == "queued" || v.State == "rendering"
	v.DownloadHidden = v.ID == "" || v.State != "succeeded" && v.State != "shortfall"
	v.DownloadLabel, v.DownloadURL, v.Progress = "Download WAV", "", v.State
	if v.Busy && v.Pass > 0 {
		v.Progress = fmt.Sprintf("Rendering · pass %d of %d", v.Pass, v.PassLimit)
	}
	switch v.State {
	case "", "idle":
		v.Progress = "Ready to render"
	case "queued":
		v.Progress = "Render queued"
	case "rendering":
		if v.Pass == 0 {
			v.Progress = "Rendering WAV"
		}
	case "succeeded":
		v.Progress = "Render complete · WAV ready"
	case "shortfall":
		v.Progress = "Render complete · loudness target not reached"
	case "failed":
		v.Progress = "Render failed"
	}
	if !v.DownloadHidden {
		v.DownloadURL = "/media/exports/" + url.PathEscape(v.ID) + ".wav"
	}
	v.Quality = "The completed render will show its measured loudness and true peak here."
	if r := v.Report; r != nil {
		v.Quality = fmt.Sprintf("%.1f LUFS (target %.1f) · %.1f dBTP · gain %+.1f dB · maximum limiting %.1f dB", r.Achieved, r.Target, r.TruePeak, r.Gain, r.Limiter)
	}
	if v.State == "shortfall" {
		v.DownloadLabel = "Download shortfall WAV"
		v.Quality = "Loudness target was not reached. Inspect the shortfall render before delivery. " + v.Quality
	}
	v.Version = ""
	if v.ID != "" {
		switch {
		case v.SourceRevision == "" || v.CurrentSourceRevision == "":
			v.Version = "This render's saved score version cannot be checked."
			v.DownloadLabel += " (version unverified)"
		case v.SourceRevision != v.CurrentSourceRevision:
			v.Version = "The score has changed since this render. Render again to include your latest saved edits."
			if v.Busy {
				v.Version = "Rendering an earlier saved version. Render again after this job finishes to include your latest edits."
			}
			v.DownloadLabel = "Download earlier render"
			if v.State == "shortfall" {
				v.DownloadLabel = "Download earlier shortfall WAV"
			}
		case v.Busy:
			v.Version = "Rendering the saved score."
		default:
			v.Version = "This render matches the saved score."
		}
	}
}

func (s *studioApp) exportStatus(w http.ResponseWriter, r *http.Request) {
	var status exportView
	if err := s.backend.call(r.Context(), http.MethodGet, "/api/export", nil, &status); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	status.project()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}

func (s *studioApp) exportAudio(w http.ResponseWriter, r *http.Request) {
	s.exportDownload(w, r, ".wav", "/api/export/file/", "audio/wav")
}

func (s *studioApp) exportMIDI(w http.ResponseWriter, r *http.Request) {
	s.exportDownload(w, r, ".mid", "/api/export/midi/file/", "audio/midi")
}

func (s *studioApp) exportDownload(w http.ResponseWriter, r *http.Request, extension, backendPath, contentType string) {
	id := strings.TrimSuffix(r.PathValue("id"), extension)
	if id == "" || len(id) > 64 || strings.ContainsAny(id, "/\\") {
		http.Error(w, "choose a completed render", http.StatusBadRequest)
		return
	}
	// The native regular file and request context bound streaming. Long
	// deliveries are not cut off by the JSON client's whole-request timeout.
	stream := *s.backend
	client := *s.backend.client
	client.Timeout = 0
	stream.client = &client
	response, err := stream.request(r.Context(), http.MethodGet, backendPath+id, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(response.Body, 16<<10)).Decode(&failure)
		http.Error(w, failure.Error, response.StatusCode)
		return
	}
	if response.Header.Get("Content-Type") != contentType {
		http.Error(w, "the export service returned an unexpected file format", http.StatusBadGateway)
		return
	}
	for _, header := range []string{"Content-Type", "Content-Disposition", "Content-Length", "X-Content-Type-Options"} {
		if value := response.Header.Get(header); value != "" {
			w.Header().Set(header, value)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.Copy(w, response.Body)
}
