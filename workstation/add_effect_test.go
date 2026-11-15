package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"m31labs.dev/gosx"
)

func TestAddEffectAction(t *testing.T) {
	var envelope map[string]any
	var receipt string
	refuse := false
	audio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/intents" {
			receipt = r.Header.Get("X-Cicada-Source-Revision")
			_ = json.NewDecoder(r.Body).Decode(&envelope)
			if refuse {
				w.WriteHeader(422)
				_, _ = io.WriteString(w, `{"error":"no room","reason":"chain_full"}`)
				return
			}
			_, _ = io.WriteString(w, `{"revision":"next"}`)
			return
		}
		if r.URL.Path == "/api/mixer" {
			_, _ = io.WriteString(w, `{"effects":[{"name":"comp","kind":"comp"}]}`)
			return
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	defer audio.Close()
	backend, err := newBackend(audio.URL)
	if err != nil {
		t.Fatal(err)
	}
	h, err := newApp(backend)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	page := getPage(t, client, server.URL+"/?panel=mixer")
	form := url.Values{"csrf_token": {tokenFromPage(t, page)}, "revision": {"r1"}, "source_revision": {"all"}, "name": {"glue"}, "effectkind": {"comp"}, "inserton": {"bass"}, "confirmUpgrade": {"on"}}
	response := post(t, client, server.URL+"/__actions/add-effect", form, true, server.URL)
	response.Body.Close()
	if response.StatusCode >= 400 || receipt != "all" || envelope["revision"] != "r1" {
		t.Fatalf("status %d receipt %s envelope %v", response.StatusCode, receipt, envelope)
	}
	intent := envelope["intents"].([]any)[0].(map[string]any)
	if intent["kind"] != "addeffect" || intent["effectkind"] != "comp" || intent["name"] != "glue" || intent["inserton"] != "bass" || intent["confirmupgrade"] != true {
		t.Fatalf("intent %v", intent)
	}
	// An empty name is filled in from the kind, past the names already in use.
	form.Set("name", "")
	response = post(t, client, server.URL+"/__actions/add-effect", form, true, server.URL)
	response.Body.Close()
	if name := envelope["intents"].([]any)[0].(map[string]any)["name"]; response.StatusCode >= 400 || name != "comp2" {
		t.Fatalf("an empty name should become comp2: status %d name %v", response.StatusCode, name)
	}
	form.Set("name", "glue")
	refuse = true
	response = post(t, client, server.URL+"/__actions/add-effect", form, true, server.URL)
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if !strings.Contains(string(body), "no room") || !strings.Contains(string(body), "chain_full") {
		t.Fatalf("refusal %s", body)
	}
}

func TestAddInsertKindsAndReturns(t *testing.T) {
	s := &studioApp{}
	for _, tc := range []struct {
		c      mixerChannel
		want   string
		absent string
	}{
		{mixerChannel{Kind: "track", ID: "bass"}, "drive,eq,comp,deesser,ampcab,chorus", "limiter"},
		{mixerChannel{Kind: "bus", ID: "music"}, "comp", "eq"},
		{mixerChannel{Kind: "bus", ID: "sfx"}, "", "comp"},
		{mixerChannel{Kind: "master", ID: "master"}, "eq,comp,transient,width,limiter,convolution", "drive"},
		{mixerChannel{Kind: "return", ID: "echo"}, "", "comp"},
	} {
		if got := strings.Join(insertKinds(tc.c), ","); got != tc.want {
			t.Errorf("%s: %s", tc.c.ID, got)
		}
		html := gosx.RenderHTML(s.addInsert(workspace{Edition: 1}, "token", tc.c))
		if strings.Contains(html, `value="`+tc.absent+`"`) {
			t.Errorf("unexpected kind: %s", html)
		}
		if tc.want != "" && !strings.Contains(html, `name="confirmUpgrade"`) {
			t.Errorf("confirmation missing: %s", html)
		}
		if tc.c.ID == "sfx" && !strings.Contains(html, "This bus takes no inserts") {
			t.Error(html)
		}
	}
	html := gosx.RenderHTML(s.addReturn(workspace{Edition: 1}, "token", mixerState{}))
	for _, want := range []string{`value="delay"`, `value="tape"`, `value="reverb"`, `name="confirmUpgrade"`, "Add return"} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %s", want)
		}
	}
	// Returns merge by name, so each one needs its own.
	state := mixerState{Effects: []mixerReturn{{Name: "echo", Kind: "tape"}}}
	for i := 0; i < 7; i++ {
		state.Effects = append(state.Effects, mixerReturn{Name: "room" + strconv.Itoa(i), Kind: "reverb"})
	}
	html = gosx.RenderHTML(s.addReturn(workspace{}, "token", state))
	if !strings.Contains(html, "Delay or tape return is full") || !strings.Contains(html, "Reverb returns are full") || strings.Contains(html, "<select") {
		t.Error(html)
	}
}
