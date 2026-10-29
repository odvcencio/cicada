package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"m31labs.dev/cicada/phrase"
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

// generatorForm is the phrase generator's form with the given values replaced.
func generatorForm(csrf string, overrides map[string]string) url.Values {
	form := url.Values{"csrf_token": {csrf}, "revision": {"current"}, "__gosx_return_to": {"/?panel=generator"}}
	for key, value := range phraseDefaults {
		form.Set(key, value)
	}
	for key, value := range overrides {
		form.Set(key, value)
	}
	return form
}

func selectedVoice(t *testing.T, page string) string {
	t.Helper()
	match := regexp.MustCompile(`<option value="([a-z_]+)"[^>]*selected`).FindAllStringSubmatch(page, -1)
	for _, m := range match {
		for _, voice := range phrase.Voices() {
			if m[1] == voice {
				return voice
			}
		}
	}
	return ""
}

// The generator offers a voice and writes it on the track line. A new session
// plays the default FM bass; acid stays one choice among the voices.
func TestPhraseGeneratorWritesTheChosenVoice(t *testing.T) {
	address, client, edits := testApp(t)
	csrf := tokenFromPage(t, getPage(t, client, address+"/?panel=generator"))
	page := getPage(t, client, address+"/?panel=generator")
	if got := selectedVoice(t, page); got != phrase.DefaultVoice {
		t.Fatalf("a new session selects voice %q, want %q", got, phrase.DefaultVoice)
	}
	for _, voice := range []string{"acid", "piano", "fm_bass"} {
		if !strings.Contains(page, `<option value="`+voice+`"`) {
			t.Fatalf("the voice selector lacks %q", voice)
		}
	}
	if strings.Contains(page, "acid phrases") {
		t.Fatal("the generator still describes itself as an acid phrase generator")
	}
	for _, voice := range []string{"", "acid", "piano", "tine_ep"} {
		want := voice
		form := generatorForm(csrf, map[string]string{"voice": voice})
		if voice == "" {
			// A form from an earlier session has no voice field and selects the default.
			form.Del("voice")
			want = phrase.DefaultVoice
		}
		response := post(t, client, address+"/__actions/generate", form, false, address)
		if response.StatusCode != 303 {
			t.Fatalf("voice %q: preview status=%d", voice, response.StatusCode)
		}
		page = getPage(t, client, address+"/?panel=generator")
		if !strings.Contains(page, "track bass "+want+" {}") {
			t.Fatalf("voice %q: the preview lacks the %s track", voice, want)
		}
		if got := selectedVoice(t, page); got != want {
			t.Fatalf("voice %q: the form shows %q after the preview", voice, got)
		}
	}
	if len(*edits) != 0 {
		t.Fatal("previewing a phrase edited the score")
	}
}

func TestPhraseGeneratorRefusesAnUnknownVoice(t *testing.T) {
	address, client, edits := testApp(t)
	csrf := tokenFromPage(t, getPage(t, client, address+"/?panel=generator"))
	for _, voice := range []string{"drums", "model_bd", "nope"} {
		response := post(t, client, address+"/__actions/generate", generatorForm(csrf, map[string]string{"voice": voice}), true, address)
		if response.StatusCode != 422 || len(*edits) != 0 {
			t.Fatalf("voice %q: status=%d edits=%d", voice, response.StatusCode, len(*edits))
		}
	}
}

// Mutate and Evolve read the form back from the session. The voice must survive
// that round trip, and the default voice must accept every operation, including
// the rotation that once left a bar below the keyboard range.
func TestPhraseMutationKeepsTheVoice(t *testing.T) {
	address, client, edits := testApp(t)
	csrf := tokenFromPage(t, getPage(t, client, address+"/?panel=generator"))
	mutate := func(values map[string]string) *http.Response {
		form := url.Values{"csrf_token": {csrf}, "revision": {"current"}, "__gosx_return_to": {"/?panel=generator"}, "seed": {"99"}, "operation": {"nudge"}, "rotation": {"2"}, "generation": {"0"}, "mode": {"mutate"}}
		for key, value := range values {
			form.Set(key, value)
		}
		return post(t, client, address+"/__actions/mutate", form, false, address)
	}
	for _, voice := range []string{"acid", "piano", phrase.DefaultVoice} {
		response := post(t, client, address+"/__actions/generate", generatorForm(csrf, map[string]string{"voice": voice}), false, address)
		if response.StatusCode != 303 {
			t.Fatalf("voice %s: preview status=%d", voice, response.StatusCode)
		}
		for _, step := range []map[string]string{{"operation": "accent"}, {"mode": "evolve", "generation": "1"}, {"operation": "rotate"}} {
			if response := mutate(step); response.StatusCode != 303 {
				t.Fatalf("voice %s: mutation %v status=%d", voice, step, response.StatusCode)
			}
			page := getPage(t, client, address+"/?panel=generator")
			if !strings.Contains(page, "track bass "+voice+" {}") || selectedVoice(t, page) != voice {
				t.Fatalf("voice %s was lost after mutation %v", voice, step)
			}
		}
	}
	// Rotating moves the downbeat rest to the front of a bar. Every seed must still
	// give a playable preview on the default keyboard voice.
	for seed := 1; seed <= 40; seed++ {
		form := generatorForm(csrf, map[string]string{"seed": fmt.Sprint(seed), "structure": "a"})
		if response := post(t, client, address+"/__actions/generate", form, false, address); response.StatusCode != 303 {
			t.Fatalf("seed %d: preview status=%d", seed, response.StatusCode)
		}
		if response := mutate(map[string]string{"operation": "rotate", "seed": fmt.Sprint(seed)}); response.StatusCode != 303 {
			t.Fatalf("seed %d: rotating the default voice returned status=%d", seed, response.StatusCode)
		}
	}
	if len(*edits) != 0 {
		t.Fatal("mutating a preview edited the score")
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
