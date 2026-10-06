package main

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestSamplePackFormDefaultsToPCM16AndOffersExact(t *testing.T) {
	var body string
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/workspace":
			io.WriteString(w, `{"revision":"current","valid":true,"path":"song.cicada","source":"cicada 1","project":{"title":"Test","edition":1,"tracks":[]}}`)
		case "/api/instrument-pack/download":
			b, _ := io.ReadAll(r.Body)
			body = string(b)
			io.WriteString(w, `{"declaration":"sampler grand { pack = \"packs/grand/manifest.json\" }"}`)
		default:
			io.WriteString(w, `{}`)
		}
	}))
	defer service.Close()
	b, err := newBackend(service.URL)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newApp(b)
	if err != nil {
		t.Fatal(err)
	}
	app := httptest.NewServer(handler)
	defer app.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Get(app.URL + "/?panel=instruments")
	if err != nil {
		t.Fatal(err)
	}
	pageBytes, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	page := string(pageBytes)
	for _, text := range []string{`id="sample-packs"`, `value="hq16" selected`, `value="lossless"`, `Download sample pack`} {
		if !strings.Contains(page, text) {
			t.Fatalf("missing pack control %q", text)
		}
	}
	csrf := tokenFromPage(t, page)
	form := url.Values{"csrf_token": {csrf}, "catalogURL": {"https://example.org/catalog.json"}, "sha256": {strings.Repeat("a", 64)}, "id": {"grand"}, "__gosx_return_to": {"/?panel=instruments"}}
	response = post(t, client, app.URL+"/__actions/sample-pack", form, false, app.URL)
	if response.StatusCode != 303 || !strings.Contains(body, `"tier":"hq16"`) {
		t.Fatalf("default download: %d %s", response.StatusCode, body)
	}
	form.Set("tier", "lossless")
	response = post(t, client, app.URL+"/__actions/sample-pack", form, false, app.URL)
	if response.StatusCode != 303 || !strings.Contains(body, `"tier":"lossless"`) {
		t.Fatalf("exact download: %d %s", response.StatusCode, body)
	}
	form.Set("tier", "opus")
	response = post(t, client, app.URL+"/__actions/sample-pack", form, true, app.URL)
	if response.StatusCode != 422 {
		t.Fatalf("invalid tier: %d", response.StatusCode)
	}
}
