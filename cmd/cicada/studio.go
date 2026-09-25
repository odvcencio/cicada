package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

type studio struct {
	path            string
	mu              sync.Mutex
	lastGoodSource  []byte
	lastGoodProject *project.Project
}

func studioCommand(args []string) error {
	path, address := "main.cicada", "127.0.0.1:0"
	seenPath := false
	for i := 0; i < len(args); i++ {
		if args[i] == "--listen" && i+1 < len(args) {
			address = args[i+1]
			i++
			continue
		}
		if strings.HasPrefix(args[i], "-") || seenPath {
			return fmt.Errorf("usage: cicada studio [score.cicada] [--listen 127.0.0.1:port]")
		}
		path = args[i]
		seenPath = true
	}
	if filepath.Ext(path) != ".cicada" {
		return fmt.Errorf("studio needs a .cicada score")
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil || host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return fmt.Errorf("studio listen address must be loopback host:port")
	}
	handler, err := studioHandler(path)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	defer listener.Close()
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	finished := make(chan error, 1)
	go func() { finished <- server.Serve(listener) }()
	fmt.Printf("Cicada Studio: http://%s/\n", listener.Addr().String())
	select {
	case err = <-finished:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}

func studioHandler(path string) (http.Handler, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	source, err := os.ReadFile(absolute)
	if err != nil {
		return nil, err
	}
	p, err := compileStudioSource(absolute, source)
	if err != nil {
		return nil, err
	}
	s := &studio{path: absolute, lastGoodSource: bytes.Clone(source), lastGoodProject: p}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.page)
	mux.HandleFunc("GET /api/state", s.state)
	mux.HandleFunc("POST /api/source", s.replaceSource)
	mux.HandleFunc("POST /api/toggle", s.toggleStep)
	return mux, nil
}

func studioRevision(source []byte) string {
	hash := sha256.Sum256(source)
	return hex.EncodeToString(hash[:])
}

func (s *studio) page(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	s.mu.Lock()
	source, err := os.ReadFile(s.path)
	if err == nil {
		if p, parseErr := compileStudioSource(s.path, source); parseErr == nil {
			s.lastGoodSource, s.lastGoodProject = bytes.Clone(source), p
		}
	}
	goodSource, p := bytes.Clone(s.lastGoodSource), s.lastGoodProject
	s.mu.Unlock()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var page bytes.Buffer
	if err := writeScorePage(&page, p, string(goodSource), filepath.Base(s.path), true, studioRevision(goodSource)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(page.Bytes())
}

func (s *studio) state(w http.ResponseWriter, r *http.Request) {
	source, err := os.ReadFile(s.path)
	if err != nil {
		studioJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	_, parseErr := compileStudioSource(s.path, source)
	response := map[string]any{"revision": studioRevision(source), "source": string(source), "valid": parseErr == nil}
	if parseErr != nil {
		response["error"] = parseErr.Error()
	}
	studioJSON(w, http.StatusOK, response)
}

type studioEdit struct {
	Revision string `json:"revision"`
	Source   string `json:"source"`
	Pattern  string `json:"pattern"`
	Lane     string `json:"lane"`
	Step     int    `json:"step"`
}

func studioRequest(w http.ResponseWriter, r *http.Request) (studioEdit, bool) {
	var edit studioEdit
	if origin := r.Header.Get("Origin"); origin != "" {
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Host != r.Host || parsed.Scheme != "http" {
			studioJSON(w, http.StatusForbidden, map[string]any{"error": "cross-origin edits are not allowed"})
			return edit, false
		}
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		studioJSON(w, http.StatusUnsupportedMediaType, map[string]any{"error": "expected JSON"})
		return edit, false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&edit); err != nil {
		studioJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return edit, false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		studioJSON(w, http.StatusBadRequest, map[string]any{"error": "expected one JSON request"})
		return edit, false
	}
	if edit.Revision == "" {
		studioJSON(w, http.StatusBadRequest, map[string]any{"error": "missing revision"})
		return edit, false
	}
	return edit, true
}

func (s *studio) replaceSource(w http.ResponseWriter, r *http.Request) {
	edit, ok := studioRequest(w, r)
	if !ok {
		return
	}
	s.apply(w, edit, func([]byte) ([]byte, error) { return []byte(edit.Source), nil })
}

func (s *studio) toggleStep(w http.ResponseWriter, r *http.Request) {
	edit, ok := studioRequest(w, r)
	if !ok {
		return
	}
	s.apply(w, edit, func(source []byte) ([]byte, error) { return toggledSource(source, edit.Pattern, edit.Lane, edit.Step) })
}

func (s *studio) apply(w http.ResponseWriter, edit studioEdit, change func([]byte) ([]byte, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := os.ReadFile(s.path)
	if err != nil {
		studioJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if studioRevision(current) != edit.Revision {
		studioJSON(w, http.StatusConflict, map[string]any{"error": "score changed on disk; reload before saving"})
		return
	}
	updated, err := change(current)
	if err != nil {
		studioJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error()})
		return
	}
	p, err := compileStudioSource(s.path, updated)
	if err != nil {
		studioJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error()})
		return
	}
	latest, err := os.ReadFile(s.path)
	if err != nil {
		studioJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if studioRevision(latest) != edit.Revision {
		studioJSON(w, http.StatusConflict, map[string]any{"error": "score changed during validation; reload before saving"})
		return
	}
	info, err := os.Stat(s.path)
	if err == nil {
		err = writeFixedScore(s.path, updated, info.Mode().Perm())
	}
	if err != nil {
		studioJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	s.lastGoodSource, s.lastGoodProject = bytes.Clone(updated), p
	studioJSON(w, http.StatusOK, map[string]any{"revision": studioRevision(updated), "valid": true})
}

func compileStudioSource(path string, source []byte) (*project.Project, error) {
	if !utf8.Valid(source) {
		return nil, fmt.Errorf("score is not UTF-8")
	}
	if err := checkScoreEdition(path); err != nil {
		return nil, err
	}
	score, diagnostics := notation.Parse(source)
	for _, d := range diagnostics {
		if d.Severity == "error" {
			return nil, fmt.Errorf("%d:%d %s: %s", d.Position.Line, d.Position.Column, d.Code, d.Message)
		}
	}
	p, extra := project.FromScore(score)
	for _, d := range extra {
		if d.Severity == "error" {
			return nil, fmt.Errorf("%d:%d %s: %s", d.Position.Line, d.Position.Column, d.Code, d.Message)
		}
	}
	if p == nil {
		return nil, fmt.Errorf("score did not compile")
	}
	if _, err := project.CompileEngine(p, 48000, 128); err != nil {
		return nil, err
	}
	return p, nil
}

func toggledSource(source []byte, patternID, laneID string, index int) ([]byte, error) {
	if index < 0 || patternID == "" {
		return nil, fmt.Errorf("pattern and nonnegative step are required")
	}
	score, diagnostics := notation.Parse(source)
	if score == nil || hasDiagnosticErrors(diagnostics) {
		return nil, fmt.Errorf("score must validate before a grid edit")
	}
	for _, pattern := range score.Patterns {
		if pattern.Name != patternID {
			continue
		}
		var token notation.StepToken
		isDrum := pattern.Kind == "drums"
		if isDrum {
			for _, lane := range pattern.Lanes {
				if lane.Name == laneID && index < len(lane.Hits) {
					token = lane.Hits[index]
					break
				}
			}
		} else if laneID == "" && index < len(pattern.Steps) {
			token = pattern.Steps[index]
		}
		if token.Position.Line == 0 {
			return nil, fmt.Errorf("pattern %s has no step %d in lane %s", patternID, index+1, laneID)
		}
		start := studioSourceOffset(source, token.Position)
		end := start + len(token.Text)
		if end > len(source) || string(source[start:end]) != token.Text {
			return nil, fmt.Errorf("step source no longer matches the projection")
		}
		replacement := "."
		if token.Text == "." {
			if isDrum {
				replacement = "x"
			} else {
				replacement = "1"
			}
		}
		updated := make([]byte, 0, len(source)-len(token.Text)+len(replacement))
		updated = append(updated, source[:start]...)
		updated = append(updated, replacement...)
		updated = append(updated, source[end:]...)
		return updated, nil
	}
	return nil, fmt.Errorf("unknown pattern %q", patternID)
}

func studioSourceOffset(source []byte, at notation.Position) int {
	line, offset := 1, 0
	for offset < len(source) && line < at.Line {
		if source[offset] == '\n' {
			line++
		}
		offset++
	}
	for column := 1; offset < len(source) && column < at.Column && source[offset] != '\n'; column++ {
		_, size := utf8.DecodeRune(source[offset:])
		offset += size
	}
	return offset
}

func studioJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
