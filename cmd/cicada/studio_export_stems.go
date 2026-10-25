package main

import (
	"archive/zip"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/render"
)

const studioStemsFormat = "stereo float32 WAV"
const studioStemsTailSeconds = 3

type studioStemsExportRequest struct {
	Revision       string `json:"revision"`
	SourceRevision string `json:"source_revision"`
	Rate           int    `json:"rate"`
}

// Artifact fields describe the last successful delivery, even while a new
// attempt is running or has failed. An ID is never reassigned to other bytes.
type studioStemsExportStatus struct {
	ID                    string `json:"id"`
	Revision              string `json:"revision"`
	SourceRevision        string `json:"source_revision"`
	CurrentSourceRevision string `json:"current_source_revision"`
	JobSourceRevision     string `json:"job_source_revision"`
	State                 string `json:"state"`
	Error                 string `json:"error"`
	Tracks                int    `json:"tracks"`
	Files                 int    `json:"files"` // WAV files; the ZIP also contains manifest.json.
	Rate                  int    `json:"rate"`
	Frames                int64  `json:"frames"`
	TailSeconds           int    `json:"tail_seconds"`
	Format                string `json:"format"`
}

type studioStemsExportController struct {
	mu     sync.Mutex
	active bool
	status studioStemsExportStatus
	path   string
	render func(*notation.Score, render.Options, string) (render.Report, error)
}

func (c *studioStemsExportController) snapshot() studioStemsExportStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	status := c.status
	if status.State == "" {
		status.State = "idle"
	}
	status.Format = studioStemsFormat
	status.TailSeconds = studioStemsTailSeconds
	return status
}

func (s *studio) stemsExportStatus(w http.ResponseWriter, _ *http.Request) {
	studioJSON(w, http.StatusOK, s.stemsExportSnapshot())
}

func (s *studio) stemsExportSnapshot() studioStemsExportStatus {
	status := s.stemsExport.snapshot()
	if status.ID != "" || status.JobSourceRevision != "" {
		status.CurrentSourceRevision, _ = project.ReadScoreRevision(s.path)
	}
	return status
}

func (s *studio) startStemsExport(w http.ResponseWriter, r *http.Request) {
	if !studioSameOrigin(r) {
		studioJSON(w, http.StatusForbidden, map[string]string{"error": "cross-origin exports are not allowed"})
		return
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		studioJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "expected JSON"})
		return
	}
	var input studioStemsExportRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	err := decoder.Decode(&input)
	var extra any
	if err != nil || decoder.Decode(&extra) != io.EOF || input.SourceRevision == "" {
		studioJSON(w, http.StatusBadRequest, map[string]string{"error": "Refresh Studio to load the saved song before exporting stems."})
		return
	}
	if input.Rate != 44_100 && input.Rate != 48_000 && input.Rate != 96_000 {
		studioJSON(w, http.StatusBadRequest, map[string]string{"error": "rate must be 44100, 48000, or 96000 Hz"})
		return
	}
	inspection, err := inspectScore(s.path)
	if err != nil {
		studioJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	if input.SourceRevision != inspection.sources.ScoreRevision() || input.Revision != "" && input.Revision != studioRevision(inspection.source) {
		studioJSON(w, http.StatusConflict, map[string]string{"error": "Saved score files or libraries changed. Refresh Studio before exporting stems again."})
		return
	}
	if hasDiagnosticErrors(inspection.diagnostics) {
		studioJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": diagnosticError(inspection.diagnostics).Error()})
		return
	}
	input.Revision = studioRevision(inspection.source)
	if !s.stemsExport.start(inspection.score, s.path, input, len(inspection.semantic.Tracks)) {
		studioJSON(w, http.StatusConflict, map[string]string{"error": "Stems are already being exported. Wait for the current job to finish."})
		return
	}
	studioJSON(w, http.StatusAccepted, s.stemsExportSnapshot())
}

func (c *studioStemsExportController) start(score *notation.Score, scorePath string, request studioStemsExportRequest, tracks int) bool {
	c.mu.Lock()
	if c.active {
		c.mu.Unlock()
		return false
	}
	c.active = true
	c.status.State, c.status.Error = "queued", ""
	c.status.JobSourceRevision = request.SourceRevision
	runner := c.render
	if runner == nil {
		runner = render.Stems
	}
	c.mu.Unlock()

	go func() {
		path, report, err := c.deliver(score, scorePath, request, runner)
		c.mu.Lock()
		defer c.mu.Unlock()
		c.active = false
		if err != nil {
			c.status.State, c.status.Error = "failed", err.Error()
			return
		}
		previousPath := c.path
		c.path = path
		c.status = studioStemsExportStatus{
			ID:       strings.TrimSuffix(filepath.Base(path), ".zip"),
			Revision: request.Revision, SourceRevision: request.SourceRevision, JobSourceRevision: request.SourceRevision,
			State: "succeeded", Tracks: tracks, Files: tracks + 5, Rate: report.SampleRate, Frames: report.Frames,
			Format: studioStemsFormat, TailSeconds: studioStemsTailSeconds,
		}
		// Only retire the exact previous archive this controller published, and
		// only after its replacement is complete. An already-open download keeps
		// its file handle. Windows may refuse to unlink it until that download
		// closes; retain that file rather than failing the new delivery.
		if previousPath != "" && previousPath != path {
			_ = os.Remove(previousPath)
		}
	}()
	return true
}

func (c *studioStemsExportController) deliver(score *notation.Score, scorePath string, request studioStemsExportRequest, runner func(*notation.Score, render.Options, string) (render.Report, error)) (string, render.Report, error) {
	var report render.Report
	output := filepath.Join(filepath.Dir(scorePath), "exports")
	if err := os.MkdirAll(output, 0755); err != nil {
		return "", report, err
	}
	stage, err := os.MkdirTemp(output, ".cicada-stems-export-*")
	if err != nil {
		return "", report, err
	}
	defer os.RemoveAll(stage)
	c.mu.Lock()
	c.status.State = "rendering"
	c.mu.Unlock()
	dir := filepath.Join(stage, "stems")
	// Tracks and returns retain their authored processing and levels. There is
	// no independent normalization, dither, or loudness targeting per stem.
	options := render.Options{AssetRoot: filepath.Dir(scorePath), AssetDir: filepath.Dir(scorePath), SampleRate: request.Rate, Bits: 32, TailSec: studioStemsTailSeconds}
	report, err = runner(score, options, dir)
	if err != nil {
		return "", report, err
	}
	c.mu.Lock()
	c.status.State = "packaging"
	c.mu.Unlock()
	archive := filepath.Join(stage, "delivery.zip")
	if err := studioZipStems(dir, archive); err != nil {
		return "", report, err
	}
	// The random delivery name is independent of user input and previous files.
	// Publishing never overwrites an unrelated file in the exports directory.
	path := filepath.Join(output, "stems-"+rand.Text()+".zip")
	if err := os.Rename(archive, path); err != nil {
		return "", report, err
	}
	return path, report, nil
}

func studioZipStems(dir, path string) (err error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
	}()
	archive := zip.NewWriter(file)
	archiveClosed := false
	defer func() {
		if !archiveClosed {
			if closeErr := archive.Close(); err == nil {
				err = closeErr
			}
		}
	}()
	buffer := make([]byte, 64<<10)
	for _, entry := range files {
		name := entry.Name()
		if entry.Type()&os.ModeSymlink != 0 || strings.ContainsAny(name, "/\\") || name != "manifest.json" && !strings.HasSuffix(name, ".wav") {
			return fmt.Errorf("unexpected stem output %q", name)
		}
		input, openErr := os.Open(filepath.Join(dir, name))
		if openErr != nil {
			return openErr
		}
		info, statErr := input.Stat()
		if statErr != nil || !info.Mode().IsRegular() {
			input.Close()
			return fmt.Errorf("stem output %q is not a regular file", name)
		}
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(0644)
		writer, writeErr := archive.CreateHeader(header)
		if writeErr == nil {
			_, writeErr = io.CopyBuffer(writer, input, buffer)
		}
		closeErr := input.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	err = archive.Close()
	archiveClosed = true
	if err != nil {
		return err
	}
	return file.Sync()
}

func (s *studio) stemsExportFile(w http.ResponseWriter, r *http.Request) {
	c := &s.stemsExport
	c.mu.Lock()
	if c.status.ID == "" || r.PathValue("id") != c.status.ID {
		c.mu.Unlock()
		studioJSON(w, http.StatusConflict, map[string]string{"error": "this stems file is unavailable; refresh Export for the latest completed ZIP"})
		return
	}
	file, err := os.Open(c.path)
	c.mu.Unlock()
	if err != nil {
		studioJSON(w, http.StatusNotFound, map[string]string{"error": "the stems ZIP is no longer on disk"})
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		studioJSON(w, http.StatusNotFound, map[string]string{"error": "the stems ZIP is unavailable"})
		return
	}
	name := strings.TrimSuffix(filepath.Base(s.path), filepath.Ext(s.path)) + "-stems.zip"
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	http.ServeContent(w, r, name, info.ModTime(), file)
}
