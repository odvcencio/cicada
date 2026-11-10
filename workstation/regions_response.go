package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"m31labs.dev/gosx/server"
)

// An edit response and GET /__studio/regions carry the update regions the
// browser shows whose content changed, and name those that did not.

// maxRegionRequest bounds the region list a request may name.
const maxRegionRequest = 32

type regionRequest struct{ id, digest string }

// parseRegionRequest reads "id:digest,id:digest". Every id must be a shell
// region; the digest is what the browser shows, or empty when it shows none.
func parseRegionRequest(raw string) ([]regionRequest, error) {
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) > maxRegionRequest {
		return nil, fmt.Errorf("too many update regions")
	}
	seen := map[string]bool{}
	out := make([]regionRequest, 0, len(parts))
	for _, part := range parts {
		id, digest, _ := strings.Cut(part, ":")
		if !knownRegion(id) || seen[id] || len(digest) > 64 {
			return nil, fmt.Errorf("unknown update region %q", id)
		}
		seen[id] = true
		out = append(out, regionRequest{id, digest})
	}
	return out, nil
}

func knownRegion(id string) bool {
	for _, known := range shellRegionIDs {
		if id == known {
			return true
		}
	}
	return false
}

// regionHTML is one region the browser shows whose content changed.
type regionHTML struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
	HTML   string `json:"html"`
}

// shellRegions reads the workspace once for a /studio location and renders the
// requested regions. A region whose digest matches is reported unchanged: the
// server rendered it at this revision, so the browser may advance its revision
// fields (rule R1).
func (s *studioApp) shellRegions(r *http.Request, location string, requested []regionRequest) (workspaceProjection, error) {
	location, err := workspaceLocation(location)
	if err != nil {
		return workspaceProjection{}, err
	}
	request := r.Clone(r.Context())
	request.URL, _ = url.Parse(location)
	if request.URL.Path != "/studio" {
		return workspaceProjection{}, fmt.Errorf("update regions belong to the Studio shell")
	}
	request.Method, request.Body = http.MethodGet, nil
	ctx := &server.Context{Request: request, PageState: *server.NewPageStateForRequest(request)}
	page, _, ok := s.loadShell(ctx)
	if !ok || page.view.Revision == "" {
		return workspaceProjection{}, fmt.Errorf("saved workspace is unavailable; refresh saved state to reconnect")
	}
	view := page.view
	title := view.Filename
	if view.Project != nil && view.Project.Title != "" {
		title = view.Project.Title
	}
	projection := workspaceProjection{ProjectRevision: view.ProjectRevision, SourceRevision: view.SourceRevision, FileRevision: page.codeView.FileRevision, Revision: view.Revision, Location: location, Title: title + " · Cicada Studio", Regions: []regionHTML{}, Unchanged: []string{}}
	for _, want := range requested {
		body, digest, ok := page.renderRegion(want.id)
		if !ok {
			continue
		}
		if digest == want.digest {
			projection.Unchanged = append(projection.Unchanged, want.id)
			continue
		}
		projection.Regions = append(projection.Regions, regionHTML{ID: want.id, Digest: digest, HTML: body})
	}
	return projection, nil
}

// serveShellRegions answers GET /__studio/regions?ids=id:digest,…&location=…:
// the same region body an edit returns, without a write. The page uses it for
// stale regions, revealed regions and "Refresh saved state".
func (s *studioApp) serveShellRegions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	requested, err := parseRegionRequest(r.URL.Query().Get("ids"))
	if err != nil || len(requested) == 0 {
		http.Error(w, "name the update regions to refresh", http.StatusBadRequest)
		return
	}
	location, err := workspaceLocation(r.URL.Query().Get("location"))
	if err == nil && !strings.HasPrefix(location, "/studio") {
		err = fmt.Errorf("update regions belong to the Studio shell")
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	projection, err := s.shellRegions(r, location, requested)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(projection)
}
