package main

import (
	"net/url"
	"testing"
)

func TestClipActionUsesSourceFramesAndRevision(t *testing.T) {
	address, client, edits := testApp(t)
	csrf := tokenFromPage(t, getPage(t, client, address+"/?panel=code"))
	form := url.Values{"csrf_token": {csrf}, "revision": {"current"}, "action": {"clip-settings"}, "pattern": {"take-a"}, "start": {"480"}, "end": {"48000"}, "gainDB": {"-3.25"}, "fadeIn": {"48"}, "fadeOut": {"240"}, "__gosx_return_to": {"/?panel=takes&clip=take-a"}}
	response := post(t, client, address+"/__actions/clip", form, false, address)
	if response.StatusCode != 303 || response.Header.Get("Location") != "/?panel=takes&clip=take-a" || len(*edits) != 1 {
		t.Fatalf("clip action: %d %s", response.StatusCode, response.Header.Get("Location"))
	}
	settings := (*edits)[0]["clipSettings"].(map[string]any)
	if settings["start"] != float64(480) || settings["gainDB"] != -3.25 || (*edits)[0]["revision"] != "current" {
		t.Fatalf("clip payload %+v", *edits)
	}
	form.Set("start", "1.5")
	response = post(t, client, address+"/__actions/clip", form, true, address)
	if response.StatusCode != 422 || len(*edits) != 1 {
		t.Fatal("fractional frame reached native audio service")
	}
	form.Set("start", "480")
	form.Set("revision", "old")
	response = post(t, client, address+"/__actions/clip", form, true, address)
	if response.StatusCode != 409 {
		t.Fatal("stale clip edit accepted")
	}
}
