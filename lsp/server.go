// Package lsp serves Cicada's parser and musical tooling over Language Server
// Protocol 3.17. Documents are kept in memory; saving is the editor's job.
package lsp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"m31labs.dev/cicada/edition"
	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/language"
	"m31labs.dev/cicada/migration"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

type position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}
type region struct {
	Start position `json:"start"`
	End   position `json:"end"`
}
type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}
type document struct {
	URI     string `json:"uri"`
	Text    string `json:"text"`
	Version *int   `json:"version"`
}

var tokenTypes = []string{"keyword", "number", "string", "variable", "function", "type", "operator", "comment", "property", "enumMember"}

type server struct {
	out              io.Writer
	documents        map[string][]byte
	versions         map[string]*int
	canEditDocuments bool
	canCreateFiles   bool
}

// Serve reads Content-Length framed JSON-RPC messages until exit or EOF.
func Serve(in io.Reader, out io.Writer) error {
	s := &server{out: out, documents: map[string][]byte{}, versions: map[string]*int{}}
	reader := bufio.NewReader(in)
	for {
		body, err := readFrame(reader)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		var message request
		if err := json.Unmarshal(body, &message); err != nil {
			return err
		}
		if message.Method == "exit" {
			return nil
		}
		if err := s.handle(message); err != nil {
			return err
		}
	}
}

func readFrame(reader *bufio.Reader) ([]byte, error) {
	length := -1
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(line) == "" {
			break
		}
		if value, ok := strings.CutPrefix(strings.ToLower(strings.TrimSpace(line)), "content-length:"); ok {
			length, err = strconv.Atoi(strings.TrimSpace(value))
			if err != nil || length < 0 || length > 4<<20 {
				return nil, fmt.Errorf("invalid LSP Content-Length")
			}
		}
	}
	if length < 0 {
		return nil, fmt.Errorf("missing LSP Content-Length")
	}
	data := make([]byte, length)
	_, err := io.ReadFull(reader, data)
	return data, err
}

func (s *server) send(value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(s.out, "Content-Length: %d\r\n\r\n", len(body))
	if err != nil {
		return err
	}
	_, err = s.out.Write(body)
	return err
}

func (s *server) reply(id json.RawMessage, value any) error {
	return s.send(map[string]any{"jsonrpc": "2.0", "id": id, "result": value})
}

func (s *server) handle(message request) error {
	switch message.Method {
	case "initialize":
		var params struct {
			Capabilities struct {
				Workspace struct {
					WorkspaceEdit struct {
						DocumentChanges    bool     `json:"documentChanges"`
						ResourceOperations []string `json:"resourceOperations"`
					} `json:"workspaceEdit"`
				} `json:"workspace"`
			} `json:"capabilities"`
		}
		if err := json.Unmarshal(message.Params, &params); err != nil {
			return err
		}
		s.canEditDocuments = params.Capabilities.Workspace.WorkspaceEdit.DocumentChanges
		for _, operation := range params.Capabilities.Workspace.WorkspaceEdit.ResourceOperations {
			if operation == "create" {
				s.canCreateFiles = true
			}
		}
		return s.reply(message.ID, map[string]any{"capabilities": map[string]any{
			"textDocumentSync": 1, "hoverProvider": true, "definitionProvider": true, "renameProvider": true, "completionProvider": map[string]any{"triggerCharacters": []string{"."}}, "inlayHintProvider": true, "codeActionProvider": true,
			"semanticTokensProvider": map[string]any{"legend": map[string]any{"tokenTypes": tokenTypes, "tokenModifiers": []string{}}, "full": true},
		}, "serverInfo": map[string]any{"name": "cicada-lsp", "version": "0.1"}})
	case "shutdown":
		return s.reply(message.ID, nil)
	case "textDocument/didOpen":
		var params struct {
			TextDocument document `json:"textDocument"`
		}
		if err := json.Unmarshal(message.Params, &params); err != nil {
			return err
		}
		s.documents[params.TextDocument.URI] = []byte(params.TextDocument.Text)
		s.versions[params.TextDocument.URI] = params.TextDocument.Version
		return s.publish(params.TextDocument.URI)
	case "textDocument/didChange":
		var params struct {
			TextDocument struct {
				URI     string `json:"uri"`
				Version *int   `json:"version"`
			} `json:"textDocument"`
			ContentChanges []struct {
				Text string `json:"text"`
			} `json:"contentChanges"`
		}
		if err := json.Unmarshal(message.Params, &params); err != nil {
			return err
		}
		if len(params.ContentChanges) > 0 {
			s.documents[params.TextDocument.URI] = []byte(params.ContentChanges[len(params.ContentChanges)-1].Text)
			s.versions[params.TextDocument.URI] = params.TextDocument.Version
			return s.publish(params.TextDocument.URI)
		}
	case "textDocument/didClose":
		var params struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
		}
		if err := json.Unmarshal(message.Params, &params); err != nil {
			return err
		}
		delete(s.documents, params.TextDocument.URI)
		delete(s.versions, params.TextDocument.URI)
		if err := s.send(map[string]any{"jsonrpc": "2.0", "method": "textDocument/publishDiagnostics", "params": map[string]any{"uri": params.TextDocument.URI, "diagnostics": []any{}}}); err != nil {
			return err
		}
		closed, err := s.projectSources(params.TextDocument.URI)
		if err == nil && closed != nil && closed.Manifest.ExplicitSources() {
			for peer := range s.documents {
				if files, err := s.projectSources(peer); err == nil && files != nil && files.ManifestPath == closed.ManifestPath {
					return s.publish(peer)
				}
			}
			return s.publish(params.TextDocument.URI)
		}
		return nil
	case "textDocument/hover":
		uri, at, err := documentPosition(message.Params)
		if err != nil {
			return err
		}
		return s.reply(message.ID, s.projectHover(uri, at))
	case "textDocument/definition":
		uri, at, err := documentPosition(message.Params)
		if err != nil {
			return err
		}
		return s.reply(message.ID, s.projectDefinition(uri, at))
	case "textDocument/rename":
		var params struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
			Position position `json:"position"`
			NewName  string   `json:"newName"`
		}
		if err := json.Unmarshal(message.Params, &params); err != nil {
			return err
		}
		return s.reply(message.ID, s.projectRename(params.TextDocument.URI, params.Position, params.NewName))
	case "textDocument/completion":
		uri, at, err := documentPosition(message.Params)
		if err != nil {
			return err
		}
		return s.reply(message.ID, s.projectCompletion(uri, at))
	case "textDocument/inlayHint":
		uri, err := documentURI(message.Params)
		if err != nil {
			return err
		}
		var params struct {
			Range *region `json:"range"`
		}
		if err := json.Unmarshal(message.Params, &params); err != nil {
			return err
		}
		return s.reply(message.ID, inlayHints(s.documents[uri], params.Range))
	case "textDocument/codeAction":
		var params struct {
			Context struct {
				Only []string `json:"only"`
			} `json:"context"`
		}
		if err := json.Unmarshal(message.Params, &params); err != nil {
			return err
		}
		if len(params.Context.Only) > 0 {
			requested := false
			for _, kind := range params.Context.Only {
				if kind == "quickfix" {
					requested = true
					break
				}
			}
			if !requested {
				return s.reply(message.ID, []any{})
			}
		}
		uri, err := documentURI(message.Params)
		if err != nil {
			return err
		}
		source, ok := s.documents[uri]
		if !ok || !s.canEditDocuments {
			return s.reply(message.ID, []any{})
		}
		path, ok := scorePathFromURI(uri)
		if !ok {
			return s.reply(message.ID, []any{})
		}
		projectEdition, manifest, err := edition.ScoreEdition(path)
		if err != nil || manifest == "" && !s.canCreateFiles {
			return s.reply(message.ID, []any{})
		}
		if files, err := s.projectSources(uri); err == nil && files != nil && files.Manifest.ExplicitSources() {
			return s.reply(message.ID, s.projectFixAction(files))
		}
		fixed, changed, err := migration.FixSource(source)
		if err != nil || !changed {
			return s.reply(message.ID, []any{})
		}
		changes := make([]any, 0, 3)
		if manifest == "" {
			name := strings.TrimSuffix(filepath.Base(path), ".cicada")
			if !edition.ValidProjectName(name) {
				return s.reply(message.ID, []any{})
			}
			manifestURI := fileURI(filepath.Join(filepath.Dir(path), "cicada.mod"))
			changes = append(changes,
				map[string]any{"kind": "create", "uri": manifestURI},
				map[string]any{"textDocument": map[string]any{"uri": manifestURI, "version": nil}, "edits": []any{map[string]any{"range": region{}, "newText": "project " + name + "\ncicada 2\n"}}},
			)
		} else if projectEdition == 1 {
			manifestData, readErr := os.ReadFile(manifest)
			if readErr != nil {
				return s.reply(message.ID, []any{})
			}
			upgraded, changed, upgradeErr := edition.UpgradeManifestEdition(manifestData)
			if upgradeErr != nil || !changed {
				return s.reply(message.ID, []any{})
			}
			manifestURI := fileURI(manifest)
			changes = append(changes, map[string]any{
				"textDocument": map[string]any{"uri": manifestURI, "version": nil},
				"edits":        []any{map[string]any{"range": region{Start: position{}, End: utf16Position(manifestData, len(manifestData))}, "newText": string(upgraded)}},
			})
		}
		changes = append(changes, map[string]any{
			"textDocument": map[string]any{"uri": uri, "version": s.versions[uri]},
			"edits":        []any{map[string]any{"range": region{Start: position{}, End: utf16Position(source, len(source))}, "newText": string(fixed)}},
		})
		action := map[string]any{
			"title": "Apply Cicada notation fixes", "kind": "quickfix",
			"edit": map[string]any{"documentChanges": changes},
		}
		return s.reply(message.ID, []any{action})
	case "textDocument/semanticTokens/full":
		uri, err := documentURI(message.Params)
		if err != nil {
			return err
		}
		return s.reply(message.ID, map[string]any{"data": semanticTokens(s.documents[uri])})
	}
	if len(message.ID) != 0 {
		return s.send(map[string]any{"jsonrpc": "2.0", "id": message.ID, "error": map[string]any{"code": -32601, "message": "method not found: " + message.Method}})
	}
	return nil
}

func documentURI(raw json.RawMessage) (string, error) {
	var params struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
	}
	err := json.Unmarshal(raw, &params)
	return params.TextDocument.URI, err
}

func scorePathFromURI(uri string) (string, bool) {
	return scorePathFromURIForOS(uri, runtime.GOOS == "windows")
}

func scorePathFromURIForOS(uri string, windows bool) (string, bool) {
	parsed, err := url.Parse(uri)
	if err != nil || parsed.Scheme != "file" || parsed.Port() != "" || parsed.User != nil || parsed.Host != "" && parsed.Host != "localhost" && !windows {
		return "", false
	}
	path := parsed.Path
	if windows {
		if parsed.Host != "" && parsed.Host != "localhost" {
			path = "//" + parsed.Host + path
		} else if len(path) >= 3 && path[0] == '/' && path[2] == ':' {
			path = path[1:]
		}
		return strings.ReplaceAll(path, "/", `\`), path != ""
	}
	return filepath.FromSlash(path), path != ""
}

func fileURI(path string) string {
	return fileURIForOS(path, runtime.GOOS == "windows")
}

func fileURIForOS(path string, windows bool) string {
	if windows {
		path = strings.ReplaceAll(path, `\`, "/")
		if strings.HasPrefix(path, "//") {
			host, rest, ok := strings.Cut(strings.TrimPrefix(path, "//"), "/")
			if ok {
				return (&url.URL{Scheme: "file", Host: host, Path: "/" + rest}).String()
			}
		}
		path = "/" + path
	} else {
		path = filepath.ToSlash(path)
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}

func documentPosition(raw json.RawMessage) (string, position, error) {
	var params struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
		Position position `json:"position"`
	}
	err := json.Unmarshal(raw, &params)
	return params.TextDocument.URI, params.Position, err
}

func (s *server) publish(uri string) error {
	source := s.documents[uri]
	sources, score, diagnostics := s.parseProjectDocument(uri)

	if score != nil && !hasErrors(diagnostics) {
		_, extra := project.FromScore(score)
		seen := map[string]bool{}
		for _, d := range diagnostics {
			seen[diagnosticKey(d)] = true
		}
		for _, d := range extra {
			if !seen[diagnosticKey(d)] {
				diagnostics = append(diagnostics, d)
				seen[diagnosticKey(d)] = true
			}
		}
	}
	groups := map[string][]any{uri: {}}
	if sources != nil {
		for _, file := range sources.Files {
			groups[fileURI(file.Path)] = []any{}
		}
	}
	sourceFor := func(target string) []byte {
		if data, ok := s.documents[target]; ok {
			return data
		}
		if sources != nil {
			for _, file := range sources.Files {
				if fileURI(file.Path) == target {
					return file.Source
				}
			}
		}
		if path, ok := scorePathFromURI(target); ok {
			data, _ := os.ReadFile(path)
			return data
		}
		return source
	}
	for _, d := range diagnostics {
		target := uri
		if d.Position.File != "" {
			target = fileURI(d.Position.File)
		}
		start := utf16Position(sourceFor(target), scalarOffset(sourceFor(target), d.Position))
		severity := 1
		if d.Severity == "warning" {
			severity = 2
		}
		item := map[string]any{"range": region{Start: start, End: position{Line: start.Line, Character: start.Character + 1}}, "severity": severity, "code": d.Code, "source": "cicada", "message": d.Message}
		if d.Related.Line > 0 {
			relatedURI := fileURI(d.Related.File)
			at := utf16Position(sourceFor(relatedURI), scalarOffset(sourceFor(relatedURI), d.Related))
			item["relatedInformation"] = []any{map[string]any{"location": map[string]any{"uri": relatedURI, "range": region{Start: at, End: position{Line: at.Line, Character: at.Character + 1}}}, "message": "first declaration"}}
		}
		groups[target] = append(groups[target], item)
	}
	uris := make([]string, 0, len(groups))
	for target := range groups {
		uris = append(uris, target)
	}
	sort.Strings(uris)
	for _, target := range uris {
		if err := s.send(map[string]any{"jsonrpc": "2.0", "method": "textDocument/publishDiagnostics", "params": map[string]any{"uri": target, "diagnostics": groups[target]}}); err != nil {
			return err
		}
	}
	return nil
}

func (s *server) parseProjectDocument(uri string) (*project.Sources, *notation.Score, []notation.Diagnostic) {
	files, err := s.projectSources(uri)
	if err != nil {
		var diagnostic *project.SourceError
		if errors.As(err, &diagnostic) {
			return nil, nil, []notation.Diagnostic{diagnostic.Diagnostic}
		}
		return nil, nil, []notation.Diagnostic{{Code: "CICADA-IO", Severity: "error", Message: err.Error(), Position: notation.Position{Line: 1, Column: 1}}}
	}
	if files == nil {
		score, ds := notation.Parse(s.documents[uri])
		return nil, score, ds
	}
	score, ds := files.Parse()
	return files, score, ds
}

func hasErrors(ds []notation.Diagnostic) bool {
	for _, d := range ds {
		if d.Severity == "error" {
			return true
		}
	}
	return false
}
func diagnosticKey(d notation.Diagnostic) string {
	return fmt.Sprintf("%s/%s/%s/%d/%d", d.Code, d.Message, d.Position.File, d.Position.Line, d.Position.Column)
}

func scalarOffset(source []byte, at notation.Position) int {
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

func utf16Position(source []byte, offset int) position {
	offset = min(max(offset, 0), len(source))
	at := position{}
	for i := 0; i < offset; {
		r, size := utf8.DecodeRune(source[i:])
		if r == '\n' {
			at.Line++
			at.Character = 0
		} else {
			at.Character += utf16.RuneLen(r)
		}
		i += size
	}
	return at
}

func byteOffset(source []byte, at position) int {
	line, i := 0, 0
	for i < len(source) && line < at.Line {
		if source[i] == '\n' {
			line++
		}
		i++
	}
	column := 0
	for i < len(source) && source[i] != '\n' && column < at.Character {
		r, size := utf8.DecodeRune(source[i:])
		column += utf16.RuneLen(r)
		i += size
	}
	return i
}

func hover(source []byte, at position) any {
	return hoverWithScore(source, at, nil)
}

func hoverWithScore(source []byte, at position, score *notation.Score) any {
	if len(source) == 0 {
		return nil
	}
	if match, ok := parameterPathAt(source, byteOffset(source, at)); ok {
		message := parameterPathHover(source, match)
		if message != "" {
			return map[string]any{"contents": map[string]any{"kind": "markdown", "value": message}, "range": region{Start: utf16Position(source, match.start), End: utf16Position(source, match.end)}}
		}
	}
	if symbol, _, ok := symbolAt(source, at); ok && (symbol.Kind == "effect" || symbol.Kind == "bus") {
		message := mixerSymbolHover(source, symbol)
		if message != "" {
			return map[string]any{"contents": map[string]any{"kind": "markdown", "value": message}, "range": symbolRegion(source, symbol)}
		}
	}
	spans, err := language.Highlight(source)
	if err != nil {
		return nil
	}
	offset := byteOffset(source, at)
	var selected *language.Span
	for i := range spans {
		span := &spans[i]
		if span.Start <= offset && offset < span.End && (selected == nil || span.End-span.Start < selected.End-selected.Start) {
			selected = span
		}
	}
	if selected == nil {
		return nil
	}
	value := string(source[selected.Start:selected.End])
	message := hoverText(selected.Capture, value, source, selected.Start, score)
	if message == "" {
		return nil
	}
	return map[string]any{"contents": map[string]any{"kind": "markdown", "value": message}, "range": region{Start: utf16Position(source, selected.Start), End: utf16Position(source, selected.End)}}
}

func mixerSymbolHover(source []byte, symbol language.Symbol) string {
	score, _ := notation.Parse(source)
	if score == nil {
		return ""
	}
	if symbol.Kind == "effect" {
		for _, effect := range score.Effects {
			if effect.Name == symbol.Name {
				return fmt.Sprintf("Effect **%s** uses kind `%s`.", effect.Name, effect.Kind)
			}
		}
	}
	if symbol.Kind == "bus" {
		if symbol.Name == "music" || symbol.Name == "sfx" {
			return fmt.Sprintf("Built-in output bus **%s**.", symbol.Name)
		}
		for _, bus := range score.Buses {
			if bus.Name == symbol.Name {
				return fmt.Sprintf("Bus **%s** is declared here.", bus.Name)
			}
		}
	}
	return ""
}

func hoverText(capture, value string, source []byte, offset int, score *notation.Score) string {
	if strings.HasPrefix(capture, "function") {
		if operation, ok := instrument.Operation(value); ok {
			return "`" + operation.Signature + "`\n\n" + operation.Meaning
		}
	}
	switch {
	case strings.HasPrefix(capture, "number.probability"):
		return "Plays **" + value + "%** of the time."
	case strings.HasPrefix(capture, "number.ratchet"):
		return "Retriggers **" + value + " times** in this step."
	case strings.HasPrefix(capture, "constant.hit.velocity") && len(value) == 2:
		return fmt.Sprintf("Drum hit at velocity **%d/127**.", int(value[1]-'0')*14)
	case strings.HasPrefix(capture, "constant.hit.accent"):
		return "Accented drum hit at velocity **127/127**."
	case strings.HasPrefix(capture, "constant.pitch.degree") && len(value) > 0:
		if score == nil {
			score, _ = notation.Parse(source)
		}
		if score == nil {
			return "Scale degree **" + value + "**."
		}
		return degreeHover(score, value)
	case strings.HasPrefix(capture, "constant.pitch.letter"):
		return "Letter pitch **" + strings.ToUpper(value) + "**. An octave digit makes it absolute."
	case strings.HasPrefix(capture, "variable.parameter"):
		return parameterHover(source, offset, value)
	case strings.HasPrefix(capture, "property"):
		return trackSettingHover(source, offset, value)
	}
	return ""
}

func parameterHover(source []byte, offset int, name string) string {
	score, _ := notation.Parse(source)
	if score == nil {
		return ""
	}
	scope := instrumentScope(source, offset)
	for _, inst := range score.Instruments {
		at := scalarOffset(source, inst.Position)
		if at < scope.start || at >= scope.end {
			continue
		}
		for _, param := range inst.Params {
			if param.Name != name {
				continue
			}
			message := "Parameter **" + name + "** defaults to `" + param.Default + "`."
			var overrides []string
			for _, track := range score.Tracks {
				if track.Kind != inst.Name {
					continue
				}
				for _, setting := range track.Params {
					if setting.Name == name {
						overrides = append(overrides, "`"+track.Name+"` = `"+setting.Value+"`")
					}
				}
			}
			if len(overrides) > 0 {
				message += " Track overrides: " + strings.Join(overrides, ", ") + "."
			}
			return message
		}
	}
	return ""
}

func trackSettingHover(source []byte, offset int, name string) string {
	score, _ := notation.Parse(source)
	if score == nil {
		return ""
	}
	for _, track := range score.Tracks {
		for _, setting := range track.Params {
			if setting.Name != name || scalarOffset(source, setting.Position) != offset {
				continue
			}
			message := "Track **" + track.Name + "** sets **" + name + "** to `" + setting.Value + "`."
			for _, inst := range score.Instruments {
				if inst.Name != track.Kind {
					continue
				}
				for _, param := range inst.Params {
					if param.Name == name {
						message += " Instrument default: `" + param.Default + "`."
					}
				}
			}
			return message
		}
	}
	return ""
}

var scaleSteps = map[string][]int{"major": {0, 2, 4, 5, 7, 9, 11}, "minor": {0, 2, 3, 5, 7, 8, 10}, "dorian": {0, 2, 3, 5, 7, 9, 10}, "phrygian": {0, 1, 3, 5, 7, 8, 10}, "harmonic": {0, 2, 3, 5, 7, 8, 11}, "mixo": {0, 2, 4, 5, 7, 9, 10}, "pent": {0, 0, 3, 5, 7, 0, 10}, "blues": {0, 0, 3, 5, 7, 0, 10}}
var pitchClass = map[byte]int{'c': 0, 'd': 2, 'e': 4, 'f': 5, 'g': 7, 'a': 9, 'b': 11}
var noteNames = []string{"C", "C♯", "D", "D♯", "E", "F", "F♯", "G", "G♯", "A", "A♯", "B"}

func degreeHover(score *notation.Score, value string) string {
	degree := int(value[0] - '1')
	steps := scaleSteps[score.Scale]
	if degree < 0 || degree >= len(steps) {
		return "Scale degree **" + value + "**."
	}
	root := pitchClass[score.KeyRoot[0]]
	if len(score.KeyRoot) > 1 {
		if score.KeyRoot[1] == '#' {
			root++
		} else {
			root--
		}
	}
	note := root + steps[degree]
	if len(value) > 1 {
		if value[1] == '#' {
			note++
		} else if value[1] == 'b' {
			note--
		}
	}
	note = (note%12 + 12) % 12
	return fmt.Sprintf("**%s** — degree %s of %s %s.", noteNames[note], value, strings.ToUpper(score.KeyRoot), score.Scale)
}

func inlayHints(source []byte, requested *region) []any {
	hints := []any{}
	score, diagnostics := notation.Parse(source)
	if score == nil || hasErrors(diagnostics) {
		return hints
	}
	for _, pattern := range score.Patterns {
		steps := len(pattern.Steps)
		if pattern.Kind == "drums" && len(pattern.Lanes) > 0 {
			steps = len(pattern.Lanes[0].Hits)
		}
		start := scalarOffset(source, pattern.Position)
		end := start
		for end < len(source) && source[end] != '{' && source[end] != '\n' {
			end++
		}
		if end < len(source) && source[end] == '{' {
			hints = append(hints, map[string]any{"position": utf16Position(source, end), "label": fmt.Sprintf("%d steps", steps), "kind": 2, "paddingLeft": true})
		}
	}
	bars := 0
	for _, entry := range score.Song {
		startBar := bars + 1
		bars += entry.Bars
		offset := scalarOffset(source, entry.Position)
		for offset < len(source) && source[offset] != ' ' && source[offset] != '\t' && source[offset] != '\r' && source[offset] != '\n' && source[offset] != '}' {
			offset++
		}
		seconds := 0.0
		if score.TempoMilli > 0 {
			seconds = float64(bars) * 240000 / float64(score.TempoMilli)
		}
		minute := int(seconds) / 60
		second := seconds - float64(minute*60)
		label := fmt.Sprintf("bars %d–%d, %d:%04.1f", startBar, bars, minute, second)
		hints = append(hints, map[string]any{"position": utf16Position(source, offset), "label": label, "kind": 2, "paddingLeft": true})
	}
	if requested == nil {
		return hints
	}
	filtered := make([]any, 0, len(hints))
	for _, item := range hints {
		at := item.(map[string]any)["position"].(position)
		if beforePosition(at, requested.Start) || beforePosition(requested.End, at) {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

func beforePosition(a, b position) bool {
	return a.Line < b.Line || a.Line == b.Line && a.Character < b.Character
}

func semanticTokens(source []byte) []int {
	data := []int{}
	spans, err := language.Highlight(source)
	if err != nil {
		return data
	}
	lastLine, lastColumn, lastEnd := 0, 0, -1
	for _, span := range spans {
		kind := semanticKind(span.Capture)
		if kind < 0 || span.Start < lastEnd {
			continue
		}
		start, end := utf16Position(source, span.Start), utf16Position(source, span.End)
		if start.Line != end.Line || end.Character <= start.Character {
			continue
		}
		deltaLine, deltaColumn := start.Line-lastLine, start.Character
		if deltaLine == 0 {
			deltaColumn -= lastColumn
		}
		data = append(data, deltaLine, deltaColumn, end.Character-start.Character, kind, 0)
		lastLine, lastColumn, lastEnd = start.Line, start.Character, span.End
	}
	return data
}

func semanticKind(capture string) int {
	switch {
	case strings.HasPrefix(capture, "keyword"):
		return 0
	case strings.HasPrefix(capture, "number"), strings.HasPrefix(capture, "constant.pitch"), strings.HasPrefix(capture, "constant.hit"):
		return 1
	case strings.HasPrefix(capture, "string"):
		return 2
	case strings.HasPrefix(capture, "variable"):
		return 3
	case strings.HasPrefix(capture, "function"):
		return 4
	case strings.HasPrefix(capture, "type"):
		return 5
	case strings.HasPrefix(capture, "operator"):
		return 6
	case strings.HasPrefix(capture, "comment"):
		return 7
	case strings.HasPrefix(capture, "property"):
		return 8
	case strings.HasPrefix(capture, "tag"), strings.HasPrefix(capture, "constant"):
		return 9
	}
	return -1
}

// Frame is useful to clients that embed the server instead of launching it.
func Frame(body []byte) []byte {
	var out bytes.Buffer
	fmt.Fprintf(&out, "Content-Length: %d\r\n\r\n", len(body))
	out.Write(body)
	return out.Bytes()
}
