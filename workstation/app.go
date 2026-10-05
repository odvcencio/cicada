package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"m31labs.dev/cicada/workstation/ui"
	"m31labs.dev/gosx"
	"m31labs.dev/gosx/action"
	"m31labs.dev/gosx/editor"
	"m31labs.dev/gosx/engine"
	"m31labs.dev/gosx/server"
	"m31labs.dev/gosx/session"
)

type studioApp struct {
	backend *backend
	draftMu sync.Mutex
	drafts  map[string]draft
}

func newApp(b *backend) (http.Handler, error) {
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		return nil, err
	}
	cookieName := "cicada-workstation"
	if b != nil {
		// Browser cookies share a host across ports. Isolate simultaneous
		// workspaces by their private service origin, without exposing a key.
		identity := sha256.Sum256([]byte(b.url.String()))
		cookieName += "-" + hex.EncodeToString(identity[:8])
	}
	sessions, err := session.New(hex.EncodeToString(key[:]), session.Options{CookieName: cookieName, AllowInsecure: true, HTTPOnly: true, SameSite: http.SameSiteStrictMode, Encrypt: true})
	if err != nil {
		return nil, err
	}
	s := &studioApp{backend: b}
	app := server.New()
	root := runtimeRoot()
	app.SetPublicDir("")
	if root != "" {
		app.SetRuntimeRoot(root)
		app.SetPublicDir(filepath.Join(root, "public"))
	}
	app.EnableNavigation()
	app.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host := r.Host
			if strings.Contains(host, ":") {
				var splitErr error
				host, _, splitErr = net.SplitHostPort(host)
				if splitErr != nil {
					http.Error(w, "invalid host", 403)
					return
				}
			}
			if host != "localhost" && (net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback()) {
				http.Error(w, "Studio requires a loopback host", 403)
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			next.ServeHTTP(w, r)
		})
	})
	app.Use(sessions.Middleware)
	// The 2 MiB limit covers score forms and applies before CSRF form parsing.
	app.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
			next.ServeHTTP(w, r)
		})
	})
	app.Use(sessions.Protect)
	app.Page("GET /{$}", s.page)
	if b == nil {
		return app.Build(), nil
	}
	for name, handler := range s.actions() {
		app.Mount("POST /__actions/"+name, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			action.ServeHandlerWithOptions(w, r, handler, action.ServeHandlerOptions{MaxBodyBytes: 2 << 20})
		}))
	}
	app.Mount("GET /studio.css", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		_, _ = w.Write(ui.CSS)
	}))
	app.Mount("/editor/", http.StripPrefix("/editor/", editor.AssetHandler()))
	app.Mount("GET /media/takes/{id}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.SetPathValue("id", strings.TrimSuffix(r.PathValue("id"), ".wav"))
		s.takeAudio(w, r)
	}))
	app.Mount("GET /media/exports/{id}", http.HandlerFunc(s.exportAudio))
	app.Mount("GET /api/export", http.HandlerFunc(s.exportStatus))
	for _, path := range []string{"/api/state", "/api/transport", "/api/meters", "/api/audio/config", "/api/takes", "/api/capture", "/api/history", "/api/params"} {
		app.Mount("GET "+path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var data json.RawMessage
			if err := b.call(r.Context(), http.MethodGet, path, nil, &data); err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(data)
		}))
	}
	return app.Build(), nil
}

func (s *studioApp) page(ctx *server.Context) gosx.Node {
	ctx.NoStore()
	if s.backend == nil {
		return gosx.El("p", gosx.Text("Open a score with cicada studio to start the workspace."))
	}
	// Studio is an editing session. GoSX leaves anonymous reads cookie-free;
	// establish state before requesting the CSRF token used by every action.
	if store := session.Current(ctx.Request); store != nil {
		store.Set("workspace", true)
	}
	ctx.SetLanguage("en")
	ctx.AddHead(server.Stylesheet("/studio.css"))
	var view workspace
	if err := s.backend.call(ctx.Request.Context(), http.MethodGet, "/api/workspace", nil, &view); err != nil {
		ctx.SetStatus(http.StatusServiceUnavailable)
		return ui.Panel(ui.PanelProps{ID: "workspace", Title: "Studio unavailable", Description: err.Error()})
	}
	props := ui.ShellProps{Title: view.Filename, Filename: view.Filename, Message: view.Error, HasMessage: view.Error != ""}
	view.DiskSource, view.DiskRevision = view.Source, view.Revision
	if view.Project != nil {
		props.Title = view.Project.Title
		props.Tempo = strconv.FormatFloat(float64(view.Project.TempoMilli)/1000, 'f', -1, 64)
		props.Key = view.Project.Key.Scale
		if int(view.Project.Key.Root) < len(phraseKeys) {
			props.Key = strings.ToUpper(phraseKeys[view.Project.Key.Root]) + " " + props.Key
		}
	}
	ctx.SetMetadata(server.Metadata{Title: server.Title{Absolute: props.Title + " · Cicada Studio"}})
	for name, state := range action.States(ctx.Request) {
		if state.Message() != "" {
			props.Message = state.Message()
			props.HasMessage = true
		}
		if name == "source" && !state.OK() {
			if saved, ok := s.draft(state.Value("draft"), session.Token(ctx.Request)); ok {
				view.Source, view.Revision = saved.Source, saved.Revision
			}
		}
	}
	panel := ctx.Request.URL.Query().Get("panel")
	if panel == "" {
		panel = "session"
	}
	if panel == "code" {
		if saved, ok := s.draft(session.Current(ctx.Request).String("score-draft"), session.Token(ctx.Request)); ok {
			view.Source, view.Revision = saved.Source, saved.Revision
			view.HasDraft = true
			if !props.HasMessage {
				props.Message, props.HasMessage = "Unsaved score draft restored. Save it after correcting the error or conflict.", true
			}
		}
	}
	csrf := session.Token(ctx.Request)
	var state transport
	_ = s.backend.call(ctx.Request.Context(), http.MethodGet, "/api/transport", nil, &state)
	var meters gosx.Node = gosx.Fragment()
	if runtimeRoot() != "" && panel == "mixer" {
		meters = gosx.El("section", gosx.Attrs(gosx.Attr("aria-label", "Master loudness")), ctx.Engine(engine.Config{Name: "CicadaMeters", Kind: engine.KindSurface, MountID: "cicada-meters", Runtime: engine.RuntimeGoWASM, WASMPath: ui.MeterEnginePath, Capabilities: []engine.Capability{engine.CapCanvas, engine.CapFetch}, RequiredCapabilities: []engine.Capability{engine.CapCanvas, engine.CapWASM, engine.CapFetch}}, gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Live output meters require browser WASM and canvas support."))), s.form(view, csrf, "mixer", "loudness-reset", submit("", "", "Reset live loudness")))
	}
	return ui.Shell(props, s.toolbar(view, csrf, panel, state), s.navigation(panel), gosx.El("main", gosx.Attrs(gosx.Attr("id", "workspace")), meters, s.panel(ctx, view, csrf, panel)))
}

func (s *studioApp) form(view workspace, csrf, panel, name string, children ...gosx.Node) gosx.Node {
	return ui.Form(ui.FormProps{Action: "/__actions/" + name, CSRF: csrf, Revision: view.Revision, ReturnTo: "/?panel=" + panel, Class: "action-form"}, children...)
}
func hidden(name, value string) gosx.Node {
	return gosx.El("input", gosx.Attrs(gosx.Attr("type", "hidden"), gosx.Attr("name", name), gosx.Attr("value", value)))
}
func submit(name, value, label string) gosx.Node {
	return gosx.El("button", gosx.Attrs(gosx.Attr("type", "submit"), gosx.Attr("name", name), gosx.Attr("value", value)), gosx.Text(label))
}

func (s *studioApp) toolbar(view workspace, csrf, panel string, t transport) gosx.Node {
	return gosx.El("div", gosx.Attrs(gosx.Attr("class", "toolbar")),
		s.form(view, csrf, panel, "transport", submit("action", "play", "Play"), submit("action", "pause", "Pause"), submit("action", "stop", "Stop"), submit("action", "home", "Return to start")),
		s.form(view, csrf, panel, "undo", submit("", "", "Undo")), s.form(view, csrf, panel, "redo", submit("", "", "Redo")),
		ui.Position(ui.TransportProps{Playing: t.Playing, Bar: t.Bar, Step: t.Step, Backend: t.Backend, Error: t.Error}),
	)
}

func (s *studioApp) navigation(panel string) gosx.Node {
	var links []gosx.Node
	for _, item := range []struct{ key, label string }{{"session", "Session"}, {"patterns", "Patterns"}, {"generator", "Generate"}, {"live", "Live"}, {"mixer", "Mixer"}, {"voices", "Instruments"}, {"code", "Score"}, {"takes", "Takes"}, {"history", "History"}, {"audio", "Audio"}, {"export", "Export"}} {
		attrs := gosx.Attrs(gosx.Attr("href", "/?panel="+item.key))
		if panel == item.key {
			attrs = append(attrs, gosx.Attr("aria-current", "page"))
		}
		links = append(links, gosx.El("a", attrs, gosx.Text(item.label)))
	}
	return gosx.El("nav", gosx.Attrs(gosx.Attr("class", "nav"), gosx.Attr("aria-label", "Workspace")), gosx.Fragment(links...))
}

func failure(err error) gosx.Node {
	return gosx.El("p", gosx.Attrs(gosx.Attr("class", "error"), gosx.Attr("role", "alert")), gosx.Text(fmt.Sprint(err)))
}
