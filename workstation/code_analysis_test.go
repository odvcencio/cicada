package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCodeAnalysisDecoratesUnsavedUnicodeWithoutWriting(t *testing.T) {
	address, client, edits := testApp(t)
	page := getPage(t, client, address+"/studio")
	if !strings.Contains(page, `data-code-intelligence-server="/__code/analysis"`) {
		t.Fatal("Code editor must request live score analysis")
	}
	csrf := tokenFromPage(t, page)
	for _, source := range []string{
		"// café 🎵 日本語\ncicada 2\ntrack bass acid {}\npattern pulse { 1 . 5 . }\n",
		"pattern pulse { ???",
		"",
	} {
		payload, _ := json.Marshal(map[string]string{"path": "unsaved.cicada", "content": source})
		request, _ := http.NewRequest(http.MethodPost, address+"/__code/analysis", bytes.NewReader(payload))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-CSRF-Token", csrf)
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			Highlights []struct {
				StartByte int    `json:"startByte"`
				EndByte   int    `json:"endByte"`
				Capture   string `json:"capture"`
			} `json:"highlights"`
			HasErrors bool `json:"hasErrors"`
		}
		err = json.NewDecoder(response.Body).Decode(&result)
		response.Body.Close()
		if response.StatusCode != http.StatusOK || err != nil {
			t.Fatalf("analysis status=%d error=%v", response.StatusCode, err)
		}
		if !strings.Contains(response.Header.Get("Cache-Control"), "no-store") {
			t.Fatal("unsaved source analysis must remain private")
		}
		for _, span := range result.Highlights {
			if span.StartByte < 0 || span.StartByte >= span.EndByte || span.EndByte > len(source) || !utf8.ValidString(source[span.StartByte:span.EndByte]) {
				t.Fatalf("invalid source byte range %+v", span)
			}
		}
		if strings.Contains(source, "track bass") {
			at := strings.Index(source, "track")
			found := false
			for _, span := range result.Highlights {
				found = found || span.StartByte == at && span.EndByte == at+len("track")
			}
			if !found {
				t.Fatal("keyword byte offset must include preceding Unicode bytes")
			}
		}
		if strings.Contains(source, "???") && !result.HasErrors {
			t.Fatal("incomplete notation must still return decorated syntax errors")
		}
	}
	if len(*edits) != 0 {
		t.Fatalf("typing analysis wrote to the song: %+v", *edits)
	}
}

func TestCodeAnalysisRejectsUntrustedOrMalformedRequests(t *testing.T) {
	address, client, edits := testApp(t)
	csrf := tokenFromPage(t, getPage(t, client, address+"/studio"))
	for _, tc := range []struct {
		body, token string
		status      int
	}{
		{`{"content":"track bass acid {}"}`, "", http.StatusForbidden},
		{`{"content":`, csrf, http.StatusBadRequest},
		{`{"content":"", "unexpected":true}`, csrf, http.StatusBadRequest},
		{`{"content":"` + strings.Repeat(" ", 2<<20) + `"}`, csrf, http.StatusBadRequest},
	} {
		request, _ := http.NewRequest(http.MethodPost, address+"/__code/analysis", strings.NewReader(tc.body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-CSRF-Token", tc.token)
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != tc.status {
			t.Fatalf("analysis status=%d want %d", response.StatusCode, tc.status)
		}
	}
	if len(*edits) != 0 {
		t.Fatal("rejected analysis wrote to the song")
	}
}
