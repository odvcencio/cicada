package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/render"
	"m31labs.dev/cicada/workstation/collab"
)

const sharedTestScore = `cicada 1
tempo 120
key a minor
// shared: abcdefghijklmnopqrstuvwxyz
instrument tone {
  voice mono {
    let shape = env(gate, 120ms);
    out = sine(pitch) * shape;
  }
}
track lead tone {}
track bass tone {}
pattern lead notes steps=4 { 1 . . . }
pattern bass notes steps=4 { 3 . . . }
scene main { lead=lead bass=bass }
song { main }
`

type sharedTestBackend struct {
	mu               sync.Mutex
	stored           json.RawMessage
	source, revision string
	writes           int
	failStore        bool
}

func sharedTestApp(t *testing.T, state *sharedTestBackend) string {
	t.Helper()
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state.mu.Lock()
		defer state.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/workspace":
			_ = json.NewEncoder(w).Encode(workspace{Source: state.source, Revision: state.revision, Filename: "shared.cicada", Valid: true})
		case "/api/collaboration/store":
			if r.Method == "GET" {
				if state.stored == nil {
					w.WriteHeader(404)
				} else {
					_, _ = w.Write(state.stored)
				}
				return
			}
			if state.failStore {
				w.WriteHeader(503)
				return
			}
			state.stored, _ = io.ReadAll(r.Body)
			_, _ = io.WriteString(w, `{"ok":true}`)
		case "/api/source":
			var request map[string]string
			_ = json.NewDecoder(r.Body).Decode(&request)
			if request["revision"] != state.revision {
				w.WriteHeader(409)
				_, _ = io.WriteString(w, `{"error":"score changed"}`)
				return
			}
			if _, ds := notation.Parse([]byte(request["source"])); len(ds) > 0 {
				w.WriteHeader(422)
				_, _ = io.WriteString(w, `{"error":"invalid score"}`)
				return
			}
			state.writes++
			state.source = request["source"]
			state.revision = fmt.Sprintf("saved-%d", state.writes)
			_ = json.NewEncoder(w).Encode(map[string]string{"revision": state.revision})
		default:
			_, _ = io.WriteString(w, `{}`)
		}
	}))
	t.Cleanup(service.Close)
	b, err := newBackend(service.URL)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newApp(b)
	if err != nil {
		t.Fatal(err)
	}
	app := httptest.NewServer(handler)
	t.Cleanup(app.Close)
	return app.URL
}

func sharedClient(t *testing.T, address string) (*http.Client, string) {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := tokenFromPage(t, getPage(t, client, address+"/?panel=collaboration"))
	return client, csrf
}

func sharedPost(t *testing.T, client *http.Client, address, csrf, path string, payload any) (int, []byte) {
	t.Helper()
	data, _ := json.Marshal(payload)
	request, _ := http.NewRequest("POST", address+path, bytes.NewReader(data))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", csrf)
	request.Header.Set("Origin", address)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	return response.StatusCode, body
}

func sharedGrant(t *testing.T, address string, owner *http.Client, ownerCSRF string, client *http.Client, csrf, role string) {
	t.Helper()
	status, body := sharedPost(t, owner, address, ownerCSRF, "/api/collaboration/invite", map[string]string{"role": role})
	if status != 200 {
		t.Fatalf("invite: %d %s", status, body)
	}
	var invite map[string]string
	_ = json.Unmarshal(body, &invite)
	status, body = sharedPost(t, client, address, csrf, "/api/collaboration/join", invite)
	if status != 200 {
		t.Fatalf("join: %d %s", status, body)
	}
	status, _ = sharedPost(t, client, address, csrf, "/api/collaboration/join", invite)
	if status != 403 {
		t.Fatal("invitation was reusable")
	}
}

func sharedDial(t *testing.T, client *http.Client, address, tab string) (*websocket.Conn, collaborationSnapshot, string) {
	t.Helper()
	u, _ := url.Parse(address)
	header := http.Header{"Origin": {address}}
	var cookies []string
	for _, cookie := range client.Jar.Cookies(u) {
		cookies = append(cookies, cookie.Name+"="+cookie.Value)
	}
	header.Set("Cookie", strings.Join(cookies, "; "))
	conn, response, err := websocket.DefaultDialer.Dial(strings.Replace(address, "http", "ws", 1)+"/collaboration/score?client="+tab, header)
	if err != nil {
		t.Fatalf("dial: %v %+v", err, response)
	}
	t.Cleanup(func() { _ = conn.Close() })
	var snapshot collaborationSnapshot
	actor := ""
	for snapshot.Document == nil || actor == "" {
		event, data := sharedRead(t, conn)
		switch event {
		case "state":
			_ = json.Unmarshal(data, &snapshot)
		case "presence":
			var members []collaborationMember
			_ = json.Unmarshal(data, &members)
			for _, member := range members {
				if strings.HasSuffix(member.Actor, "-"+tab) {
					actor = member.Actor
				}
			}
		}
	}
	return conn, snapshot, actor
}

func sharedRead(t *testing.T, conn *websocket.Conn) (string, json.RawMessage) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	var message struct {
		Event string          `json:"event"`
		Data  json.RawMessage `json:"data"`
	}
	if err := conn.ReadJSON(&message); err != nil {
		t.Fatal(err)
	}
	return message.Event, message.Data
}

func sharedSend(t *testing.T, conn *websocket.Conn, event string, data any) {
	t.Helper()
	if err := conn.WriteJSON(map[string]any{"event": event, "data": data}); err != nil {
		t.Fatal(err)
	}
}

func sharedSync(t *testing.T, conn *websocket.Conn, id string, replica *collab.Replica) {
	t.Helper()
	for len(replica.Pending) > 0 {
		ops := replica.Pending[:min(32, len(replica.Pending))]
		last := ops[len(ops)-1]
		sharedSend(t, conn, "sync", map[string]any{"id": id, "operations": ops})
		for {
			event, data := sharedRead(t, conn)
			if event == "error" {
				t.Fatalf("sync: %s", data)
			}
			if event != "state" {
				continue
			}
			var snapshot collaborationSnapshot
			_ = json.Unmarshal(data, &snapshot)
			seen := false
			for _, op := range snapshot.Document.Operations {
				if op.Actor == last.Actor && op.Seq == last.Seq {
					seen = true
				}
			}
			if err := replica.Receive(snapshot.Document.Operations); err != nil {
				t.Fatal(err)
			}
			if seen {
				break
			}
		}
	}
	sharedSend(t, conn, "sync", map[string]any{"id": id})
	for {
		event, data := sharedRead(t, conn)
		if event == "state" {
			var snapshot collaborationSnapshot
			_ = json.Unmarshal(data, &snapshot)
			if err := replica.Receive(snapshot.Document.Operations); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
}

func TestCollaborationM4ConvergencePCM(t *testing.T) {
	state := &sharedTestBackend{source: sharedTestScore, revision: "initial"}
	address := sharedTestApp(t, state)
	one, csrfOne := sharedClient(t, address)
	two, csrfTwo := sharedClient(t, address)
	sharedGrant(t, address, one, csrfOne, two, csrfTwo, "editor")
	aConn, snapshot, aActor := sharedDial(t, one, address, "0000000000000001")
	bConn, _, bActor := sharedDial(t, two, address, "0000000000000002")
	a := &collab.Replica{Actor: aActor, Document: snapshot.Document.Clone()}
	b := &collab.Replica{Actor: bActor, Document: snapshot.Document.Clone()}
	rng := rand.New(rand.NewSource(20261006))
	for round := 0; round < 25; round++ {
		if round == 5 {
			_ = aConn.Close()
			_ = bConn.Close()
		}
		for edit := 0; edit < 20; edit++ {
			for index, r := range []*collab.Replica{a, b} {
				text, _ := r.Document.Visible()
				start := strings.Index(text, "// shared: ") + len("// shared: ")
				end := start + strings.IndexByte(text[start:], '\n')
				runes := []rune(text[start:end])
				at := rng.Intn(len(runes) + 1)
				count := 0
				if at < len(runes) && rng.Intn(3) == 0 {
					count = 1
				}
				insert := string([]rune("abcXYZ🎵")[rng.Intn(7)])
				if count == 1 && string(runes[at]) == insert {
					insert = "Q"
				}
				updated := text[:start] + string(runes[:at]) + insert + string(runes[at+count:]) + text[end:]
				// Each client also edits its own sounding pattern. Their comment
				// edits overlap while their musical edits remain valid scores.
				if edit == 0 {
					name := []string{"lead", "bass"}[index]
					prefix := "pattern " + name + " notes steps=4 { "
					pos := strings.Index(text, prefix) + len(prefix)
					degree := int(text[pos] - '1')
					updated = text[:pos] + fmt.Sprint(1+(degree+1+rng.Intn(6))%7) + text[pos+1:]
				}
				if err := r.Edit(updated); err != nil {
					t.Fatal(err)
				}
			}
		}
		if round == 14 {
			// Restore a disconnected client exactly as a browser tab reload does.
			data, _ := json.Marshal(a)
			var restored collab.Replica
			if err := json.Unmarshal(data, &restored); err != nil {
				t.Fatal(err)
			}
			a = &restored
			aConn, _, _ = sharedDial(t, one, address, "0000000000000001")
			bConn, _, _ = sharedDial(t, two, address, "0000000000000002")
		}
		if round < 5 || round >= 14 {
			if rng.Intn(2) == 0 {
				sharedSync(t, aConn, snapshot.ID, a)
				sharedSync(t, bConn, snapshot.ID, b)
			} else {
				sharedSync(t, bConn, snapshot.ID, b)
				sharedSync(t, aConn, snapshot.ID, a)
			}
		}
	}
	// Poll for the final union after the last client's publication.
	sharedSync(t, aConn, snapshot.ID, a)
	sharedSync(t, bConn, snapshot.ID, b)
	for index, conn := range []*websocket.Conn{aConn, bConn} {
		r := []*collab.Replica{a, b}[index]
		sharedSend(t, conn, "sync", map[string]any{"id": snapshot.ID})
		for len(r.Document.Operations) < 1001 {
			event, data := sharedRead(t, conn)
			if event != "state" {
				continue
			}
			var latest collaborationSnapshot
			if err := json.Unmarshal(data, &latest); err != nil {
				t.Fatal(err)
			}
			if err := r.Receive(latest.Document.Operations); err != nil {
				t.Fatal(err)
			}
		}
	}
	aText, _ := a.Document.Visible()
	bText, _ := b.Document.Visible()
	if aText != bText {
		t.Fatal("clients did not converge")
	}
	if len(a.Document.Operations) != 1001 {
		t.Fatalf("operations=%d want 1000 edits plus bootstrap", len(a.Document.Operations))
	}
	hash := func(source string) [32]byte {
		score, ds := notation.Parse([]byte(source))
		if len(ds) > 0 {
			t.Fatalf("merged score diagnostics: %+v", ds)
		}
		var wav bytes.Buffer
		report, err := render.WAV(score, render.Options{SampleRate: 48000}, &wav)
		if err != nil {
			t.Fatal(err)
		}
		if report.Peak < 0.01 {
			t.Fatal("render is silent")
		}
		data := wav.Bytes()
		return sha256.Sum256(data[44 : 44+int(report.Frames)*6])
	}
	ha, hb := hash(aText), hash(bText)
	if ha != hb {
		t.Fatal("offline PCM24 hashes differ")
	}
	if ha == hash(sharedTestScore) {
		t.Fatal("musical edits did not change PCM")
	}
	t.Logf("seed=20261006 edits=1000 offline_rounds=10 text_bytes=%d PCM24_SHA256=%x", len(aText), ha)
	status, body := sharedPost(t, one, address, csrfOne, "/api/collaboration/save", collaborationSnapshot{ID: snapshot.ID, Document: a.Document})
	if status != 200 {
		t.Fatalf("save: %d %s", status, body)
	}
	state.mu.Lock()
	saved := state.source
	state.mu.Unlock()
	if saved != aText {
		t.Fatal("saved score differs from converged draft")
	}
}

func TestCollaborationViewerCannotWrite(t *testing.T) {
	state := &sharedTestBackend{source: sharedTestScore, revision: "initial"}
	address := sharedTestApp(t, state)
	owner, ownerCSRF := sharedClient(t, address)
	viewer, csrf := sharedClient(t, address)
	conn, snapshot, actor := sharedDial(t, viewer, address, "0000000000000003")
	doc := snapshot.Document.Clone()
	op, _ := doc.Replace(actor, sharedTestScore+"// forbidden\n")
	sharedSend(t, conn, "sync", map[string]any{"id": snapshot.ID, "role": "editor", "operations": []collab.Operation{op}})
	for {
		event, data := sharedRead(t, conn)
		if event == "error" {
			if !strings.Contains(string(data), "Viewers") {
				t.Fatalf("viewer rejection: %s", data)
			}
			break
		}
	}
	for _, path := range []string{"/api/collaboration/save", "/api/collaboration/invite", "/__actions/source", "/__actions/undo", "/__actions/pattern", "/__actions/revert"} {
		status, _ := sharedPost(t, viewer, address, csrf, path, map[string]any{"id": snapshot.ID, "document": doc, "role": "owner", "content": "forbidden", "revision": "initial"})
		if status != 403 {
			t.Fatalf("viewer path %s status=%d", path, status)
		}
	}
	state.mu.Lock()
	if state.writes != 0 {
		t.Fatal("viewer reached the source writer")
	}
	var saved collaborationSnapshot
	_ = json.Unmarshal(state.stored, &saved)
	state.mu.Unlock()
	if !sameCollaborationDocument(saved.Document, snapshot.Document) {
		t.Fatal("viewer changed durable state")
	}
	// Editors also cannot forge another user's operations or undo targets.
	sharedGrant(t, address, owner, ownerCSRF, viewer, csrf, "editor")
	foreign := op
	foreign.Actor = "foreign"
	sharedSend(t, conn, "sync", map[string]any{"id": snapshot.ID, "operations": []collab.Operation{foreign}})
	for {
		event, data := sharedRead(t, conn)
		if event == "error" {
			if !strings.Contains(string(data), "this user") {
				t.Fatalf("identity rejection: %s", data)
			}
			break
		}
	}
	status, _ := sharedPost(t, viewer, address, csrf, "/api/collaboration/save", collaborationSnapshot{ID: snapshot.ID, Document: doc})
	if status != 409 {
		t.Fatal("stale/unpublished draft could save")
	}
}

func TestCollaborationDurableRestartAndDiskConflict(t *testing.T) {
	state := &sharedTestBackend{source: sharedTestScore, revision: "initial"}
	address := sharedTestApp(t, state)
	client, csrf := sharedClient(t, address)
	conn, snapshot, actor := sharedDial(t, client, address, "0000000000000004")
	r := &collab.Replica{Actor: actor, Document: snapshot.Document.Clone()}
	if err := r.Edit(sharedTestScore + "// retained draft\n"); err != nil {
		t.Fatal(err)
	}
	sharedSync(t, conn, snapshot.ID, r)
	restarted := sharedTestApp(t, state)
	other, _ := sharedClient(t, restarted)
	_, restored, _ := sharedDial(t, other, restarted, "0000000000000005")
	if restored.ID != snapshot.ID || !sameCollaborationDocument(restored.Document, r.Document) {
		t.Fatal("restart lost shared operations")
	}
	state.mu.Lock()
	state.revision = "external"
	state.mu.Unlock()
	status, _ := sharedPost(t, client, address, csrf, "/api/collaboration/save", collaborationSnapshot{ID: snapshot.ID, Document: r.Document})
	if status != 409 {
		t.Fatal("shared save overwrote an external revision")
	}
	state.mu.Lock()
	state.failStore = true
	state.mu.Unlock()
	if err := r.Edit(sharedTestScore + "// retry\n"); err != nil {
		t.Fatal(err)
	}
	sharedSend(t, conn, "sync", map[string]any{"id": snapshot.ID, "operations": r.Pending})
	for {
		event, data := sharedRead(t, conn)
		if event == "error" {
			if !strings.Contains(string(data), "retained") {
				t.Fatal(string(data))
			}
			break
		}
	}
	state.mu.Lock()
	state.failStore = false
	state.mu.Unlock()
	sharedSync(t, conn, snapshot.ID, r)
	// A failed draft receipt after a successful score save must retry only
	// persistence, without executing the source mutation again.
	state.mu.Lock()
	state.revision = snapshot.Revision
	state.failStore = true
	state.mu.Unlock()
	status, body := sharedPost(t, client, address, csrf, "/api/collaboration/save", collaborationSnapshot{ID: snapshot.ID, Document: r.Document})
	if status != 503 || !strings.Contains(string(body), "score was saved") {
		t.Fatalf("save receipt: %d %s", status, body)
	}
	state.mu.Lock()
	state.failStore = false
	state.mu.Unlock()
	sharedSend(t, conn, "sync", map[string]any{"id": snapshot.ID})
	for {
		event, data := sharedRead(t, conn)
		if event != "state" {
			continue
		}
		var latest collaborationSnapshot
		_ = json.Unmarshal(data, &latest)
		if latest.Revision != "saved-1" {
			continue
		}
		// The sync acknowledgment may have been queued by Save. Read the
		// durable receipt under the mock's lock before asserting it.
		state.mu.Lock()
		var stored collaborationSnapshot
		_ = json.Unmarshal(state.stored, &stored)
		writes := state.writes
		state.mu.Unlock()
		if stored.Revision == "saved-1" {
			if writes != 1 {
				t.Fatal("source save was repeated")
			}
			break
		}
	}
}

func TestCollaborationHubRequiresSessionAndOrigin(t *testing.T) {
	state := &sharedTestBackend{source: sharedTestScore, revision: "initial"}
	address := sharedTestApp(t, state)
	_, response, err := websocket.DefaultDialer.Dial(strings.Replace(address, "http", "ws", 1)+"/collaboration/score?client=0000000000000000", http.Header{"Origin": {address}})
	if err == nil || response.StatusCode != 403 {
		t.Fatal("anonymous hub connection accepted")
	}
	response.Body.Close()
	client, _ := sharedClient(t, address)
	u, _ := url.Parse(address)
	header := http.Header{"Origin": {"http://foreign.example"}}
	for _, cookie := range client.Jar.Cookies(u) {
		header.Add("Cookie", cookie.String())
	}
	_, response, err = websocket.DefaultDialer.Dial(strings.Replace(address, "http", "ws", 1)+"/collaboration/score?client=0000000000000000", header)
	if err == nil || response.StatusCode != 403 {
		t.Fatal("foreign-origin hub connection accepted")
	}
	response.Body.Close()
}

func TestCollaborationReadinessDoesNotClaimOwner(t *testing.T) {
	state := &sharedTestBackend{source: sharedTestScore, revision: "initial"}
	address := sharedTestApp(t, state)
	response, err := http.Get(address + "/")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	owner, csrf := sharedClient(t, address)
	status, body := sharedPost(t, owner, address, csrf, "/api/collaboration/invite", map[string]string{"role": "editor"})
	if status != 200 {
		t.Fatalf("readiness request took ownership: %d %s", status, body)
	}
}
