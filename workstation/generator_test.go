package main

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
)

func TestPhrasePreviewIsPrivateAndDoesNotWriteScore(t *testing.T) {
	address, client, edits := testApp(t)
	csrf := tokenFromPage(t, getPage(t, client, address+"/?panel=generator"))
	form := url.Values{"csrf_token": {csrf}, "revision": {"current"}, "__gosx_return_to": {"/?panel=generator"}}
	for key, value := range phraseDefaults {
		form.Set(key, value)
	}
	// Do not convert a 64-bit seed through JavaScript's numeric representation.
	form.Set("seed", "18446744073709551615")
	response := post(t, client, address+"/__actions/generate", form, false, address)
	if response.StatusCode != 303 {
		t.Fatalf("preview status=%d", response.StatusCode)
	}
	response.Body.Close()
	page := getPage(t, client, address+"/?panel=generator")
	if !strings.Contains(page, "Generated score preview") || !strings.Contains(page, "Replace open score with this phrase") || len(*edits) != 0 {
		t.Fatal("preview missing or mutated the score")
	}
	if !strings.Contains(page, `value="18446744073709551615"`) {
		t.Fatal("seed lost precision")
	}
	if !strings.Contains(getPage(t, client, address+"/?panel=generator"), "Generated score preview") {
		t.Fatal("preview lost after reload")
	}
	jar, _ := cookiejar.New(nil)
	other := &http.Client{Jar: jar}
	if strings.Contains(getPage(t, other, address+"/?panel=generator"), "Generated score preview") {
		t.Fatal("preview leaked to another session")
	}
	form.Set("steps", "9")
	response = post(t, client, address+"/__actions/generate", form, true, address)
	defer response.Body.Close()
	if response.StatusCode != 422 || len(*edits) != 0 {
		t.Fatalf("invalid preview status=%d edits=%d", response.StatusCode, len(*edits))
	}
}

func TestManagedTransportUsesLiveBindingWithoutRedirect(t *testing.T) {
	address, client, _ := testApp(t)
	csrf := tokenFromPage(t, getPage(t, client, address+"/?panel=code"))
	form := url.Values{"csrf_token": {csrf}, "revision": {"current"}, "action": {"stop"}, "__gosx_return_to": {"/?panel=code"}}
	response := post(t, client, address+"/__actions/transport", form, true, address)
	defer response.Body.Close()
	var result struct {
		OK       bool   `json:"ok"`
		Redirect string `json:"redirect"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || !result.OK || result.Redirect != "" {
		t.Fatalf("transport result=%+v status=%d", result, response.StatusCode)
	}
}
