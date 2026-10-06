package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"m31labs.dev/gosx/action"
)

func reactivePost(t *testing.T, client *http.Client, address, name string, fields map[string]string) (*http.Response, action.Result) {
	t.Helper()
	data, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, address+"/__actions/"+name, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-CSRF-Token", fields["csrf_token"])
	req.Header.Set("X-Cicada-Reactive", "1")
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result action.Result
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return response, result
}

func TestReactiveEditReturnsCanonicalProjectionWithoutRedirect(t *testing.T) {
	address, client, edits := testApp(t)
	csrf := tokenFromPage(t, getPage(t, client, address+"/?panel=patterns"))
	response, result := reactivePost(t, client, address, "pattern", map[string]string{
		"csrf_token": csrf, "revision": "current", "action": "pitch", "pattern": "p", "step": "4", "pitch": "72", "octave": "3",
		"__cicada_location": "/?panel=patterns&pattern=p&step=1&octave=3",
	})
	if response.StatusCode != 200 || !result.OK || result.Redirect != "" || response.Header.Get("Location") != "" {
		t.Fatalf("reactive edit navigated: %d %+v", response.StatusCode, result)
	}
	var projection workspaceProjection
	if err := json.Unmarshal(result.Data, &projection); err != nil {
		t.Fatal(err)
	}
	// The acknowledgment and canonical workspace agree, so queued edits can
	// safely advance to this confirmed revision.
	if projection.Revision != "current" || projection.WriteRevision != projection.Revision || !projection.Saved || projection.RefreshRequired || projection.Location != patternURL("p", 4, "", "3") || !strings.Contains(projection.HTML, `class="studio"`) || len(*edits) != 1 {
		t.Fatalf("canonical projection: %+v edits=%d", projection, len(*edits))
	}
}

func TestReactiveConflictRetainsValuesAndDoesNotProjectSavedState(t *testing.T) {
	address, client, edits := testApp(t)
	csrf := tokenFromPage(t, getPage(t, client, address+"/"))
	response, result := reactivePost(t, client, address, "pattern", map[string]string{
		"csrf_token": csrf, "revision": "old", "action": "pitch", "pattern": "p", "step": "4", "pitch": "72", "octave": "3",
		"__cicada_location": "/?panel=patterns&pattern=p&step=4",
	})
	if response.StatusCode != 409 || result.OK || result.Redirect != "" || result.Values["pitch"] != "72" || len(result.Data) != 0 || len(*edits) != 1 {
		t.Fatalf("conflict lost the submitted draft: %d %+v", response.StatusCode, result)
	}
}

func TestReactiveRangeEditPreservesPatternSelection(t *testing.T) {
	address, client, _ := testApp(t)
	csrf := tokenFromPage(t, getPage(t, client, address+"/?panel=patterns"))
	location := "/?panel=patterns&pattern=p&step=4&octave=3"
	response, result := reactivePost(t, client, address, "pattern", map[string]string{
		"csrf_token": csrf, "revision": "current", "action": "range", "pattern": "p",
		"operation": "clear", "first": "4", "last": "4", "target": "1", "amount": "0",
		"__cicada_location": location, "__gosx_return_to": "/?panel=patterns",
	})
	var projection workspaceProjection
	if err := json.Unmarshal(result.Data, &projection); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || !result.OK || projection.Location != location {
		t.Fatalf("range edit lost the selected pattern and step: %d %+v", response.StatusCode, projection)
	}
}

func TestReactiveLocationValidationPrecedesMutation(t *testing.T) {
	address, client, edits := testApp(t)
	csrf := tokenFromPage(t, getPage(t, client, address+"/"))
	for _, location := range []string{"https://example.com/", "//example.com/", "/api/workspace", "/__actions/source", strings.Repeat("x", 8193)} {
		response, result := reactivePost(t, client, address, "pattern", map[string]string{
			"csrf_token": csrf, "revision": "current", "action": "pitch", "pattern": "p", "step": "4", "pitch": "72",
			"__cicada_location": location,
		})
		if response.StatusCode != 422 || result.OK || len(*edits) != 0 {
			t.Fatalf("unsafe location reached mutation: %d edits=%d", response.StatusCode, len(*edits))
		}
	}
}

func TestProjectionFailureAfterSaveDoesNotReportWriteFailure(t *testing.T) {
	address, client, _ := testApp(t)
	csrf := tokenFromPage(t, getPage(t, client, address+"/"))
	// Exercise the response adapter directly with a successful command and a
	// failed read. The saved=true receipt prevents a client retrying the POST.
	audio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_, _ = io.WriteString(w, `{"revision":"own-save","valid":true}`)
			return
		}
		w.WriteHeader(503)
		_, _ = io.WriteString(w, `{"error":"temporarily unavailable"}`)
	}))
	defer audio.Close()
	b, _ := newBackend(audio.URL)
	s := &studioApp{backend: b}
	request := httptest.NewRequest(http.MethodPost, "http://localhost/__actions/pattern", strings.NewReader(`{"revision":"current","__cicada_location":"/?panel=patterns"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Cicada-Reactive", "1")
	request.Header.Set("X-CSRF-Token", csrf)
	response := httptest.NewRecorder()
	writes := 0
	s.serveAction(response, request, "pattern", func(ctx *action.Context) error {
		writes++
		if err := b.call(ctx.Request.Context(), http.MethodPost, "/api/pattern", ctx.FormData, nil); err != nil {
			return err
		}
		ctx.Redirect("/?panel=patterns")
		return nil
	})
	var result action.Result
	_ = json.Unmarshal(response.Body.Bytes(), &result)
	var receipt struct {
		WriteRevision   string `json:"writeRevision"`
		Saved           bool   `json:"saved"`
		RefreshRequired bool   `json:"refreshRequired"`
	}
	_ = json.Unmarshal(result.Data, &receipt)
	if response.Code != 200 || !result.OK || !receipt.Saved || !receipt.RefreshRequired || receipt.WriteRevision != "own-save" || writes != 1 || result.Redirect != "" {
		t.Fatalf("ambiguous saved receipt: %d %+v %+v", response.Code, result, receipt)
	}
}
