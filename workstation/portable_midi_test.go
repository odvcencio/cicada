package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"
)

func TestPortableMIDILearnChecksProjectRevisionAndOwner(t *testing.T) {
	var seen []struct {
		SourceRevision, Revision, Port, Target string
		Channel, Number                        int
	}
	audio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/workspace":
			_, _ = w.Write([]byte(`{"revision":"r1","source_revision":"s1","valid":false}`))
		case "/api/midi/ports":
			_, _ = w.Write([]byte(`{"ports":{"keys":"Synthetic Keys"}}`))
		case "/api/intents":
			var body struct {
				Revision string `json:"revision"`
				Intents  []struct {
					Port    string `json:"port"`
					Target  string `json:"target"`
					Channel int    `json:"channel"`
					Number  int    `json:"number"`
				} `json:"intents"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Intents) != 1 {
				t.Errorf("invalid MIDI intent: %+v, %v", body, err)
				w.WriteHeader(400)
				return
			}
			x := body.Intents[0]
			seen = append(seen, struct {
				SourceRevision, Revision, Port, Target string
				Channel, Number                        int
			}{r.Header.Get("X-Cicada-Source-Revision"), body.Revision, x.Port, x.Target, x.Channel, x.Number})
			if body.Revision != "r1" || r.Header.Get("X-Cicada-Source-Revision") != "s1" {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"error":"score changed"}`))
				return
			}
			_, _ = w.Write([]byte(`{"revision":"r2","valid":true}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer audio.Close()
	b, err := newBackend(audio.URL)
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
	client := &http.Client{Jar: jar}
	csrf := tokenFromPage(t, getPage(t, client, app.URL+"/?panel=live"))
	values := map[string]string{"revision": "r1", "source_revision": "s1", "port": "keys", "message": "cc", "channel": "2", "number": "74", "target": "bass.cutoff"}
	post := func(token string) *http.Response {
		data, _ := json.Marshal(values)
		req, _ := http.NewRequest("POST", app.URL+"/__actions/midi-learn", bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Origin", app.URL)
		req.Header.Set("X-CSRF-Token", token)
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { response.Body.Close() })
		return response
	}
	if response := post(""); response.StatusCode != 403 || len(seen) != 0 {
		t.Fatal("CSRF allowed", response.StatusCode)
	}
	if response := post(csrf); response.StatusCode != 200 {
		t.Fatalf("learn: %d", response.StatusCode)
	}
	if len(seen) != 1 || seen[0].SourceRevision != "s1" || seen[0].Port != "keys" || seen[0].Target != "bass.cutoff" || seen[0].Channel != 2 || seen[0].Number != 74 {
		t.Fatalf("intent lost identity: %+v", seen)
	}
	values["revision"] = "stale"
	if response := post(csrf); response.StatusCode != 409 {
		t.Fatalf("stale edit: %d", response.StatusCode)
	}
	response, err := client.Get(app.URL + "/api/midi/ports")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("owner port read: %d", response.StatusCode)
	}
}

func TestPortableMIDIBindUsesOwnerAndCSRF(t *testing.T) {
	var writes int
	audio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/midi/ports" || r.Method != "POST" {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		var input map[string]string
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input["port"] != "keys" || input["device"] != "Keyboard" || input["revision"] != "r1" {
			t.Errorf("invalid binding: %v %v", input, err)
			w.WriteHeader(400)
			return
		}
		writes++
		_, _ = w.Write([]byte(`{"ports":{"keys":"Keyboard"},"configRevision":"r2"}`))
	}))
	defer audio.Close()
	b, err := newBackend(audio.URL)
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
	client := &http.Client{Jar: jar}
	csrf := tokenFromPage(t, getPage(t, client, app.URL+"/?panel=live"))
	post := func(token string) *http.Response {
		body := bytes.NewBufferString(`{"port":"keys","device":"Keyboard","revision":"r1"}`)
		req, _ := http.NewRequest("POST", app.URL+"/__actions/midi-bind", body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Origin", app.URL)
		req.Header.Set("X-CSRF-Token", token)
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { response.Body.Close() })
		return response
	}
	if response := post(""); response.StatusCode != 403 || writes != 0 {
		t.Fatalf("CSRF allowed: %d", response.StatusCode)
	}
	if response := post(csrf); response.StatusCode != 200 || writes != 1 {
		t.Fatalf("binding failed: %d, writes %d", response.StatusCode, writes)
	}
}
