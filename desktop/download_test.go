package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestNativeExportAcceptsOnlyCompletedMediaPaths(t *testing.T) {
	for _, path := range []string{
		"/media/exports/a.wav", "/media/midi-exports/ABC_123-def.mid", "/media/stem-exports/" + strings.Repeat("a", 64) + ".zip",
	} {
		if _, _, err := nativeExportFormat(path); err != nil {
			t.Fatal(path, err)
		}
	}
	for _, path := range []string{
		"", "media/exports/a.wav", "http://127.0.0.1:1234/media/exports/a.wav", "//127.0.0.1/media/exports/a.wav",
		"/media/exports/.wav", "/media/exports/a.mid", "/media/exports/a.WAV", "/media/exports/a.wav/",
		"/media/exports/a.wav?file=other", "/media/exports/a.wav#fragment", "/media/exports/a.wav?",
		"/media/exports/../secret.wav", "/media/exports/%2e%2e%2fsecret.wav", "/media/exports/%252e%252e.wav",
		"/media/exports/a/b.wav", "/media/exports/a\\b.wav", "/media/exports/a\x00.wav", "/media/exports/ä.wav",
		"/media/exports/" + strings.Repeat("x", 65) + ".wav", "/media/takes/a.wav", "/api/export/file/a",
	} {
		if _, _, err := nativeExportFormat(path); err == nil {
			t.Errorf("unsafe path accepted: %q", path)
		}
	}
}

func TestNativeExportUsesOnlyTheActiveLoopbackOrigin(t *testing.T) {
	for _, ready := range []string{
		"http://127.0.0.1:1234/", "http://127.0.0.1:1234/__local/private-startup-capability", "http://[::1]:1234/studio",
	} {
		origin, err := nativeExportOrigin(ready)
		if err != nil || origin.Path != "" || origin.RawPath != "" || origin.RawQuery != "" {
			t.Fatal("bootstrap path was not removed", ready, origin, err)
		}
	}
	for _, ready := range []string{
		"", "not a URL", "file:///tmp/score", "https://127.0.0.1:1234/", "http:127.0.0.1:1234",
		"http://example.com:1234/", "http://127.0.0.1.example.com:1234/", "http://192.168.1.2:1234/", "http://[::]:1234/",
		"http://127.0.0.1/", "http://127.0.0.1:0/", "http://127.0.0.1:65536/", "http://127.0.0.1:bad/",
		"http://user:password@127.0.0.1:1234/", "http://127.0.0.1:1234/?token=x", "http://127.0.0.1:1234/?", "http://127.0.0.1:1234/#token",
	} {
		if _, err := nativeExportOrigin(ready); err == nil {
			t.Errorf("unsafe service origin accepted: %q", ready)
		}
	}
}

func TestNativeExportStreamsEachFormatAndReplacesOnlyOnSuccess(t *testing.T) {
	for _, format := range []struct{ path, contentType, name string }{
		{"/media/exports/render_1.wav", "audio/wav", "Love song.wav"},
		{"/media/midi-exports/notes_1.mid", "audio/midi", "Song notes.mid"},
		{"/media/stem-exports/stems-1.zip", "application/zip", "Song stems.zip"},
	} {
		t.Run(format.contentType, func(t *testing.T) {
			const content = "complete export bytes"
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != format.path || r.URL.RawQuery != "" || r.Method != http.MethodGet || r.Header.Get("Cookie") != "" {
					t.Errorf("unexpected export request: %s %s", r.Method, r.URL)
				}
				w.Header().Set("Content-Type", format.contentType)
				w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": format.name}))
				w.Header().Set("Content-Length", fmt.Sprint(len(content)))
				w.(http.Flusher).Flush()
				<-release
				_, _ = io.WriteString(w, content)
			}))
			defer server.Close()
			defer func() {
				select {
				case <-release:
				default:
					close(release)
				}
			}()
			response, filename, err := openNativeExport(context.Background(), server.URL+"/__local/private-startup-capability", format.path)
			if err != nil || filename != format.name {
				t.Fatal(filename, err)
			}
			dir := t.TempDir()
			target := filepath.Join(dir, filename)
			if err := os.WriteFile(target, []byte("previous delivery"), 0600); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- saveNativeExport(response, target) }()
			if old, err := os.ReadFile(target); err != nil || string(old) != "previous delivery" {
				t.Fatal("download changed destination before completion", err)
			}
			close(release)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if data, err := os.ReadFile(target); err != nil || string(data) != content {
				t.Fatal("saved different bytes", string(data), err)
			}
			assertNativeExportFiles(t, dir, filename)
		})
	}
}

func TestNativeExportRejectsRedirectsAndUnexpectedResponses(t *testing.T) {
	var redirected atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected.Add(1)
		_, _ = io.WriteString(w, "must not fetch")
	}))
	defer other.Close()
	for _, input := range []struct {
		name, contentType, disposition, encoding string
		status                                   int
	}{
		{name: "redirect", status: 302},
		{name: "expired", status: 409},
		{name: "partial", status: 206},
		{name: "wrong MIME", contentType: "text/html", disposition: `attachment; filename="mix.wav"`},
		{name: "missing disposition", contentType: "audio/wav"},
		{name: "inline", contentType: "audio/wav", disposition: `inline; filename="mix.wav"`},
		{name: "traversal", contentType: "audio/wav", disposition: `attachment; filename="../mix.wav"`},
		{name: "encoded traversal", contentType: "audio/wav", disposition: `attachment; filename*=UTF-8''..%2fmix.wav`},
		{name: "backslash", contentType: "audio/wav", disposition: `attachment; filename="C:\\mix.wav"`},
		{name: "wrong extension", contentType: "audio/wav", disposition: `attachment; filename="mix.zip"`},
		{name: "device", contentType: "audio/wav", disposition: `attachment; filename="NUL.wav"`},
		{name: "encoded response", contentType: "audio/wav", disposition: `attachment; filename="mix.wav"`, encoding: "gzip"},
	} {
		t.Run(input.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", input.contentType)
				w.Header().Set("Content-Disposition", input.disposition)
				w.Header().Set("Content-Encoding", input.encoding)
				if input.status == 302 {
					w.Header().Set("Location", other.URL+"/media/exports/stolen.wav")
				}
				if input.status > 0 {
					w.WriteHeader(input.status)
				}
				_, _ = io.WriteString(w, "not a completed WAV")
			}))
			defer server.Close()
			response, _, err := openNativeExport(context.Background(), server.URL, "/media/exports/job.wav")
			if err == nil || response != nil {
				if response != nil {
					response.Body.Close()
				}
				t.Fatal("unsafe download accepted", err)
			}
		})
	}
	if redirected.Load() != 0 {
		t.Fatal("download followed a redirect")
	}
}

func TestNativeExportRejectsUnsafeSuggestedFilenames(t *testing.T) {
	for _, name := range []string{"", ".wav", "../out.wav", "/out.wav", `C:\out.wav`, "out.wav:stream", "a\x00.wav", "a\n.wav", "a?.wav", " out.wav", "out.wav ", "out.WAV", "CON.wav", "LPT1.wav", strings.Repeat("a", 256) + ".wav"} {
		if nativeExportFilename(name, ".wav") {
			t.Errorf("unsafe name accepted: %q", name)
		}
	}
	for _, name := range []string{"mix.wav", "Love song.wav", "Voz de Danilo.wav", "夜の歌.wav"} {
		if !nativeExportFilename(name, ".wav") {
			t.Errorf("valid name refused: %q", name)
		}
	}
}

func TestNativeExportFailedOrCancelledTransferPreservesDestination(t *testing.T) {
	for _, failure := range []string{"truncated", "empty", "cancelled after body", "cancelled before save"} {
		t.Run(failure, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "audio/wav")
				w.Header().Set("Content-Disposition", `attachment; filename="mix.wav"`)
				if failure == "truncated" {
					w.Header().Set("Content-Length", "100")
				}
				w.(http.Flusher).Flush()
				if failure != "empty" {
					_, _ = io.WriteString(w, "partial")
				}
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			response, _, err := openNativeExport(ctx, server.URL, "/media/exports/job.wav")
			if err != nil {
				t.Fatal(err)
			}
			if failure == "cancelled after body" {
				response.Body = &cancelNativeExportOnClose{ReadCloser: response.Body, cancel: cancel}
			}
			if failure == "cancelled before save" {
				cancel()
			}
			dir := t.TempDir()
			target := filepath.Join(dir, "mix.wav")
			if err := os.WriteFile(target, []byte("previous good WAV"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := saveNativeExport(response, target); err == nil {
				t.Fatal("failed transfer published a file")
			} else if strings.HasPrefix(failure, "cancelled") && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation was not retained", err)
			}
			if data, err := os.ReadFile(target); err != nil || string(data) != "previous good WAV" {
				t.Fatal("failed transfer replaced the prior file", err)
			}
			assertNativeExportFiles(t, dir, "mix.wav")
		})
	}
}

type cancelNativeExportOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelNativeExportOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

func TestNativeExportStreamsLargeChunkedBody(t *testing.T) {
	chunk := bytes.Repeat([]byte("01234567"), 1024)
	const blocks = 512
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="stems.zip"`)
		w.(http.Flusher).Flush()
		for range blocks {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	response, _, err := openNativeExport(context.Background(), server.URL, "/media/stem-exports/job.zip")
	if err != nil {
		t.Fatal(err)
	}
	if response.ContentLength != -1 {
		t.Fatal("fixture must exercise a chunked response")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "stems.zip")
	if err := saveNativeExport(response, target); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	actual, expected := sha256.New(), sha256.New()
	n, err := io.Copy(actual, file)
	for range blocks {
		_, _ = expected.Write(chunk)
	}
	if err != nil || n != int64(blocks*len(chunk)) || !bytes.Equal(actual.Sum(nil), expected.Sum(nil)) {
		t.Fatal("streaming changed or lost bytes", n, err)
	}
	assertNativeExportFiles(t, dir, "stems.zip")
	t.Logf("streamed %d bytes from chunked HTTP to disk; SHA-256 %x", n, actual.Sum(nil))
}

func TestNativeExportRequiresAPickerFileTarget(t *testing.T) {
	dir := t.TempDir()
	for _, target := range []string{"relative.wav", filepath.Join(dir, "wrong.zip"), filepath.Join(dir, "directory.wav"), filepath.Join(dir, "missing", "mix.wav")} {
		if strings.HasSuffix(target, "directory.wav") {
			if err := os.Mkdir(target, 0700); err != nil {
				t.Fatal(err)
			}
		}
		request, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:1234/media/exports/job.wav", nil)
		response := &http.Response{Request: request, Body: io.NopCloser(strings.NewReader("data")), ContentLength: 4}
		if err := saveNativeExport(response, target); err == nil {
			t.Fatal("invalid picker target accepted", target)
		}
	}
	assertNativeExportFiles(t, dir, "directory.wav")
}

func assertNativeExportFiles(t *testing.T, dir string, names ...string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != len(names) {
		t.Fatal("partial export files leaked", entries, err)
	}
	for i, entry := range entries {
		if entry.Name() != names[i] {
			t.Fatal("unexpected file remained", entry.Name())
		}
	}
}
