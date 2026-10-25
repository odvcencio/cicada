package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"m31labs.dev/gosx"
	"m31labs.dev/gosx/action"
	"m31labs.dev/gosx/server"
)

type stemsExportView struct {
	ID                    string `json:"id"`
	SourceRevision        string `json:"source_revision"`
	CurrentSourceRevision string `json:"current_source_revision"`
	JobSourceRevision     string `json:"job_source_revision"`
	State                 string `json:"state"`
	Error                 string `json:"error"`
	Tracks                int    `json:"tracks"`
	Rate                  int    `json:"rate"`
	Busy                  bool   `json:"busy"`
	DownloadHidden        bool   `json:"downloadHidden"`
	DownloadURL           string `json:"downloadURL"`
	DownloadLabel         string `json:"downloadLabel"`
	Progress              string `json:"progress"`
	Version               string `json:"version"`
	Delivery              string `json:"delivery"`
}

func (v *stemsExportView) project() {
	v.Busy = v.State == "queued" || v.State == "rendering" || v.State == "packaging"
	v.DownloadHidden = v.ID == ""
	v.DownloadURL, v.Version, v.Delivery = "", "", ""
	v.DownloadLabel = "Download stems ZIP"
	v.Progress = "Ready to render individual tracks."
	switch v.State {
	case "queued":
		v.Progress = "Stem render queued."
	case "rendering":
		v.Progress = "Rendering aligned tracks and effects…"
	case "packaging":
		v.Progress = "Packing the stems ZIP…"
	case "succeeded":
		v.Progress = "Stems ready to download."
	case "failed":
		v.Progress = "Stem render failed."
	}
	if v.Busy && v.JobSourceRevision != "" && v.CurrentSourceRevision != "" && v.JobSourceRevision != v.CurrentSourceRevision {
		v.Progress += " Rendering an earlier saved version. Render again after this job finishes to include your latest edits."
	}
	if v.ID == "" {
		return
	}
	v.DownloadURL = "/media/stem-exports/" + url.PathEscape(v.ID) + ".zip"
	v.Delivery = fmt.Sprintf("%d tracks · %s Hz · stereo float32 WAV · 3-second tail", v.Tracks, strconv.Itoa(v.Rate))
	switch {
	case v.SourceRevision == "" || v.CurrentSourceRevision == "":
		v.Version = "This ZIP's saved score version cannot be checked."
		v.DownloadLabel += " (version unverified)"
	case v.SourceRevision != v.CurrentSourceRevision:
		v.Version = "The saved score has changed. Render stems again to include your latest edits."
		v.DownloadLabel = "Download earlier stems ZIP"
	default:
		v.Version = "These stems match the saved score."
	}
	if v.Busy || v.State == "failed" {
		v.Delivery = "Last completed ZIP: " + v.Delivery
	}
}

func (s *studioApp) stemsExportStatus(w http.ResponseWriter, r *http.Request) {
	var status stemsExportView
	if err := s.backend.call(r.Context(), http.MethodGet, "/api/export/stems", nil, &status); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	status.project()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(status)
}

func (s *studioApp) exportStems(w http.ResponseWriter, r *http.Request) {
	s.exportDownload(w, r, ".zip", "/api/export/stems/file/", "application/zip")
}

func (s *studioApp) stemsExportPanel(ctx *server.Context, v workspace, csrf string) gosx.Node {
	var status stemsExportView
	if err := s.backend.call(ctx.Request.Context(), http.MethodGet, "/api/export/stems", nil, &status); err != nil {
		return gosx.El("p", gosx.Attrs(gosx.Attr("class", "error")), gosx.Text("Stem export is unavailable. Refresh Studio to try again."))
	}
	status.project()
	message := ""
	if state, ok := action.States(ctx.Request)["export-stems"]; ok {
		message = state.Message()
	}
	return gosx.El("details", gosx.Attrs(gosx.Attr("id", "stems-export"), gosx.Attr("class", "strip")),
		gosx.El("summary", gosx.El("strong", gosx.Text("Individual tracks (stems)"))),
		gosx.El("p", gosx.Text("Take your song into another DAW or send it to a collaborator. Download aligned tracks, shared effects, mix buses, and a reference master in one ZIP.")),
		gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Full saved song with a 3-second tail, in stereo float32 WAV. Track effects, levels, and panning are retained; tracks are not individually loudness-normalized.")),
		s.form(v, csrf, "export-stems", "export-stems", hidden("source_revision", v.SourceRevision),
			field("Stem sample rate", selectInput("rate", "48000", []string{"44100", "48000", "96000"})),
			gosx.El("button", gosx.Attrs(gosx.Attr("id", "stems-render-button"), gosx.Attr("type", "submit"), gosx.Attr("disabled", status.Busy)), gosx.Text("Render stems")),
			gosx.El("p", gosx.Attrs(gosx.Attr("class", "form-status"), gosx.Attr("role", "status")), gosx.Text(message))),
		gosx.El("section", gosx.Attrs(gosx.Attr("id", "stems-export-result"), gosx.Attr("data-gosx-live-src", "/api/export/stems"), gosx.Attr("data-gosx-live-interval", "1s"), gosx.Attr("data-gosx-live-focus", "allow"), gosx.Attr("aria-busy", strconv.FormatBool(status.Busy)), gosx.Attr("data-gosx-live-bind-attr", "aria-busy:busy")),
			gosx.El("p", gosx.Attrs(gosx.Attr("role", "status"), gosx.Attr("data-gosx-live-bind", "progress")), gosx.Text(status.Progress)),
			gosx.El("p", gosx.Attrs(gosx.Attr("data-gosx-live-bind", "delivery")), gosx.Text(status.Delivery)),
			gosx.El("p", gosx.Attrs(gosx.Attr("role", "status"), gosx.Attr("data-gosx-live-bind", "version")), gosx.Text(status.Version)),
			gosx.El("p", gosx.Attrs(gosx.Attr("class", "error"), gosx.Attr("data-gosx-live-bind", "error")), gosx.Text(status.Error)),
			gosx.El("a", gosx.Attrs(gosx.Attr("href", status.DownloadURL), gosx.BoolAttr("download"), gosx.Attr("hidden", status.DownloadHidden), gosx.Attr("data-gosx-live-bind-attr", "href:downloadURL,hidden:downloadHidden"), gosx.Attr("data-gosx-live-bind", "downloadLabel")), gosx.Text(status.DownloadLabel))),
		gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("For mixing, import the numbered tracks and return-a/b at the same start. music.wav and sfx.wav are bus alternatives; master.wav is the compressed and limited reference. Do not stack the buses or master on top of the individual tracks.")))
}
