package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"m31labs.dev/cicada/workstation/ui"
	"m31labs.dev/gosx"
	"m31labs.dev/gosx/action"
	"m31labs.dev/gosx/engine"
	"m31labs.dev/gosx/server"
	"m31labs.dev/gosx/session"
)

// A source command and its confirmed UI projection are one response. Native
// forms retain GoSX's redirect-after-POST contract; the mounted GoSX workspace
// uses the framework's incremental tree diff and patch receiver instead.
type workspaceProjection struct {
	HTML            string `json:"html"`
	Revision        string `json:"revision"`
	Location        string `json:"location"`
	Title           string `json:"title"`
	WriteRevision   string `json:"writeRevision,omitempty"`
	Saved           bool   `json:"saved,omitempty"`
	RefreshRequired bool   `json:"refreshRequired,omitempty"`
}

// Keep the native acknowledgment outside the action result: selecting a
// redirect replaces that result, and the subsequent read may see another save.
type workspaceWriteReceipt struct {
	Revision string `json:"revision"`
}

type workspaceWriteReceiptKey struct{}

func (s *studioApp) reactiveWorkspace(ctx *server.Context, view workspace, body gosx.Node) gosx.Node {
	runtime := gosx.Fragment()
	if s.backend != nil && runtimeRoot() != "" {
		// Engine-only pages do not implicitly load the island patch receiver.
		// Register GoSX's own standalone asset through its managed script API.
		ctx.Runtime().ManagedScript("/gosx/patch.js", server.ManagedScriptOptions{Role: server.ManagedScriptRolePatch})
		props, _ := json.Marshal(map[string]string{"revision": view.Revision, "csrf": session.Token(ctx.Request)})
		runtime = ctx.Engine(engine.Config{
			Name: "CicadaWorkspace", Kind: engine.KindSurface,
			MountID: "cicada-workspace-runtime", Runtime: engine.RuntimeGoWASM,
			WASMPath:             ui.MeterEnginePath,
			Props:                props,
			Capabilities:         []engine.Capability{engine.CapFetch},
			RequiredCapabilities: []engine.Capability{engine.CapWASM, engine.CapFetch},
		}, gosx.El("span", gosx.Attrs(gosx.BoolAttr("hidden"))))
	}
	return gosx.El("div", gosx.Attrs(gosx.Attr("id", "cicada-workspace")), body,
		gosx.El("div", gosx.Attrs(gosx.Attr("class", "workspace-feedback")),
			gosx.El("span", gosx.Attrs(gosx.BoolAttr("data-workspace-status"), gosx.Attr("role", "status"), gosx.Attr("aria-live", "polite"))),
			gosx.El("button", gosx.Attrs(gosx.Attr("type", "button"), gosx.BoolAttr("data-workspace-retry"), gosx.BoolAttr("hidden")), gosx.Text("Refresh saved state"))),
		runtime)
}

// Only the root page's local editing context can be projected. Never allow an
// action to turn an untrusted return URL into a server-side request or redirect.
func workspaceLocation(raw string) (string, error) {
	if len(raw) > 8192 {
		return "", fmt.Errorf("workspace location is too long")
	}
	if raw == "" {
		raw = "/"
	}
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" || u.Path != "/" || strings.HasPrefix(raw, "//") {
		return "", fmt.Errorf("workspace location must be the local Studio page")
	}
	return u.String(), nil
}

func (s *studioApp) projection(r *http.Request, location string) (workspaceProjection, error) {
	location, err := workspaceLocation(location)
	if err != nil {
		return workspaceProjection{}, err
	}
	request := r.Clone(r.Context())
	request.URL, _ = url.Parse(location)
	request.Method, request.Body = http.MethodGet, nil
	ctx := &server.Context{Request: request, PageState: *server.NewPageStateForRequest(request)}
	body, view := s.workspaceContent(ctx)
	if view.Revision == "" {
		return workspaceProjection{}, fmt.Errorf("saved workspace is unavailable; refresh saved state to reconnect")
	}
	title := view.Filename
	if view.Project != nil && view.Project.Title != "" {
		title = view.Project.Title
	}
	return workspaceProjection{HTML: gosx.RenderHTML(body), Revision: view.Revision, Location: location, Title: title + " · Cicada Studio"}, nil
}

func (s *studioApp) workspaceProjection(w http.ResponseWriter, r *http.Request) {
	projection, err := s.projection(r, "/?"+r.URL.RawQuery)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(projection)
}

type actionCapture struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (w *actionCapture) Header() http.Header { return w.header }
func (w *actionCapture) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *actionCapture) Write(data []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	return w.body.Write(data)
}

func (s *studioApp) serveAction(w http.ResponseWriter, r *http.Request, name string, handler action.Handler) {
	options := action.ServeHandlerOptions{MaxBodyBytes: 2 << 20}
	if r.Header.Get("X-Cicada-Reactive") != "1" || !action.WantsJSON(r) {
		action.ServeHandlerWithOptions(w, r, handler, options)
		return
	}
	var fields map[string]string
	receipt := &workspaceWriteReceipt{}
	r = r.WithContext(context.WithValue(r.Context(), workspaceWriteReceiptKey{}, receipt))
	capture := &actionCapture{header: make(http.Header)}
	action.ServeHandlerWithOptions(capture, r, func(ctx *action.Context) error {
		if err := actionValues(ctx); err != nil {
			return err
		}
		if _, err := workspaceLocation(ctx.FormData["__cicada_location"]); err != nil {
			return action.Validation(err.Error(), nil, ctx.FormData)
		}
		fields = make(map[string]string, len(ctx.FormData))
		for key, value := range ctx.FormData {
			fields[key] = value
		}
		return handler(ctx)
	}, options)
	var result action.Result
	if err := json.Unmarshal(capture.body.Bytes(), &result); err != nil {
		for name, values := range capture.header {
			w.Header()[name] = values
		}
		w.WriteHeader(capture.status)
		_, _ = w.Write(capture.body.Bytes())
		return
	}
	status := capture.status
	if result.OK {
		location := fields["__cicada_location"]
		// Pattern commands may intentionally select another step or variation.
		// Generic form return targets must not erase the current deep selection.
		followPattern := fields["action"] == "step" || fields["action"] == "pitch" || fields["action"] == "toggle" || fields["action"] == "duplicate"
		if name == "pattern" && followPattern && result.Redirect != "" {
			location = result.Redirect
		}
		projection, err := s.projection(r, location)
		if err != nil {
			// The write succeeded; never encourage a blind POST retry.
			result.Message = "Saved. " + err.Error()
			result.Data, _ = json.Marshal(map[string]any{"saved": true, "refreshRequired": true, "writeRevision": receipt.Revision})
		} else {
			projection.WriteRevision = receipt.Revision
			projection.Saved = receipt.Revision != ""
			projection.RefreshRequired = receipt.Revision != "" && receipt.Revision != projection.Revision
			if projection.RefreshRequired {
				result.Message = "Saved. The score changed before the workspace refreshed."
			}
			result.Data, _ = json.Marshal(projection)
		}
		result.Redirect, result.Values = "", nil
		status = http.StatusOK
	}
	for name, values := range capture.header {
		if name != "Location" && name != "Content-Length" {
			w.Header()[name] = values
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(result)
}
