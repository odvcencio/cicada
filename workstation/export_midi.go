package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"m31labs.dev/gosx"
	"m31labs.dev/gosx/action"
	"m31labs.dev/gosx/server"
)

type midiExportView struct {
	ID                    string   `json:"id"`
	Revision              string   `json:"revision"`
	SourceRevision        string   `json:"source_revision"`
	CurrentSourceRevision string   `json:"current_source_revision"`
	State                 string   `json:"state"`
	Notes                 int      `json:"notes"`
	PPQ                   uint16   `json:"ppq"`
	OmittedAudio          []string `json:"audio_tracks_omitted"`
	Busy                  bool     `json:"busy"`
	DownloadHidden        bool     `json:"downloadHidden"`
	DownloadURL           string   `json:"downloadURL"`
	DownloadLabel         string   `json:"downloadLabel"`
	Progress              string   `json:"progress"`
	Version               string   `json:"version"`
}

func (v *midiExportView) project() {
	v.Busy = v.State == "preparing"
	v.DownloadHidden = v.ID == ""
	v.DownloadLabel = "Download MIDI"
	v.Progress = "Prepare the saved song's instrument notes for another music tool."
	if v.ID != "" {
		v.DownloadURL = "/media/midi-exports/" + url.PathEscape(v.ID) + ".mid"
		v.Progress = fmt.Sprintf("%d notes", v.Notes)
		if len(v.OmittedAudio) > 0 {
			v.Progress += ". Audio omitted from " + strings.Join(v.OmittedAudio, ", ") + "."
		}
		switch {
		case v.SourceRevision == "" || v.CurrentSourceRevision == "":
			v.Version = "This MIDI file's saved score version cannot be checked."
			v.DownloadLabel += " (version unverified)"
		case v.SourceRevision != v.CurrentSourceRevision:
			v.Version = "The saved score has changed. Prepare MIDI again to include your latest edits."
			v.DownloadLabel = "Download earlier MIDI"
		default:
			v.Version = "These notes match the saved score."
		}
	}
	if v.Busy {
		v.Progress = "Preparing MIDI…"
	}
}

func (s *studioApp) midiExportStatus(w http.ResponseWriter, r *http.Request) {
	var status midiExportView
	if err := s.backend.call(r.Context(), http.MethodGet, "/api/export/midi", nil, &status); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	status.project()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(status)
}

func (s *studioApp) midiExportPanel(ctx *server.Context, v workspace, csrf string) gosx.Node {
	var status midiExportView
	if err := s.backend.call(ctx.Request.Context(), http.MethodGet, "/api/export/midi", nil, &status); err != nil {
		return gosx.El("p", gosx.Attrs(gosx.Attr("class", "error")), gosx.Text("MIDI export is unavailable. Refresh Studio to try again."))
	}
	status.project()
	message := ""
	if state, ok := action.States(ctx.Request)["export-midi"]; ok {
		message = state.Message()
	}
	return gosx.El("details", gosx.Attrs(gosx.Attr("id", "midi-export"), gosx.Attr("class", "strip")),
		gosx.El("summary", gosx.El("strong", gosx.Text("Notes and timing (MIDI)"))),
		gosx.El("p", gosx.Text("Carry instrument and sampler notes, tempo, and markers into another music tool. MIDI does not include recordings, sounds, or effects. Use WAV for the finished sound.")),
		gosx.El("div", gosx.Attrs(gosx.Attr("data-gosx-live-src", "/api/export/midi"), gosx.Attr("data-gosx-live-interval", "1s"), gosx.Attr("data-gosx-live-focus", "allow")),
			s.form(v, csrf, "export-midi", "export-midi", hidden("source_revision", v.SourceRevision),
				gosx.El("button", gosx.Attrs(gosx.Attr("type", "submit"), gosx.Attr("disabled", status.Busy), gosx.Attr("data-gosx-live-bind-attr", "disabled:busy")), gosx.Text("Prepare MIDI")),
				gosx.El("p", gosx.Attrs(gosx.Attr("class", "form-status"), gosx.Attr("role", "status")), gosx.Text(message))),
			gosx.El("p", gosx.Attrs(gosx.Attr("role", "status"), gosx.Attr("data-gosx-live-bind", "progress")), gosx.Text(status.Progress)),
			gosx.El("p", gosx.Attrs(gosx.Attr("role", "status"), gosx.Attr("data-gosx-live-bind", "version")), gosx.Text(status.Version)),
			gosx.El("a", gosx.Attrs(gosx.Attr("href", status.DownloadURL), gosx.BoolAttr("download"), gosx.Attr("hidden", status.DownloadHidden), gosx.Attr("data-gosx-live-bind-attr", "href:downloadURL,hidden:downloadHidden"), gosx.Attr("data-gosx-live-bind", "downloadLabel")), gosx.Text(status.DownloadLabel))))
}
