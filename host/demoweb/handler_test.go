package demoweb

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testRevision = "954826cfd34f6f6ec3c1fde9f03c0dd0c111ea03"

func testBuild() Build {
	return Build{Revision: testRevision, Assets: map[string]Asset{
		"/":                   {ContentType: "text/html; charset=utf-8", Bytes: []byte("<!doctype html><title>Static host test fixture</title>")},
		"/assets/kernel.wasm": {ContentType: "application/wasm", Bytes: []byte("wasm fixture")},
		"/audio/bundled.wav":  {ContentType: "audio/wav", Bytes: []byte("playback fixture")},
	}}
}

func TestNoNativeExportAgentOrFilesystemRoutes(t *testing.T) {
	h, err := NewHandler(testBuild())
	if err != nil {
		t.Fatal(err)
	}
	for _, urlPath := range []string{
		"/api/export", "/api/export/midi", "/api/project/download", "/api/render", "/api/stems", "/exports/mix.wav", "/download/project.json",
		"/api/audio/ws", "/api/audio/config", "/api/transport", "/api/kernel-image", "/api/takes", "/api/source", "/api/future-route",
		"/agent/execute", "/mcp", "/_gosx/rpc/export", "/_gosx/action", "/api/export?download=1",
	} {
		for _, method := range []string{"GET", "HEAD", "POST", "PUT", "DELETE", "OPTIONS"} {
			r := httptest.NewRequest(method, "https://"+PublicHost+urlPath, strings.NewReader(`{"action":"export","path":"/etc/passwd"}`))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != http.StatusForbidden {
				t.Fatalf("%s %s returned %d", method, urlPath, w.Code)
			}
		}
	}
	for _, urlPath := range []string{"/etc/passwd", "/.git/config", "/assets/../../etc/passwd", "/assets/%2e%2e/private", "/%61pi/export", "/assets%2fkernel.wasm", "/audio/private.wav", "/assets/project.json", "/assets/kernel.wasm/extra", "/assets//kernel.wasm"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "https://"+PublicHost+urlPath, nil))
		if w.Code != http.StatusNotFound {
			t.Fatalf("%s returned %d", urlPath, w.Code)
		}
	}
}

func TestImmutablePlaybackAssetsAndHeaders(t *testing.T) {
	build := testBuild()
	h, err := NewHandler(build)
	if err != nil {
		t.Fatal(err)
	}
	build.Assets["/audio/bundled.wav"].Bytes[0] = 'X'
	for _, urlPath := range []string{"/", "/assets/kernel.wasm", "/audio/bundled.wav"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "https://"+PublicHost+urlPath, nil))
		if w.Code != 200 || w.Header().Get("X-Cicada-Revision") != testRevision {
			t.Fatal("build asset unavailable")
		}
		if urlPath == "/audio/bundled.wav" && w.Body.String() != "playback fixture" {
			t.Fatal("build bytes are mutable")
		}
		if w.Header().Get("Access-Control-Allow-Origin") != "" || w.Header().Get("Content-Disposition") != "" {
			t.Fatal("unexpected CORS/download header")
		}
		if !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox allow-scripts allow-same-origin") || strings.Contains(w.Header().Get("Content-Security-Policy"), "allow-downloads") {
			t.Fatal("download sandbox absent")
		}
		if !strings.Contains(w.Header().Get("Permissions-Policy"), "microphone=(self)") {
			t.Fatal("browser permission policy absent")
		}
		r := httptest.NewRequest("HEAD", "https://"+PublicHost+urlPath, nil)
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 || w.Body.Len() != 0 {
			t.Fatal("HEAD returned body")
		}
		r = httptest.NewRequest("GET", "https://"+PublicHost+urlPath, nil)
		r.Header.Set("If-None-Match", w.Header().Get("ETag"))
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 304 || w.Body.Len() != 0 {
			t.Fatal("ETag mismatch")
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "https://evil.invalid/", nil))
	if w.Code != 403 {
		t.Fatal("unexpected host accepted")
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "https://"+PublicHost+"/", nil))
	if w.Code != 405 {
		t.Fatal("asset mutation accepted")
	}
}

func TestBuildCannotPublishPrivateFilesOrDownloads(t *testing.T) {
	for _, entry := range []struct{ path, mime string }{
		{"/api/export", "application/javascript"}, {"/assets/project.json", "application/json"}, {"/assets/score.cicada", "text/plain"},
		{"/assets/song.mid", "audio/midi"}, {"/assets/private.zip", "application/zip"}, {"/assets/.env", "application/javascript"},
		{"/assets/../secret.js", "application/javascript"}, {"/assets/index.html", "text/html; charset=utf-8"},
	} {
		build := testBuild()
		build.Assets[entry.path] = Asset{ContentType: entry.mime, Bytes: []byte("private")}
		if _, err := NewHandler(build); err == nil {
			t.Fatalf("unsafe build path admitted: %s", entry.path)
		}
	}
	build := testBuild()
	build.Revision = "main"
	if _, err := NewHandler(build); err == nil {
		t.Fatal("unidentified source admitted")
	}
	build = testBuild()
	build.Host = "replacement-production.invalid"
	if _, err := NewHandler(build); err == nil {
		t.Fatal("arbitrary target admitted")
	}
	build = testBuild()
	build.InlineScriptHashes = []string{"unsafe-inline"}
	if _, err := NewHandler(build); err == nil {
		t.Fatal("unsafe scripts admitted")
	}
}
