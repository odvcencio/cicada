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

	"m31labs.dev/cicada/edition"
	"m31labs.dev/cicada/host/capture"
	"m31labs.dev/cicada/host/recording"
	"m31labs.dev/cicada/host/takejournal"
	"m31labs.dev/cicada/internal/audiobackend"
	"m31labs.dev/cicada/lsp"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

type studio struct {
	recordedInstruments map[string]*recording.Pack
	takes               *takejournal.Store
	captureID           string
	captureRecorder     *capture.Recorder
	path                string
	mu                  sync.Mutex
	lastGoodSource      []byte
	lastGoodProject     *project.Project
	transport           *studioTransport
	history             *studioHistory
	exports             *studioExportController
}

func studioCommand(args []string) error {
	args, backendName, _, err := selectCommandAudio("studio", args)
	if err != nil {
		return err
	}
	restoreThreads := raiseAudioProcessThreads(backendName)
	defer restoreThreads()
	path, address := "main.cicada", "127.0.0.1:0"
	seenPath := false
	lspStdio := false
	audioNull := backendName == audiobackend.Null
	for i := 0; i < len(args); i++ {
		if args[i] == "--lsp-stdio" {
			lspStdio = true
			continue
		}
		if args[i] == "--listen" {
			if i+1 >= len(args) {
				return fmt.Errorf("usage: cicada studio [score.cicada] [--listen 127.0.0.1:port] [--audio tymbal|oto|null] [--lsp-stdio]")
			}
			address = args[i+1]
			i++
			continue
		}
		if strings.HasPrefix(args[i], "-") || seenPath {
			return fmt.Errorf("usage: cicada studio [score.cicada] [--listen 127.0.0.1:port] [--audio tymbal|oto|null] [--lsp-stdio]")
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
	studio, err := newStudioWithInvalid(path, lspStdio)
	if err != nil {
		return err
	}
	defer func() {
		if err := studio.shutdown(); err != nil {
			fmt.Fprintln(os.Stderr, "Cicada take recovery:", err)
		}
	}()
	studio.transport.audioNull = audioNull
	studio.transport.audioBackend = string(backendName)
	studio.transport.audioOptions = defaultStudioAudioOptionsFor(backendName)
	handler := studio.routes()
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	defer listener.Close()
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if lspStdio {
		// Stdout is the LSP wire in this mode. The extension reads the Studio
		// address from stderr and shows the same views as the browser frame.
		go func() {
			if err := lsp.Serve(os.Stdin, os.Stdout); err != nil {
				fmt.Fprintln(os.Stderr, "Cicada LSP:", err)
			}
			stop()
		}()
	}
	go studio.watchHistory(ctx)
	finished := make(chan error, 1)
	go func() { finished <- server.Serve(listener) }()
	addressLine := fmt.Sprintf("Cicada Studio: http://%s/\n", listener.Addr().String())
	if lspStdio {
		fmt.Fprint(os.Stderr, addressLine)
	} else {
		fmt.Print(addressLine)
	}
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
	s, err := newStudio(path)
	if err != nil {
		return nil, err
	}
	return s.routes(), nil
}

func newStudio(path string) (*studio, error) {
	return newStudioWithInvalid(path, false)
}

func newStudioWithInvalid(path string, allowInvalid bool) (*studio, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, err
	}
	if err := refuseMultiFileStudio(absolute); err != nil {
		return nil, err
	}
	s := &studio{path: absolute}
	opened := false
	defer func() {
		if !opened && s.takes != nil {
			s.takes.Close()
		}
	}()
	if err = s.recoverTakesOnOpen(); err != nil {
		return nil, err
	}
	source, err := os.ReadFile(absolute)
	if err != nil {
		return nil, err
	}
	p, err := compileStudioSource(absolute, source)
	if err != nil && !allowInvalid {
		return nil, err
	}
	history := newStudioHistory(source)
	transport := newStudioTransport(absolute)
	transport.history = history
	s.lastGoodSource, s.lastGoodProject, s.transport, s.history, s.exports = bytes.Clone(source), p, transport, history, newStudioExportController()
	opened = true
	return s, nil
}

func (s *studio) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.page)
	mux.HandleFunc("GET /api/state", s.state)
	mux.HandleFunc("GET /api/takes", s.takeState)
	mux.HandleFunc("POST /api/takes", s.takeCommand)
	mux.HandleFunc("POST /api/source", s.replaceSource)
	mux.HandleFunc("POST /api/toggle", s.toggleStep)
	mux.HandleFunc("POST /api/record", s.recordTake)
	mux.HandleFunc("POST /api/instrument-record", s.instrumentRecord)
	mux.HandleFunc("POST /api/instrument-audition", s.instrumentAudition)
	mux.HandleFunc("GET /assets/recorded/{pack}/{file}", s.instrumentPackAsset)
	mux.HandleFunc("GET /studio-instrument-record.js", s.instrumentRecordScript)
	mux.HandleFunc("POST /api/song", s.editSong)
	mux.HandleFunc("POST /api/undo", s.undo)
	mux.HandleFunc("POST /api/redo", s.redo)
	mux.HandleFunc("POST /api/history/{id}/revert", s.revertHistory)
	mux.HandleFunc("GET /api/mixer", s.mixerState)
	mux.HandleFunc("POST /api/mixer", s.editMixer)
	mux.HandleFunc("POST /api/transport", s.transportCommand)
	mux.HandleFunc("GET /api/transport", s.transportState)
	mux.HandleFunc("POST /api/transport/browser-audio", s.browserAudioStatus)
	mux.HandleFunc("GET /api/transport/ws", s.transportSocket)
	mux.HandleFunc("GET /api/params", s.params)
	mux.HandleFunc("GET /api/audio/ws", s.audioSocket)
	mux.HandleFunc("GET /api/audio/config", s.audioConfig)
	mux.HandleFunc("POST /api/audio/config", s.audioConfig)
	mux.HandleFunc("GET /studio-workspace.js", s.workspaceScript)
	mux.HandleFunc("GET /studio-audio.js", s.audioScript)
	mux.HandleFunc("GET /studio-audio-devices.js", s.audioDeviceScript)
	mux.HandleFunc("GET /studio-midi.js", s.midiScript)
	mux.HandleFunc("GET /studio-live.js", s.liveScript)
	mux.HandleFunc("GET /studio-master.js", s.masterScript)
	mux.HandleFunc("GET /studio-mix.js", s.mixScript)
	mux.HandleFunc("GET /api/export", s.exportStatus)
	mux.HandleFunc("POST /api/export", s.startExport)
	mux.HandleFunc("GET /api/history", s.historyState)
	mux.HandleFunc("GET /api/kernel-image", s.kernelImage)
	mux.HandleFunc("GET /api/kernel.wasm", s.kernelWASM)
	mux.HandleFunc("GET /audio/cicada-processor.js", s.processorAsset)
	mux.HandleFunc("GET /audio/cicada-client.js", s.clientAsset)
	mux.HandleFunc("GET /audio/cicada-capture.js", s.captureAdapterAsset)
	mux.HandleFunc("GET /audio/cicada-capture-processor.js", s.captureProcessorAsset)
	mux.HandleFunc("GET /audio/cicada-capture-worker.js", s.captureWorkerAsset)
	mux.HandleFunc("GET /audio/cicada-capture-client.js", s.captureClientAsset)
	mux.HandleFunc("GET /studio-capture.js", s.captureUIScript)
	mux.HandleFunc("GET /studio-history.js", s.historyScript)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !studioLoopbackHost(r.Host) {
			http.Error(w, "Studio requires a loopback host", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func studioLoopbackHost(address string) bool {
	host := address
	if strings.Contains(address, ":") {
		var err error
		host, _, err = net.SplitHostPort(address)
		if err != nil {
			return false
		}
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
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
		state := s.transport.snapshot()
		bar, step := historyPosition(state)
		s.history.observe(source, bar, step)
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
	if p == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = io.WriteString(w, studioWaitingPage)
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
	s.observeHistory()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := studioRecoveryConflict(s.path); err != nil {
		studioJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
		return
	}
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
	Capture        *studioBrowserTake    `json:"capture,omitempty"`
	Sample         *studioSampleRequest  `json:"sample,omitempty"`
	TakeID         string                `json:"takeId,omitempty"`
	Scene          string                `json:"scene,omitempty"`
	Revision       string                `json:"revision"`
	Label          string                `json:"label,omitempty"`
	Action         string                `json:"action,omitempty"`
	Index          int                   `json:"index,omitempty"`
	Target         int                   `json:"target,omitempty"`
	Bars           int                   `json:"bars,omitempty"`
	Source         string                `json:"source"`
	Pattern        string                `json:"pattern"`
	Track          string                `json:"track,omitempty"`
	Count          int                   `json:"count,omitempty"`
	Take           []studioTakeNote      `json:"take,omitempty"`
	Recordings     []studioTakeRecording `json:"recordings,omitempty"`
	PatternCount   int                   `json:"patternCount,omitempty"`
	Lane           string                `json:"lane"`
	Step           int                   `json:"step"`
	Pitch          *int                  `json:"pitch,omitempty"`
	Modifier       string                `json:"modifier,omitempty"`
	Path           string                `json:"path,omitempty"`
	Value          json.RawMessage       `json:"value,omitempty"`
	ConfirmUpgrade bool                  `json:"confirmUpgrade,omitempty"`
}

func (s *studio) editSong(w http.ResponseWriter, r *http.Request) {
	edit, ok := studioRequest(w, r)
	if !ok {
		return
	}
	if edit.Action != "move" && edit.Action != "bars" {
		studioJSON(w, http.StatusBadRequest, map[string]any{"error": "song action must be move or bars"})
		return
	}
	s.apply(w, edit, func(source []byte) ([]byte, error) {
		return editedSongSource(source, edit.Action, edit.Index, edit.Target, edit.Bars)
	})
}

func studioRequest(w http.ResponseWriter, r *http.Request) (studioEdit, bool) {
	return studioRequestLimit(w, r, 2<<20)
}

func studioRequestLimit(w http.ResponseWriter, r *http.Request, limit int64) (studioEdit, bool) {
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
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
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
	if edit.Pitch != nil && edit.Modifier != "" {
		studioJSON(w, http.StatusBadRequest, map[string]any{"error": "choose one grid edit per request"})
		return
	}
	if edit.Pitch != nil {
		s.apply(w, edit, func(source []byte) ([]byte, error) {
			return pitchedSource(source, edit.Pattern, edit.Lane, edit.Step, *edit.Pitch)
		})
		return
	}
	if edit.Modifier != "" {
		if edit.Modifier == "ratchet" || edit.Modifier == "chance" {
			s.apply(w, edit, func(source []byte) ([]byte, error) {
				return cycledStepSource(source, edit.Pattern, edit.Lane, edit.Step, edit.Modifier)
			})
			return
		}
		s.apply(w, edit, func(source []byte) ([]byte, error) {
			return toggledModifierSource(source, edit.Pattern, edit.Lane, edit.Step, edit.Modifier)
		})
		return
	}
	s.apply(w, edit, func(source []byte) ([]byte, error) { return toggledSource(source, edit.Pattern, edit.Lane, edit.Step) })
}

func (s *studio) apply(w http.ResponseWriter, edit studioEdit, change func([]byte) ([]byte, error)) {
	s.applyWithHook(w, edit, change, nil)
}

func (s *studio) applyWithHook(w http.ResponseWriter, edit studioEdit, change func([]byte) ([]byte, error), beforeSwap func()) {
	s.applyWithResult(w, edit, func(source []byte) (studioMutation, error) {
		updated, err := change(source)
		return studioMutation{Source: updated}, err
	}, beforeSwap)
}

type studioMutation struct {
	Source        []byte
	Response      map[string]any
	HistoryDetail string
	Files         []studioAuxiliaryFile
}

type studioAuxiliaryFile struct {
	Path   string
	Before []byte
	After  []byte
	Mode   os.FileMode
}

func (s *studio) applyWithResult(w http.ResponseWriter, edit studioEdit, change func([]byte) (studioMutation, error), beforeSwap func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := studioRecoveryConflict(s.path); err != nil {
		studioJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
		return
	}
	current, err := os.ReadFile(s.path)
	if err != nil {
		studioJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if s.history != nil {
		bar, step := s.historyPosition()
		s.history.observe(current, bar, step)
	}
	if studioRevision(current) != edit.Revision {
		studioJSON(w, http.StatusConflict, map[string]any{"error": "score changed on disk; reload before saving"})
		return
	}
	mutation, err := change(current)
	if err != nil {
		studioJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error()})
		return
	}
	updated := mutation.Source
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
	if bytes.Equal(current, updated) {
		s.lastGoodSource, s.lastGoodProject = bytes.Clone(current), p
		response := map[string]any{"revision": studioRevision(current), "valid": true, "source": string(current), "unchanged": true}
		for key, value := range mutation.Response {
			response[key] = value
		}
		studioJSON(w, http.StatusOK, response)
		return
	}
	info, err := os.Stat(s.path)
	committed, preserved := false, ""
	if err == nil {
		committed, preserved, err = studioWriteIfRevision(s.path, updated, info.Mode().Perm(), edit.Revision, beforeSwap)
	}
	if err != nil {
		if errors.Is(err, errStudioSwapUnavailable) {
			studioJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
			return
		}
		if preserved != "" {
			studioJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "preserved": preserved})
			return
		}
		studioJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if !committed {
		studioJSON(w, http.StatusConflict, map[string]any{"error": "score changed during commit; reload before saving"})
		return
	}
	for _, file := range mutation.Files {
		if err := studioWriteAuxiliaryFile(file); err != nil {
			_, _, rollbackErr := studioWriteIfRevision(s.path, current, info.Mode().Perm(), studioRevision(updated), nil)
			message := "mixer source saved but edition upgrade failed: " + err.Error()
			if rollbackErr != nil {
				message += "; source rollback failed: " + rollbackErr.Error()
			}
			studioJSON(w, http.StatusConflict, map[string]any{"error": message})
			return
		}
	}
	s.lastGoodSource, s.lastGoodProject = bytes.Clone(updated), p
	if s.history != nil {
		if mutation.HistoryDetail != "" {
			edit.Label = mutation.HistoryDetail
		}
		s.history.recordSourceWrite(current, updated, studioEditLabel(edit), studioHistoryWriteNew, 0)
	}
	response := map[string]any{"revision": studioRevision(updated), "valid": true, "source": string(updated), "preserved": preserved}
	for key, value := range mutation.Response {
		response[key] = value
	}
	studioJSON(w, http.StatusOK, response)
}

func compileStudioSource(path string, source []byte) (*project.Project, error) {
	if err := refuseMultiFileStudio(path); err != nil {
		return nil, err
	}
	if !utf8.Valid(source) {
		return nil, fmt.Errorf("score is not UTF-8")
	}
	score, diagnostics, err := parseScoreForPath(path, source)
	if err != nil {
		return nil, err
	}
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
	if err := project.ValidateProject(p); err != nil {
		return nil, err
	}
	if !p.HasAudio() {
		if _, err := project.CompileEngine(p, 48000, 128); err != nil {
			return nil, err
		}
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

func refuseMultiFileStudio(path string) error {
	_, manifestPath, err := scoreEdition(path)
	if err != nil {
		return err
	}
	if manifestPath == "" {
		return nil
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	manifest, err := edition.ParseProjectManifest(data)
	if err != nil {
		return err
	}
	if len(manifest.SourcePaths()) > 1 {
		return fmt.Errorf("CICADA-UNSUPPORTED: Studio cannot edit multi-file projects yet; use a text editor and cicada check, play, or render")
	}
	return nil
}
