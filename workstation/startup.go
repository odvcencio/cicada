package main

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"fmt"
	"net/http"
	"time"
)

//go:embed assets/studio-startup.js
var startupScript []byte

func serveStartupScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("ETag", fmt.Sprintf(`"%x"`, sha256.Sum256(startupScript)))
	http.ServeContent(w, r, "studio-startup.js", time.Time{}, bytes.NewReader(startupScript))
}
