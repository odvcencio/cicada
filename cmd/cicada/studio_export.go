package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/render"
)

type studioExportRequest struct {
	TargetLUFS  float64 `json:"target_lufs"`
	TruePeakMax float64 `json:"true_peak_max"`
	Tolerance   float64 `json:"tolerance"`
	Rate        int     `json:"rate"`
	Bits        int     `json:"bits"`
}

type studioExportStatus struct {
	State      string              `json:"state"`
	Path       string              `json:"path,omitempty"`
	TargetLUFS float64             `json:"target_lufs,omitempty"`
	Pass       int                 `json:"pass,omitempty"`
	PassLimit  int                 `json:"pass_limit,omitempty"`
	Report     *loudnessFileReport `json:"report,omitempty"`
	Error      string              `json:"error,omitempty"`
}

type studioExportController struct {
	mu            sync.Mutex
	active        bool
	sequence      uint64
	status        studioExportStatus
	shortfallPath string
	render        func(*notation.Score, string, render.Options, renderTargetOptions, func(int)) (loudnessFileReport, error)
}

func newStudioExportController() *studioExportController {
	return &studioExportController{
		status: studioExportStatus{State: "idle"}, render: renderStudioLoudnessFileReport,
	}
}

func (c *studioExportController) snapshot() studioExportStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

func (c *studioExportController) start(score *notation.Score, path string, request studioExportRequest) bool {
	c.mu.Lock()
	if c.active {
		c.mu.Unlock()
		return false
	}
	c.active = true
	c.sequence++
	sequence := c.sequence
	shortfallPath := studioShortfallPath(path)
	previousShortfallPath := ""
	if c.status.State == "shortfall" {
		previousShortfallPath = c.status.Path
	}
	c.shortfallPath = shortfallPath
	c.status = studioExportStatus{
		State: "queued", Path: path, TargetLUFS: request.TargetLUFS, PassLimit: loudnessPassLimit,
	}
	runner := c.render
	c.mu.Unlock()

	options := render.Options{AssetRoot: filepath.Dir(filepath.Dir(path)), SampleRate: request.Rate, Bits: request.Bits, TailSec: 3}
	target := renderTargetOptions{LoudnessTarget: request.TargetLUFS, TruePeakMaxDBTP: request.TruePeakMax, Tolerance: request.Tolerance}
	go func() {
		c.update(sequence, func(status *studioExportStatus) { status.State = "rendering" })
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			c.finish(sequence, nil, err)
			return
		}
		stalePaths := []string{previousShortfallPath, shortfallPath}
		for index, stalePath := range stalePaths {
			if stalePath == "" || (index == 1 && stalePath == previousShortfallPath) {
				continue
			}
			if err := os.Remove(stalePath); err != nil && !os.IsNotExist(err) {
				c.finish(sequence, nil, fmt.Errorf("remove previous shortfall render: %w", err))
				return
			}
		}
		report, err := runner(score, path, options, target, func(pass int) {
			c.update(sequence, func(status *studioExportStatus) { status.Pass = pass })
		})
		c.finish(sequence, &report, err)
	}()
	return true
}

func (c *studioExportController) update(sequence uint64, update func(*studioExportStatus)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if sequence == c.sequence {
		update(&c.status)
	}
}

func (c *studioExportController) finish(sequence uint64, report *loudnessFileReport, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if sequence != c.sequence {
		return
	}
	c.active = false
	if report != nil && report.Passes > 0 {
		c.status.Report = report
		c.status.Pass = report.Passes
	}
	if err != nil {
		var shortfall *loudnessShortfallError
		if errors.As(err, &shortfall) {
			c.status.State = "shortfall"
			c.status.Path = c.shortfallPath
		} else {
			c.status.State = "failed"
		}
		c.status.Error = err.Error()
		return
	}
	c.status.State = "succeeded"
}

func (s *studio) exportStatus(w http.ResponseWriter, _ *http.Request) {
	studioJSON(w, http.StatusOK, s.exports.snapshot())
}

func (s *studio) startExport(w http.ResponseWriter, r *http.Request) {
	if !studioSameOrigin(r) {
		studioJSON(w, http.StatusForbidden, map[string]string{"error": "cross-origin exports are not allowed"})
		return
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		studioJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "expected JSON"})
		return
	}
	var input studioExportRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		studioJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		studioJSON(w, http.StatusBadRequest, map[string]string{"error": "expected one JSON request"})
		return
	}
	if err := validateStudioExportRequest(input); err != nil {
		studioJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	inspection, err := inspectScore(s.path)
	if err != nil {
		studioJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	targetText := studioTargetFilename(input.TargetLUFS)
	scoreName := strings.TrimSuffix(filepath.Base(s.path), filepath.Ext(s.path))
	path := filepath.Join(filepath.Dir(s.path), "exports", scoreName+"-"+targetText+"LUFS.wav")
	if !s.exports.start(inspection.score, path, input) {
		studioJSON(w, http.StatusConflict, map[string]any{"error": "an export is already running", "job": s.exports.snapshot()})
		return
	}
	studioJSON(w, http.StatusAccepted, s.exports.snapshot())
}

func studioTargetFilename(target float64) string {
	if target == 0 {
		return "0"
	}
	value := strconv.FormatFloat(math.Abs(target), 'f', -1, 64)
	if target < 0 {
		return "minus" + value
	}
	return "plus" + value
}

func studioShortfallPath(path string) string {
	extension := filepath.Ext(path)
	return strings.TrimSuffix(path, extension) + "-shortfall" + extension
}

func validateStudioExportRequest(request studioExportRequest) error {
	if math.IsNaN(request.TargetLUFS) || math.IsInf(request.TargetLUFS, 0) || request.TargetLUFS < -70 || request.TargetLUFS > 0 {
		return fmt.Errorf("target_lufs must be between -70 and 0 LUFS")
	}
	if math.IsNaN(request.TruePeakMax) || math.IsInf(request.TruePeakMax, 0) || request.TruePeakMax < -24 || request.TruePeakMax > 0 {
		return fmt.Errorf("true_peak_max must be between -24 and 0 dBTP")
	}
	if math.IsNaN(request.Tolerance) || math.IsInf(request.Tolerance, 0) || request.Tolerance < 0 || request.Tolerance > 10 {
		return fmt.Errorf("tolerance must be between 0 and 10 LU")
	}
	if request.Rate != 44_100 && request.Rate != 48_000 && request.Rate != 96_000 {
		return fmt.Errorf("rate must be 44100, 48000, or 96000 Hz")
	}
	if request.Bits != 16 && request.Bits != 24 && request.Bits != 32 {
		return fmt.Errorf("bits must be 16, 24, or 32")
	}
	return nil
}
