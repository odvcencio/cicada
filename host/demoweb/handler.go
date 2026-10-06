// Package demoweb serves only an explicitly selected immutable GoSX build.
// Native Studio, server-side scores, device APIs and export jobs are absent.
package demoweb

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
)

const PublicHost = "cicada.m31labs.dev"
const MaxBuildBytes = 32 << 20

type Asset struct {
	ContentType string
	Bytes       []byte
}

// Build must come from the reviewed GoSX build manifest, never from a walk of
// a working directory. Paths are public URL paths, not host filesystem paths.
// This type intentionally has no fs.FS, disk root or fallback HTTP handler.
type Build struct {
	Revision string
	Assets   map[string]Asset
	// Host defaults to PublicHost. An explicit loopback host:port is permitted
	// for preview/tests. Arbitrary public hosts are rejected.
	Host string
	// InlineScriptHashes are CSP sha256 hashes extracted from the exact GoSX
	// output. External scripts need no hash. Never accept unsafe-inline/eval.
	InlineScriptHashes []string
}

type sealedAsset struct {
	Asset
	etag string
}

type handler struct {
	host, revision, csp string
	assets              map[string]sealedAsset
}

var revisionPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
var hashPattern = regexp.MustCompile(`^sha256-[A-Za-z0-9+/]{43}=$`)

func NewHandler(build Build) (http.Handler, error) {
	if !revisionPattern.MatchString(build.Revision) {
		return nil, errors.New("CICADA-DEMO: full source commit required")
	}
	host := build.Host
	if host == "" {
		host = PublicHost
	}
	if host != PublicHost && !loopbackHost(host) {
		return nil, errors.New("CICADA-DEMO: unsupported public host")
	}
	h := &handler{host: host, revision: build.Revision, assets: make(map[string]sealedAsset, len(build.Assets))}
	total := 0
	for urlPath, asset := range build.Assets {
		if !safeAsset(urlPath, asset.ContentType) || len(asset.Bytes) == 0 || len(asset.Bytes) > MaxBuildBytes-total {
			return nil, errors.New("CICADA-DEMO: invalid or oversized build asset")
		}
		total += len(asset.Bytes)
		sum := sha256.Sum256(asset.Bytes)
		h.assets[urlPath] = sealedAsset{
			Asset: Asset{ContentType: asset.ContentType, Bytes: bytes.Clone(asset.Bytes)},
			etag:  `"` + hex.EncodeToString(sum[:]) + `"`,
		}
	}
	if _, ok := h.assets["/"]; !ok {
		return nil, errors.New("CICADA-DEMO: GoSX entry document required")
	}
	scripts := "'self' 'wasm-unsafe-eval'"
	for _, hash := range build.InlineScriptHashes {
		if !hashPattern.MatchString(hash) {
			return nil, errors.New("CICADA-DEMO: invalid GoSX inline script hash")
		}
		scripts += " '" + hash + "'"
	}
	h.csp = "default-src 'none'; script-src " + scripts + "; style-src 'self' 'unsafe-inline'; " +
		"connect-src 'self'; worker-src 'self' blob:; media-src 'self' blob:; img-src 'self' data:; " +
		"font-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'; object-src 'none'; " +
		"sandbox allow-scripts allow-same-origin"
	return h, nil
}

func loopbackHost(address string) bool {
	host, port, err := net.SplitHostPort(address)
	if err != nil || port == "" {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func safeAsset(urlPath, contentType string) bool {
	if urlPath == "/" {
		return contentType == "text/html; charset=utf-8"
	}
	if path.Clean(urlPath) != urlPath || strings.ContainsAny(urlPath, "%\\?#\x00") || strings.Contains(urlPath, "/.") {
		return false
	}
	if !strings.HasPrefix(urlPath, "/assets/") && !strings.HasPrefix(urlPath, "/audio/") && !strings.HasPrefix(urlPath, "/_gosx/") {
		return false
	}
	// Only immutable build/runtime/playback assets are admitted. In particular,
	// a score, project JSON, archive or MIDI file cannot become a download by
	// adding it to the asset map. Bundled WAVs are allowed for actual playback.
	switch contentType {
	case "application/wasm":
		return strings.HasSuffix(urlPath, ".wasm")
	case "application/javascript":
		return strings.HasSuffix(urlPath, ".js")
	case "text/css":
		return strings.HasSuffix(urlPath, ".css")
	case "audio/wav":
		return strings.HasSuffix(urlPath, ".wav")
	case "image/png":
		return strings.HasSuffix(urlPath, ".png")
	case "image/webp":
		return strings.HasSuffix(urlPath, ".webp")
	case "font/woff2":
		return strings.HasSuffix(urlPath, ".woff2")
	default:
		return false
	}
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
	w.Header().Set("Content-Security-Policy", h.csp)
	w.Header().Set("Permissions-Policy", "microphone=(self), midi=(self), camera=(), display-capture=(), geolocation=()")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Cicada-Revision", h.revision)
	if r.Host != h.host {
		http.Error(w, "unsupported demo host", http.StatusForbidden)
		return
	}
	if r.URL.RawPath != "" || path.Clean(r.URL.Path) != r.URL.Path || strings.ContainsAny(r.URL.Path, "%\\\x00") {
		http.NotFound(w, r)
		return
	}
	// Reject all native/agent/RPC APIs, regardless of method or payload. New
	// native routes cannot accidentally become public after a future rebase.
	for _, prefix := range []string{"/api", "/agent", "/mcp", "/exports", "/download", "/_gosx/rpc", "/_gosx/action"} {
		if r.URL.Path == prefix || strings.HasPrefix(r.URL.Path, prefix+"/") {
			http.Error(w, "unavailable in public demo", http.StatusForbidden)
			return
		}
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "immutable demo assets only", http.StatusMethodNotAllowed)
		return
	}
	asset, ok := h.assets[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", asset.ContentType)
	w.Header().Set("ETag", asset.etag)
	if r.Header.Get("If-None-Match") == asset.etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(asset.Bytes)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(asset.Bytes)
	}
}
