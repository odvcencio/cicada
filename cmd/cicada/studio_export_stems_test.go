package main

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/render"
)

func TestStudioStemsExportDeliversAlignedFloatAudio(t *testing.T) {
	source, err := os.ReadFile("../../testdata/edition1/examples/sfx-bus.cicada")
	if err != nil {
		t.Fatal(err)
	}
	source = bytes.Replace(source, []byte("main*4"), []byte("main"), 1)
	for _, rate := range []int{44100, 48000, 96000} {
		t.Run(fmt.Sprint(rate), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mix.cicada")
			if err := os.WriteFile(path, source, 0600); err != nil {
				t.Fatal(err)
			}
			s, err := newStudio(path)
			if err != nil {
				t.Fatal(err)
			}
			defer s.shutdown()
			handler := s.routes()
			s.exports.status = studioExportStatus{ID: "keep-wav", State: "succeeded"}
			s.midiExport.status = studioMIDIExportStatus{ID: "keep-midi"}
			request := stemsRequest(t, path, rate)
			response := studioCall(t, handler, "/api/export/stems", request)
			if response.Code != http.StatusAccepted {
				t.Fatal(response.Code, response.Body.String())
			}
			status := waitStudioStems(t, handler, "succeeded")
			if status.ID == "" || status.SourceRevision != request.SourceRevision || status.CurrentSourceRevision != request.SourceRevision || status.Tracks != 2 || status.Files != 7 || status.Rate != rate || status.Frames != int64(5*rate) || status.Format != studioStemsFormat || status.TailSeconds != 3 {
				t.Fatalf("incorrect delivery projection: %+v", status)
			}
			download := studioCall(t, handler, "/api/export/stems/file/"+status.ID, nil)
			if download.Code != http.StatusOK || download.Header().Get("Content-Type") != "application/zip" || download.Header().Get("Cache-Control") != "no-store" || download.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(download.Header().Get("Content-Disposition"), "mix-stems.zip") {
				t.Fatalf("missing private download: %d %v", download.Code, download.Header())
			}
			dir := extractStudioStems(t, download.Body.Bytes(), status.Files+1)
			inspection, err := inspectScore(path)
			if err != nil {
				t.Fatal(err)
			}
			verified, err := render.VerifyStems(inspection.score, dir, render.VerifyStemsOptions{ResidualMaxDB: -80})
			if err != nil || verified.Files != status.Files || verified.Frames != status.Frames || verified.SampleRate != rate {
				t.Fatalf("downloaded stems do not align and sum: %+v %v", verified, err)
			}
			var expected bytes.Buffer
			if _, err := render.WAV(inspection.score, render.Options{SampleRate: rate, Bits: 32, TailSec: 3}, &expected); err != nil {
				t.Fatal(err)
			}
			master, err := os.ReadFile(filepath.Join(dir, "master.wav"))
			if err != nil {
				t.Fatal(err)
			}
			dataEnd := 44 + int(status.Frames)*8
			if binary.LittleEndian.Uint16(master[20:22]) != 3 || binary.LittleEndian.Uint16(master[34:36]) != 32 || !bytes.Equal(master[44:dataEnd], expected.Bytes()[44:dataEnd]) {
				t.Fatal("master changed authored sound or is not float32")
			}
			if s.exports.snapshot().ID != "keep-wav" || s.midiExport.status.ID != "keep-midi" {
				t.Fatal("stems replaced the independent WAV or MIDI")
			}
			entries, err := os.ReadDir(filepath.Join(filepath.Dir(path), "exports"))
			if err != nil || len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), ".zip") {
				t.Fatal("temporary PCM or incomplete archive retained", entries, err)
			}
			t.Logf("downloaded ZIP: %d tracks, %d float32 WAVs, %d Hz, %d aligned frames; bus residual %.2f dB; master PCM matches full render", status.Tracks, verified.Files, verified.SampleRate, verified.Frames, verified.ResidualPeakDB)
		})
	}
}

func TestStudioStemsExportIncludesRecordedAudio(t *testing.T) {
	s := newTakeStudio(t, t.TempDir())
	id := captureTestTake(t, s)
	source, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.commitTake(id, studioRevision(source), nil); err != nil {
		t.Fatal(err)
	}
	handler := s.routes()
	if response := studioCall(t, handler, "/api/export/stems", stemsRequest(t, s.path, 48000)); response.Code != 202 {
		t.Fatal(response.Code, response.Body.String())
	}
	status := waitStudioStems(t, handler, "succeeded")
	download := studioCall(t, handler, "/api/export/stems/file/"+status.ID, nil)
	if download.Code != 200 {
		t.Fatal(download.Code, download.Body.String())
	}
	dir := extractStudioStems(t, download.Body.Bytes(), status.Files+1)
	inspection, err := inspectScore(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := render.VerifyStems(inspection.score, dir, render.VerifyStemsOptions{ResidualMaxDB: -80}); err != nil {
		t.Fatal("recorded vocal stems invalid", err)
	}
	vocal, err := os.ReadFile(filepath.Join(dir, "01-vox.wav"))
	if err != nil || len(vocal) < 52 || bytes.Equal(vocal[44:44+48*8], make([]byte, 48*8)) {
		t.Fatal("recorded vocal was omitted or silenced", err)
	}
}

func TestStudioStemsExportKeepsCompletedDeliveryDuringRetries(t *testing.T) {
	handler, path, s := studioTestStudio(t)
	defer s.shutdown()
	request := stemsRequest(t, path, 48000)
	if response := studioCall(t, handler, "/api/export/stems", request); response.Code != 202 {
		t.Fatal(response.Code, response.Body.String())
	}
	first := waitStudioStems(t, handler, "succeeded")
	previousPath := s.stemsExport.path
	download := studioCall(t, handler, "/api/export/stems/file/"+first.ID, nil)
	for _, failure := range []string{"render", "archive"} {
		t.Run(failure, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			defer func() {
				select {
				case <-release:
				default:
					close(release)
				}
			}()
			s.stemsExport.render = func(_ *notation.Score, _ render.Options, dir string) (render.Report, error) {
				close(started)
				<-release
				if failure == "render" {
					return render.Report{}, fmt.Errorf("injected render failure")
				}
				if err := os.MkdirAll(filepath.Join(dir, "not-a-stem"), 0700); err != nil {
					return render.Report{}, err
				}
				return render.Report{SampleRate: 48000, Frames: 1}, nil
			}
			if response := studioCall(t, handler, "/api/export/stems", request); response.Code != 202 {
				t.Fatal(response.Code, response.Body.String())
			}
			<-started
			if response := studioCall(t, handler, "/api/export/stems", request); response.Code != 409 {
				t.Fatal("concurrent job accepted", response.Code)
			}
			if response := studioCall(t, handler, "/api/export/stems/file/"+first.ID, nil); response.Code != 200 || !bytes.Equal(response.Body.Bytes(), download.Body.Bytes()) {
				t.Fatal("active retry hid or changed the completed archive")
			}
			close(release)
			failed := waitStudioStems(t, handler, "failed")
			if failed.ID != first.ID || failed.SourceRevision != first.SourceRevision || failed.Error == "" {
				t.Fatalf("failed export replaced a good one: %+v", failed)
			}
			if response := studioCall(t, handler, "/api/export/stems/file/"+first.ID, nil); response.Code != 200 || !bytes.Equal(response.Body.Bytes(), download.Body.Bytes()) {
				t.Fatal("failed retry lost the completed archive")
			}
			entries, err := os.ReadDir(filepath.Join(filepath.Dir(path), "exports"))
			if err != nil || len(entries) != 1 {
				t.Fatal("failed retry retained its temporary files", entries, err)
			}
		})
	}
	unrelatedPath := filepath.Join(filepath.Dir(path), "exports", "unrelated.zip")
	if err := os.WriteFile(unrelatedPath, []byte("user delivery"), 0600); err != nil {
		t.Fatal(err)
	}
	s.stemsExport.render = nil
	if response := studioCall(t, handler, "/api/export/stems", request); response.Code != 202 {
		t.Fatal(response.Code, response.Body.String())
	}
	current := waitStudioStems(t, handler, "succeeded")
	if current.ID == first.ID || current.Error != "" {
		t.Fatal("successful retry did not publish a new archive", current)
	}
	if response := studioCall(t, handler, "/api/export/stems/file/"+first.ID, nil); response.Code != 409 {
		t.Fatal("old download ID was reassigned to a new archive")
	}
	if _, err := os.Stat(previousPath); !os.IsNotExist(err) {
		t.Fatal("successful replacement retained the obsolete controller-owned ZIP", err)
	}
	if data, err := os.ReadFile(unrelatedPath); err != nil || string(data) != "user delivery" {
		t.Fatal("replacement changed an unrelated export", err)
	}
	entries, err := os.ReadDir(filepath.Join(filepath.Dir(path), "exports"))
	if err != nil || len(entries) != 2 {
		t.Fatal("replacement should retain only its current ZIP and the unrelated export", entries, err)
	}
}

func TestStudioStemsExportTracksChangesDuringFirstJob(t *testing.T) {
	handler, path, s := studioTestStudio(t)
	defer s.shutdown()
	request := stemsRequest(t, path, 48000)
	started, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	s.stemsExport.render = func(*notation.Score, render.Options, string) (render.Report, error) {
		close(started)
		<-release
		return render.Report{}, fmt.Errorf("fixture stopped after observing the version")
	}
	if response := studioCall(t, handler, "/api/export/stems", request); response.Code != 202 {
		t.Fatal(response.Code, response.Body.String())
	}
	<-started
	if err := os.WriteFile(path, []byte(studioScore+"\n// saved during first export\n"), 0600); err != nil {
		t.Fatal(err)
	}
	status := s.stemsExportSnapshot()
	if status.ID != "" || status.State != "rendering" || status.JobSourceRevision != request.SourceRevision || status.CurrentSourceRevision == "" || status.CurrentSourceRevision == status.JobSourceRevision {
		t.Fatalf("first in-flight render hid its changed source: %+v", status)
	}
	close(release)
	waitStudioStems(t, handler, "failed")
}

func TestStudioStemsExportRejectsUnreviewedAndInvalidProjectSources(t *testing.T) {
	for _, name := range []string{"secondary file", "manifest", "library definition", "invalid score"} {
		t.Run(name, func(t *testing.T) {
			path := copyStudioProject(t)
			changedPath := filepath.Join(filepath.Dir(path), "parts/patterns.cicada")
			updatePins := func() {
				t.Helper()
				sources, err := project.ReadSources(path, nil)
				if err != nil {
					t.Fatal(err)
				}
				pins, _, err := sources.UpdateLibraries("")
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(filepath.Dir(path), "cicada.sum"), pins, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if name == "manifest" {
				changedPath = filepath.Join(filepath.Dir(path), "cicada.mod")
			}
			if name == "library definition" {
				t.Setenv("CICADA_LIBRARY", t.TempDir())
				library := filepath.Join(filepath.Dir(path), "lib/demo/tone")
				if err := project.NewLibrary("demo/tone", library); err != nil {
					t.Fatal(err)
				}
				source, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				source = bytes.Replace(source, []byte("cicada 2\n"), []byte("cicada 2\nimport \"demo/tone\"\n"), 1)
				if err := os.WriteFile(path, source, 0600); err != nil {
					t.Fatal(err)
				}
				changedPath = filepath.Join(library, "main.cicada")
				updatePins()
			}
			s, err := newStudio(path)
			if err != nil {
				t.Fatal(err)
			}
			defer s.shutdown()
			handler := s.routes()
			request := stemsRequest(t, path, 48000)
			s.stemsExport.status = studioStemsExportStatus{ID: "previous", State: "succeeded", SourceRevision: request.SourceRevision}
			original, err := os.ReadFile(changedPath)
			if err != nil {
				t.Fatal(err)
			}
			changed := append(bytes.Clone(original), []byte("\n// outside save\n")...)
			if name == "manifest" {
				changed = bytes.Replace(original, []byte("project studio_project"), []byte("project changed_project"), 1)
			}
			if name == "invalid score" {
				changed = append(bytes.Clone(original), []byte("\npattern incomplete {\n")...)
			}
			if err := os.WriteFile(changedPath, changed, 0600); err != nil {
				t.Fatal(err)
			}
			if name == "library definition" {
				updatePins()
			}
			want := 409
			if name == "invalid score" {
				request.SourceRevision, err = project.ReadScoreRevision(path)
				if err != nil {
					t.Fatal(err)
				}
				want = 422
			}
			response := studioCall(t, handler, "/api/export/stems", request)
			status := s.stemsExportSnapshot()
			if response.Code != want || status.ID != "previous" || status.State != "succeeded" || status.CurrentSourceRevision == status.SourceRevision {
				t.Fatalf("unreviewed source started a render: %d %s %+v", response.Code, response.Body.String(), status)
			}
		})
	}
}

func TestStudioStemsExportRejectsUnsafeRequests(t *testing.T) {
	handler, path := studioTestHandler(t)
	valid := stemsRequest(t, path, 48000)
	body, _ := json.Marshal(valid)
	for _, input := range []struct {
		body, origin, contentType string
		want                      int
	}{
		{`{}`, "", "application/json", 400},
		{`{"source_revision":"x","rate":48000,"path":"escape.zip"}`, "", "application/json", 400},
		{`{"source_revision":"x","rate":48000,"bits":24}`, "", "application/json", 400},
		{`{"source_revision":"x","rate":48000} {}`, "", "application/json", 400},
		{`{"source_revision":"x","rate":48000.5}`, "", "application/json", 400},
		{`{"source_revision":"x","rate":NaN}`, "", "application/json", 400},
		{`{"source_revision":"x","rate":22050}`, "", "application/json", 400},
		{`{"source_revision":"x","rate":48000}`, "", "application/json", 409},
		{string(body), "https://outside.example", "application/json", 403},
		{string(body), "", "text/plain", 415},
	} {
		r := httptest.NewRequest("POST", "http://127.0.0.1:1234/api/export/stems", strings.NewReader(input.body))
		r.Header.Set("Content-Type", input.contentType)
		r.Header.Set("Origin", input.origin)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != input.want {
			t.Fatalf("unsafe request accepted: %s: %d %s", input.body, w.Code, w.Body.String())
		}
	}
	for _, id := range []string{"unknown", "unknown?path=/etc/passwd"} {
		if response := studioCall(t, handler, "/api/export/stems/file/"+id, nil); response.Code != 409 {
			t.Fatal("arbitrary ID downloaded a file", response.Code)
		}
	}
	var status studioStemsExportStatus
	response := studioCall(t, handler, "/api/export/stems", nil)
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil || status.State != "idle" || status.ID != "" {
		t.Fatal("bad request started an export", status, err)
	}
}

func stemsRequest(t *testing.T, path string, rate int) studioStemsExportRequest {
	t.Helper()
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := project.ReadScoreRevision(path)
	if err != nil {
		t.Fatal(err)
	}
	return studioStemsExportRequest{Revision: studioRevision(source), SourceRevision: revision, Rate: rate}
}

func waitStudioStems(t *testing.T, handler http.Handler, want string) studioStemsExportStatus {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		response := studioCall(t, handler, "/api/export/stems", nil)
		var status studioStemsExportStatus
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &status) != nil {
			t.Fatalf("could not read stems status: %d %s", response.Code, response.Body.String())
		}
		if status.State == want {
			return status
		}
		if status.State == "failed" && want != "failed" {
			t.Fatalf("stems export failed: %+v", status)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("stems job did not reach", want)
	return studioStemsExportStatus{}
}

func extractStudioStems(t *testing.T, data []byte, count int) string {
	t.Helper()
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(archive.File) != count {
		t.Fatal("invalid downloaded archive", err)
	}
	dir := t.TempDir()
	for _, entry := range archive.File {
		if strings.ContainsAny(entry.Name, "/\\") || entry.FileInfo().IsDir() {
			t.Fatal("unsafe archive entry", entry.Name)
		}
		file, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(file)
		file.Close()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, entry.Name), content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
