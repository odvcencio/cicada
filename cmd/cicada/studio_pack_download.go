package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"time"

	"m31labs.dev/cicada/host/instrumentpack"
)

func (s *studio) downloadInstrumentPack(w http.ResponseWriter, r *http.Request) {
	var request struct{ CatalogURL, SHA256, ID, Tier string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&request); err != nil {
		studioJSON(w, 400, map[string]string{"error": "invalid pack download request"})
		return
	}
	if !instrumentpack.ValidPath(request.ID) || filepath.Base(request.ID) != request.ID || request.Tier != "" && request.Tier != "hq16" && request.Tier != "lossless" {
		studioJSON(w, 422, map[string]string{"error": "choose a pack and download quality"})
		return
	}
	if request.Tier == "" {
		request.Tier = instrumentpack.DefaultTier
	}
	if len(request.SHA256) != 64 {
		studioJSON(w, 422, map[string]string{"error": "catalog SHA-256 pin required"})
		return
	}
	relative := filepath.Join("packs", request.ID+"-"+request.Tier+"-"+request.SHA256[:16])
	client := &http.Client{Timeout: 10 * time.Minute}
	selected, err := instrumentpack.Download(r.Context(), client, request.CatalogURL, request.SHA256, request.ID, request.Tier, filepath.Join(filepath.Dir(s.path), relative))
	if err != nil {
		studioJSON(w, 422, map[string]string{"error": err.Error()})
		return
	}
	manifest := filepath.ToSlash(filepath.Join(relative, "manifest.json"))
	declaration := fmt.Sprintf("sampler %s { pack = %q sha256 = %q root = c4 voices = 16 }", request.ID, manifest, selected.SHA256)
	studioJSON(w, 200, map[string]string{"manifest": manifest, "sha256": selected.SHA256, "tier": request.Tier, "declaration": declaration})
}
