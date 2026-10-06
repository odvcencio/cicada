package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sync"
	"time"

	"m31labs.dev/cicada/workstation/collab"
	"m31labs.dev/cicada/workstation/ui"
	"m31labs.dev/gosx"
	"m31labs.dev/gosx/engine"
	"m31labs.dev/gosx/hub"
	"m31labs.dev/gosx/server"
	"m31labs.dev/gosx/session"
)

type collaborationSnapshot struct {
	ID       string           `json:"id"`
	Revision string           `json:"revision"`
	Document *collab.Document `json:"document"`
}
type collaborationMember struct {
	Actor  string    `json:"actor"`
	Role   string    `json:"role"`
	Cursor collab.ID `json:"cursor"`
}
type collaborationInvite struct {
	Role    string
	Expires time.Time
}
type collaboration struct {
	mu         sync.Mutex
	backend    *backend
	hub        *hub.Hub
	snapshot   collaborationSnapshot
	roles      map[string]string
	owner      string
	storeDirty bool
	invites    map[string]collaborationInvite
	presence   map[string]collaborationMember
}

func collaborationID() string {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		panic("cannot create collaboration identity")
	}
	return hex.EncodeToString(id[:])
}

func newCollaboration(b *backend) *collaboration {
	c := &collaboration{backend: b, hub: hub.New("score"), roles: map[string]string{}, invites: map[string]collaborationInvite{}, presence: map[string]collaborationMember{}}
	c.hub.RequireOrigin = true
	c.hub.MaxClients = 32
	c.hub.MaxMessagesPerSecond = 40
	c.hub.MaxMessageBurst = 80
	c.hub.On("join", func(ctx *hub.Context) {
		c.mu.Lock()
		defer c.mu.Unlock()
		actor, _ := ctx.Client.Metadata("actor")
		user, _ := ctx.Client.Metadata("user")
		role := c.roles[user]
		c.presence[ctx.Client.ID] = collaborationMember{Actor: actor, Role: role}
		c.hub.Send(ctx.Client.ID, "state", c.snapshot)
		c.broadcastPresence()
	})
	c.hub.On("leave", func(ctx *hub.Context) {
		c.mu.Lock()
		defer c.mu.Unlock()
		delete(c.presence, ctx.Client.ID)
		c.broadcastPresence()
	})
	c.hub.On("sync", c.sync)
	c.hub.On("cursor", func(ctx *hub.Context) {
		var cursor collab.ID
		if json.Unmarshal(ctx.Data, &cursor) != nil {
			return
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		if cursor != (collab.ID{}) {
			_, ids := c.snapshot.Document.Visible()
			found := false
			for _, id := range ids {
				if id == cursor {
					found = true
					break
				}
			}
			if !found {
				return
			}
		}
		member := c.presence[ctx.Client.ID]
		member.Cursor = cursor
		c.presence[ctx.Client.ID] = member
		c.broadcastPresence()
	})
	return c
}

// Membership lives on the server. The encrypted session only names a user;
// neither a query parameter nor a WebSocket message can supply their role.
func (c *collaboration) member(r *http.Request, create bool) (string, string) {
	store := session.Current(r)
	if store == nil {
		return "", ""
	}
	actor := store.String("collaboration-user")
	c.mu.Lock()
	defer c.mu.Unlock()
	if actor == "" && create {
		actor = collaborationID()
		store.Set("collaboration-user", actor)
		c.roles[actor] = "viewer"
	}
	// Anonymous readiness requests never claim ownership. An established
	// session claims it when opening Collaborate or making its first action.
	if actor != "" && c.roles[actor] != "" && c.owner == "" && (r.URL.Query().Get("panel") == "collaboration" || !create) {
		c.owner = actor
		c.roles[actor] = "owner"
	}
	return actor, c.roles[actor]
}

func (c *collaboration) initialize(ctx context.Context) error {
	if c.snapshot.Document != nil {
		return nil
	}
	var saved collaborationSnapshot
	err := c.backend.call(ctx, http.MethodGet, "/api/collaboration/store", nil, &saved)
	var failure *backendError
	if err != nil && (!errors.As(err, &failure) || failure.Status != 404) {
		return err
	}
	if err == nil {
		if saved.ID == "" || saved.Revision == "" || saved.Document == nil {
			return fmt.Errorf("invalid saved shared draft")
		}
		doc := &collab.Document{}
		if err := doc.Merge(saved.Document.Operations); err != nil {
			return err
		}
		saved.Document = doc
		c.snapshot = saved
		return nil
	}
	var view workspace
	if err := c.backend.call(ctx, http.MethodGet, "/api/workspace", nil, &view); err != nil {
		return err
	}
	doc, err := collab.New(view.Source)
	if err != nil {
		return err
	}
	saved = collaborationSnapshot{ID: collaborationID(), Revision: view.Revision, Document: doc}
	if err := c.persist(ctx, saved); err != nil {
		return err
	}
	c.snapshot = saved
	return nil
}

func (c *collaboration) persist(ctx context.Context, snapshot collaborationSnapshot) error {
	return c.backend.call(ctx, http.MethodPost, "/api/collaboration/store", snapshot, nil)
}

func (c *collaboration) serveHub(w http.ResponseWriter, r *http.Request) {
	actor, role := c.member(r, false)
	if actor == "" || role == "" {
		http.Error(w, "Open Studio before connecting to the shared draft.", 403)
		return
	}
	c.mu.Lock()
	err := c.initialize(r.Context())
	c.mu.Unlock()
	if err != nil {
		http.Error(w, err.Error(), 503)
		return
	}
	client := r.URL.Query().Get("client")
	if !collaborationClientID.MatchString(client) {
		http.Error(w, "Invalid editor identity.", http.StatusBadRequest)
		return
	}
	c.hub.ServeHTTPWithMetadata(w, r, hub.ConnectionMetadata{"user": actor, "actor": actor + "-" + client})
}

var collaborationClientID = regexp.MustCompile(`^[a-f0-9]{16}$`)

func (c *collaboration) sync(ctx *hub.Context) {
	var request struct {
		ID         string             `json:"id"`
		Operations []collab.Operation `json:"operations"`
	}
	if err := json.Unmarshal(ctx.Data, &request); err != nil {
		c.hub.Send(ctx.Client.ID, "error", "Invalid score edits.")
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	actor, _ := ctx.Client.Metadata("actor")
	user, _ := ctx.Client.Metadata("user")
	if request.ID != c.snapshot.ID {
		c.hub.Send(ctx.Client.ID, "error", "This is a different shared draft. Keep your local text and reopen Studio.")
		return
	}
	if len(request.Operations) == 0 {
		if c.storeDirty {
			ctxStore, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			err := c.persist(ctxStore, c.snapshot)
			cancel()
			if err == nil {
				c.storeDirty = false
			}
		}
		c.hub.Send(ctx.Client.ID, "state", c.snapshot)
		return
	}
	if c.roles[user] != "editor" && c.roles[user] != "owner" {
		c.hub.Send(ctx.Client.ID, "error", "Viewers cannot edit the score.")
		return
	}
	if len(request.Operations) > 32 {
		c.hub.Send(ctx.Client.ID, "error", "Send at most 32 edits at a time.")
		return
	}
	for _, op := range request.Operations {
		if op.Actor != actor {
			c.hub.Send(ctx.Client.ID, "error", "An edit must belong to this user.")
			return
		}
	}
	doc := c.snapshot.Document.Clone()
	if err := doc.Merge(request.Operations); err != nil {
		c.hub.Send(ctx.Client.ID, "error", err.Error())
		return
	}
	next := c.snapshot
	next.Document = doc
	// Acknowledge only durable operations. A retry reuses the same identities.
	ctxStore, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := c.persist(ctxStore, next); err != nil {
		c.hub.Send(ctx.Client.ID, "error", "The shared draft could not be stored. Your local edits are retained.")
		return
	}
	c.snapshot = next
	c.storeDirty = false
	c.hub.Broadcast("state", next)
}

func (c *collaboration) broadcastPresence() {
	members := make([]collaborationMember, 0, len(c.presence))
	for _, member := range c.presence {
		members = append(members, member)
	}
	c.hub.Broadcast("presence", members)
}

func (c *collaboration) invite(w http.ResponseWriter, r *http.Request) {
	_, role := c.member(r, false)
	if role != "owner" {
		http.Error(w, "Only the workspace owner can invite editors.", 403)
		return
	}
	var request struct {
		Role string `json:"role"`
	}
	if json.NewDecoder(r.Body).Decode(&request) != nil || request.Role != "editor" && request.Role != "viewer" {
		http.Error(w, "Choose editor or viewer.", 400)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for token, invite := range c.invites {
		if time.Now().After(invite.Expires) {
			delete(c.invites, token)
		}
	}
	if len(c.invites) >= 64 {
		http.Error(w, "Too many unused invitations.", 429)
		return
	}
	token := collaborationID()
	c.invites[token] = collaborationInvite{request.Role, time.Now().Add(15 * time.Minute)}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"token": token})
}

func (c *collaboration) join(w http.ResponseWriter, r *http.Request) {
	actor, _ := c.member(r, false)
	var request struct {
		Token string `json:"token"`
	}
	if actor == "" || json.NewDecoder(r.Body).Decode(&request) != nil {
		http.Error(w, "Open Studio and enter an invitation.", 403)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	invite, ok := c.invites[request.Token]
	if !ok || time.Now().After(invite.Expires) {
		http.Error(w, "The invitation expired or was already used.", 403)
		return
	}
	if c.roles[actor] == "owner" {
		http.Error(w, "The owner already has edit access.", 400)
		return
	}
	delete(c.invites, request.Token)
	c.roles[actor] = invite.Role
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"role": invite.Role})
}

func (c *collaboration) save(w http.ResponseWriter, r *http.Request) {
	_, role := c.member(r, false)
	if role != "owner" && role != "editor" {
		http.Error(w, "Viewers cannot save the score.", 403)
		return
	}
	var request struct {
		ID       string           `json:"id"`
		Document *collab.Document `json:"document"`
	}
	if json.NewDecoder(r.Body).Decode(&request) != nil || request.Document == nil {
		http.Error(w, "Invalid score draft.", 400)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.snapshot.Document == nil || request.ID != c.snapshot.ID || !sameCollaborationDocument(request.Document, c.snapshot.Document) {
		http.Error(w, "The shared draft changed. Sync before saving.", 409)
		return
	}
	source, _ := c.snapshot.Document.Visible()
	var receipt struct {
		Revision string `json:"revision"`
	}
	if err := c.backend.call(r.Context(), http.MethodPost, "/api/source", map[string]string{"revision": c.snapshot.Revision, "source": source}, &receipt); err != nil {
		status := 503
		var failure *backendError
		if errors.As(err, &failure) {
			status = failure.Status
		}
		http.Error(w, err.Error(), status)
		return
	}
	next := c.snapshot
	next.Revision = receipt.Revision
	c.snapshot = next
	c.hub.Broadcast("state", next)
	if err := c.persist(r.Context(), next); err != nil {
		c.storeDirty = true
		http.Error(w, "The score was saved. Shared draft recovery will retry while Studio stays open.", 503)
		return
	}
	c.storeDirty = false
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"revision": receipt.Revision})
}

func sameCollaborationDocument(a, b *collab.Document) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func (s *studioApp) collaborationPanel(ctx *server.Context) gosx.Node {
	actor, role := s.collaboration.member(ctx.Request, false)
	props, _ := json.Marshal(map[string]string{"actor": actor, "role": role, "csrf": session.Token(ctx.Request)})
	return ui.Panel(ui.PanelProps{ID: "collaboration", Title: "Collaborate", Description: "Edit a shared score draft. Save a valid draft to hear it at the next bar."},
		ctx.Engine(engine.Config{Name: "CicadaCollaboration", Kind: engine.KindSurface, MountID: "cicada-collaboration", Runtime: engine.RuntimeGoWASM, WASMPath: ui.MeterEnginePath, Props: props, Capabilities: []engine.Capability{engine.CapFetch}, RequiredCapabilities: []engine.Capability{engine.CapWASM, engine.CapFetch}},
			gosx.El("p", gosx.Text("Shared editing requires browser WASM support."))))
}
