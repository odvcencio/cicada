// Package demoweb serves only an explicitly selected immutable GoSX build.
// Native Studio, server-side scores, device APIs and export jobs are absent.
package demoweb

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/andybalholm/brotli"
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
	etag         string
	gzip, brotli representation
}

type representation struct {
	bytes []byte
	etag  string
}

func entityTag(data []byte) string {
	sum := sha256.Sum256(data)
	return `"` + hex.EncodeToString(sum[:]) + `"`
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
		sealed := sealedAsset{
			Asset: Asset{ContentType: asset.ContentType, Bytes: bytes.Clone(asset.Bytes)},
			etag:  entityTag(asset.Bytes),
		}
		if strings.HasPrefix(asset.ContentType, "text/") || asset.ContentType == "application/javascript" || asset.ContentType == "application/wasm" {
			var gz, br bytes.Buffer
			gzipWriter, err := gzip.NewWriterLevel(&gz, gzip.BestCompression)
			if err != nil {
				return nil, err
			}
			if _, err := gzipWriter.Write(asset.Bytes); err != nil {
				return nil, err
			}
			if err := gzipWriter.Close(); err != nil {
				return nil, err
			}
			brotliWriter := brotli.NewWriterLevel(&br, 9)
			if _, err := brotliWriter.Write(asset.Bytes); err != nil {
				return nil, err
			}
			if err := brotliWriter.Close(); err != nil {
				return nil, err
			}
			sealed.gzip = representation{bytes: gz.Bytes(), etag: entityTag(gz.Bytes())}
			sealed.brotli = representation{bytes: br.Bytes(), etag: entityTag(br.Bytes())}
		}
		h.assets[urlPath] = sealed
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
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Vary", "Accept-Encoding")
	data, etag := asset.Bytes, asset.etag
	encoding, acceptable := selectEncoding(r.Header.Values("Accept-Encoding"), asset.gzip.bytes != nil)
	if !acceptable {
		http.Error(w, "no acceptable content encoding", http.StatusNotAcceptable)
		return
	}
	if encoding != "" {
		compressed := asset.gzip
		if encoding == "br" {
			compressed = asset.brotli
		}
		data, etag = compressed.bytes, compressed.etag
		w.Header().Set("Content-Encoding", encoding)
	}
	w.Header().Set("ETag", etag)
	if matchesETag(r.Header.Values("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}

// Negotiation prefers Brotli on equal weights. Identity is acceptable unless
// explicitly excluded, including by a zero-weight wildcard.
func selectEncoding(headers []string, compressed bool) (string, bool) {
	quality := map[string]float64{}
	for _, field := range headers {
		for _, part := range strings.Split(field, ",") {
			parts := strings.Split(part, ";")
			name := strings.ToLower(strings.TrimSpace(parts[0]))
			q := 1.0
			for _, parameter := range parts[1:] {
				key, value, ok := strings.Cut(strings.TrimSpace(parameter), "=")
				if ok && strings.EqualFold(key, "q") {
					parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
					if err != nil || !(parsed >= 0 && parsed <= 1) {
						q = 0
					} else {
						q = parsed
					}
				}
			}
			quality[name] = q
		}
	}
	weight := func(name string) float64 {
		if q, ok := quality[name]; ok {
			return q
		}
		if q, ok := quality["*"]; ok {
			if name != "identity" || q == 0 {
				return q
			}
		}
		if name == "identity" {
			return 1
		}
		return 0
	}
	best, bestQuality := "", 0.0
	if compressed {
		for _, name := range []string{"br", "gzip"} {
			if q := weight(name); q > bestQuality {
				best, bestQuality = name, q
			}
		}
	}
	if q := weight("identity"); q > bestQuality {
		best, bestQuality = "", q
	}
	return best, bestQuality > 0
}

func matchesETag(headers []string, etag string) bool {
	for _, field := range headers {
		for _, candidate := range strings.Split(field, ",") {
			candidate = strings.TrimSpace(candidate)
			if candidate == "*" || strings.TrimPrefix(candidate, "W/") == etag {
				return true
			}
		}
	}
	return false
}
