package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/render"
)

func TestStudioExportRejectsBadInputAndCrossOrigin(t *testing.T) {
	handler, _ := studioTestHandler(t)
	valid := studioExportRequest{TargetLUFS: -14, TruePeakMax: -1, Tolerance: .5, Rate: 48_000, Bits: 24}
	crossOrigin := studioExportCall(t, handler, valid, "https://outside.example")
	if crossOrigin.Code != http.StatusForbidden {
		t.Fatalf("cross-origin export: %d %s", crossOrigin.Code, crossOrigin.Body.String())
	}
	bad := valid
	bad.TargetLUFS = -100
	response := studioExportCall(t, handler, bad, "http://127.0.0.1:1234")
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "target_lufs") {
		t.Fatalf("bad export input: %d %s", response.Code, response.Body.String())
	}
	if status := studioCall(t, handler, "/api/export", nil); status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"state":"idle"`) {
		t.Fatalf("bad requests should not start a job: %d %s", status.Code, status.Body.String())
	}
}

func TestStudioExportRejectsStaleScoreSnapshot(t *testing.T) {
	handler, _ := studioTestHandler(t)
	request := studioExportRequest{Revision: "stale", TargetLUFS: -16, TruePeakMax: -1, Tolerance: 1, Rate: 48000, Bits: 24}
	response := studioExportCall(t, handler, request, "http://127.0.0.1:1234")
	if response.Code != 409 || !strings.Contains(response.Body.String(), "score changed") {
		t.Fatalf("stale render accepted: %d %s", response.Code, response.Body.String())
	}
}

func TestStudioExportDownloadIsBoundToCompletedJob(t *testing.T) {
	_, path := studioTestHandler(t)
	s, err := newStudio(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.transport.close()
	output := filepath.Join(filepath.Dir(path), "finished.wav")
	wav := []byte("RIFFfixtureWAVEcompleted-audio")
	if err := os.WriteFile(output, wav, 0600); err != nil {
		t.Fatal(err)
	}
	s.exports.status = studioExportStatus{ID: "completed-job", State: "succeeded", Path: output}
	handler := s.domainRoutes()
	good := studioCall(t, handler, "/api/export/file/completed-job", nil)
	if good.Code != 200 || !bytes.Equal(good.Body.Bytes(), wav) || good.Header().Get("Content-Type") != "audio/wav" || !strings.Contains(good.Header().Get("Content-Disposition"), "attachment") || good.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("download: %d %s", good.Code, good.Body.String())
	}
	unknown := studioCall(t, handler, "/api/export/file/unknown?path="+output, nil)
	if unknown.Code != 409 {
		t.Fatal("a supplied path or unknown job selected a file")
	}
	s.exports.mu.Lock()
	s.exports.active = true
	s.exports.status = studioExportStatus{ID: "new-job", State: "rendering", Path: output}
	s.exports.mu.Unlock()
	for _, id := range []string{"completed-job", "new-job"} {
		if response := studioCall(t, handler, "/api/export/file/"+id, nil); response.Code != 409 {
			t.Fatal("old or incomplete render was served")
		}
	}
}

func TestStudioExportReturns409ForSecondConcurrentJob(t *testing.T) {
	_, path := studioTestHandler(t)
	studio, err := newStudio(path)
	if err != nil {
		t.Fatal(err)
	}
	defer studio.transport.close()
	started, release := make(chan struct{}), make(chan struct{})
	studio.exports.render = func(*notation.Score, string, render.Options, renderTargetOptions, func(int)) (loudnessFileReport, error) {
		close(started)
		<-release
		return loudnessFileReport{}, nil
	}
	handler := studio.routes()
	request := studioExportRequest{TargetLUFS: -14, TruePeakMax: -1, Tolerance: .5, Rate: 48_000, Bits: 24}
	first := studioExportCall(t, handler, request, "http://127.0.0.1:1234")
	if first.Code != http.StatusAccepted {
		t.Fatalf("first export: %d %s", first.Code, first.Body.String())
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("background render did not start")
	}
	second := studioExportCall(t, handler, request, "http://127.0.0.1:1234")
	if second.Code != http.StatusConflict || !strings.Contains(second.Body.String(), "already running") {
		t.Fatalf("second export: %d %s", second.Code, second.Body.String())
	}
	close(release)
	waitStudioExport(t, handler, "succeeded")
}

func TestStudioExportSuccessAtomicFileAndReportFields(t *testing.T) {
	_, path := studioTestHandler(t)
	studio, err := newStudio(path)
	if err != nil {
		t.Fatal(err)
	}
	defer studio.transport.close()
	handler := studio.routes()
	request := studioExportRequest{TargetLUFS: -20, TruePeakMax: 0, Tolerance: 10, Rate: 48_000, Bits: 24}
	exportPath := filepath.Join(filepath.Dir(path), "exports", "score-minus20LUFS.wav")
	if err := os.MkdirAll(filepath.Dir(exportPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exportPath, []byte("old-complete-file"), 0600); err != nil {
		t.Fatal(err)
	}
	response := studioExportCall(t, handler, request, "http://127.0.0.1:1234")
	if response.Code != http.StatusAccepted {
		t.Fatalf("export request: %d %s", response.Code, response.Body.String())
	}
	status := waitStudioExport(t, handler, "succeeded")
	if status.Path != exportPath || status.Report == nil {
		t.Fatalf("export status omitted path or report: %+v", status)
	}
	if status.Report.Passes < 1 || status.Report.TargetLUFS != -20 || status.Report.AchievedLUFS == 0 {
		t.Fatalf("incomplete render report: %+v", status.Report)
	}
	data, err := os.ReadFile(exportPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 44 || string(data[:4]) != "RIFF" || bytes.Contains(data, []byte("old-complete-file")) {
		t.Fatalf("atomic output is not a complete replacement WAV (size %d)", len(data))
	}
	if rate := binary.LittleEndian.Uint32(data[24:28]); rate != 48_000 {
		t.Fatalf("render sample rate=%d, want 48000", rate)
	}
	entries, err := os.ReadDir(filepath.Dir(exportPath))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), "cicada-loudness-") {
			t.Fatalf("temporary loudness render remains after rename: %s", entry.Name())
		}
	}
	encoded, err := json.Marshal(status.Report)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"achieved_lufs"`, `"true_peak_dbtp"`, `"applied_gain_db"`, `"passes"`, `"largest_limiter_reduction_db"`, `"dc_correction"`} {
		if !bytes.Contains(encoded, []byte(field)) {
			t.Fatalf("render report missing %s: %s", field, encoded)
		}
	}
}

func TestStudioExportShortfallPreservesGoodExportAndReportsRender(t *testing.T) {
	_, path := studioTestHandler(t)
	studio, err := newStudio(path)
	if err != nil {
		t.Fatal(err)
	}
	defer studio.transport.close()
	handler := studio.routes()
	request := studioExportRequest{TargetLUFS: -5, TruePeakMax: -24, Tolerance: 0.1, Rate: 48_000, Bits: 24}
	exportPath := filepath.Join(filepath.Dir(path), "exports", "score-minus5LUFS.wav")
	shortfallPath := studioShortfallPath(exportPath)
	if err := os.MkdirAll(filepath.Dir(exportPath), 0755); err != nil {
		t.Fatal(err)
	}
	const goodExport = "existing-good-export"
	if err := os.WriteFile(exportPath, []byte(goodExport), 0600); err != nil {
		t.Fatal(err)
	}
	response := studioExportCall(t, handler, request, "http://127.0.0.1:1234")
	if response.Code != http.StatusAccepted {
		t.Fatalf("export request: %d %s", response.Code, response.Body.String())
	}
	status := waitStudioExport(t, handler, "shortfall")
	if status.Path != shortfallPath || status.Report == nil || status.Report.Passes < 1 {
		t.Fatalf("shortfall status omitted its render path or report: %+v", status)
	}
	if status.Error == "" || status.Report.AchievedLUFS == 0 || status.Report.TruePeakDBTP == 0 {
		t.Fatalf("shortfall status omitted measured loudness or true peak: %+v", status)
	}
	data, err := os.ReadFile(exportPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != goodExport {
		t.Fatalf("shortfall replaced the existing export: %q", data)
	}
	data, err = os.ReadFile(shortfallPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 44 || string(data[:4]) != "RIFF" {
		t.Fatalf("shortfall render is not a WAV: %d bytes", len(data))
	}

	started, release := make(chan struct{}), make(chan struct{})
	studio.exports.render = func(*notation.Score, string, render.Options, renderTargetOptions, func(int)) (loudnessFileReport, error) {
		close(started)
		<-release
		return loudnessFileReport{Passes: 1}, nil
	}
	next := request
	next.TargetLUFS = -16
	if response := studioExportCall(t, handler, next, "http://127.0.0.1:1234"); response.Code != http.StatusAccepted {
		t.Fatalf("new export request: %d %s", response.Code, response.Body.String())
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("new export did not start")
	}
	if _, err := os.Stat(shortfallPath); !os.IsNotExist(err) {
		t.Fatalf("starting another export should delete the shortfall render; stat error=%v", err)
	}
	close(release)
	waitStudioExport(t, handler, "succeeded")
}

func TestStudioExportTargetFilenameUsesWordsForSigns(t *testing.T) {
	for target, want := range map[float64]string{-14: "minus14", 14: "plus14", 0: "0", -14.5: "minus14.5"} {
		if got := studioTargetFilename(target); got != want {
			t.Errorf("studioTargetFilename(%v) = %q, want %q", target, got, want)
		}
	}
}

func TestRenderLoudnessFileKeepsOldFileWhenRenderFails(t *testing.T) {
	_, path := studioTestHandler(t)
	inspection, err := inspectScore(path)
	if err != nil {
		t.Fatal(err)
	}
	exportPath := filepath.Join(filepath.Dir(path), "score.wav")
	if err := os.WriteFile(exportPath, []byte("preserve-until-rename"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = renderLoudnessFileReport(inspection.score, exportPath, render.Options{SampleRate: 123, Bits: 24, TailSec: 0}, renderTargetOptions{LoudnessTarget: -14, TruePeakMaxDBTP: -1, Tolerance: .5}, nil)
	if err == nil {
		t.Fatal("invalid render unexpectedly succeeded")
	}
	data, readErr := os.ReadFile(exportPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != "preserve-until-rename" {
		t.Fatalf("failed render replaced the existing file: %q", data)
	}
}

func studioExportCall(t *testing.T, handler http.Handler, body studioExportRequest, origin string) *httptest.ResponseRecorder {
	t.Helper()
	var input bytes.Buffer
	if err := json.NewEncoder(&input).Encode(body); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/export", &input)
	request.Host = "127.0.0.1:1234"
	request.Header.Set("Origin", origin)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func waitStudioExport(t *testing.T, handler http.Handler, want string) studioExportStatus {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		request := httptest.NewRequest(http.MethodGet, "/api/export", nil)
		request.Host = "127.0.0.1:1234"
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		var status studioExportStatus
		if err := json.Unmarshal(recorder.Body.Bytes(), &status); err != nil {
			t.Fatal(err)
		}
		if status.State == want {
			return status
		}
		if status.State == "failed" && want != "failed" {
			t.Fatalf("export failed: %+v", status)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("export job did not reach", want)
	return studioExportStatus{}
}
