package main

import (
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestSamplerPanelShowsPadsChopControlsEffectsAndNativeForms(t *testing.T) {
	address, client, _ := testApp(t)
	page := getPage(t, client, address+"/?panel=sampler&pad=3")
	if strings.Count(page, `data-sampler-pad="`) != 16 {
		t.Fatal("sampler does not have sixteen pads")
	}
	for _, want := range []string{`data-pad-selected="true"`, `name="pad" value="3"`, `name="bankRevision" value="bank-current"`, `name="source_revision" value="current-sources"`, `data-sampler-wave`, `name="start"`, `name="end"`, `name="pitch"`, `name="reverse"`, `name="bypassEffects"`, `name="choke"`, `name="mode"`, `name="performance"`, `name="destination"`, `name="delayMix"`, `name="reverbMix"`, "Record pads", "Resample performance", "Import and chop WAV", "Chop source", "Apply effects"} {
		if !strings.Contains(page, want) {
			t.Errorf("sampler missing %q", want)
		}
	}
}

func TestSamplerActionsPreserveBankRevisionAndRejectMalformedFields(t *testing.T) {
	address, client, edits := testApp(t)
	csrf := tokenFromPage(t, getPage(t, client, address+"/?panel=sampler"))
	form := url.Values{"csrf_token": {csrf}, "revision": {"current"}, "source_revision": {"displayed-project"}, "bankRevision": {"bank-current"}, "action": {"pad"}, "pad": {"3"}, "name": {"reverse key"}, "source": {strings.Repeat("a", 64)}, "start": {"1000"}, "end": {"2000"}, "pitch": {"-7"}, "gainDB": {"-6"}, "attackMS": {"2"}, "releaseMS": {"5"}, "mode": {"gate"}, "choke": {"2"}, "reverse": {"on"}, "bypassEffects": {"on"}}
	response := post(t, client, address+"/__actions/sampler", form, true, "")
	data, _ := io.ReadAll(response.Body)
	if (response.StatusCode != 200 && response.StatusCode != 303) || !strings.Contains(string(data), `"ok":true`) {
		t.Fatal(response.StatusCode, string(data))
	}
	last := (*edits)[len(*edits)-1]
	settings := last["settings"].(map[string]any)
	if last["source_revision"] != "displayed-project" || last["bankRevision"] != "bank-current" || settings["start"] != float64(1000) || settings["pitch"] != float64(-7) || settings["reverse"] != true || settings["bypassEffects"] != true {
		t.Fatal(last)
	}
	count := len(*edits)
	form.Set("pitch", "oops")
	response = post(t, client, address+"/__actions/sampler", form, true, "")
	data, _ = io.ReadAll(response.Body)
	if len(*edits) != count || !strings.Contains(string(data), "pitch must be a whole number") {
		t.Fatal("invalid pitch reached backend", string(data))
	}
	form.Set("pitch", "0")
	form.Set("gainDB", "NaN")
	response = post(t, client, address+"/__actions/sampler", form, true, "")
	_, _ = io.ReadAll(response.Body)
	if len(*edits) != count {
		t.Fatal("nonfinite gain reached backend")
	}
	form.Del("csrf_token")
	if response = post(t, client, address+"/__actions/sampler", form, true, ""); response.StatusCode != 403 {
		t.Fatal("sampler action accepted without CSRF")
	}
}

func TestSamplerImportRejectsViewerAndMissingCSRF(t *testing.T) {
	address, owner, _ := testApp(t)
	getPage(t, owner, address+"/?panel=sampler")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var body strings.Builder
	form := multipart.NewWriter(&body)
	_ = form.WriteField("revision", "current")
	_ = form.Close()
	request, _ := http.NewRequest("POST", address+"/__sampler/import", strings.NewReader(body.String()))
	request.Header.Set("Content-Type", form.FormDataContentType())
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("import accepted without editor session and CSRF", response.StatusCode)
	}
}
