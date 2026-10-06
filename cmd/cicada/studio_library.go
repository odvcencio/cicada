package main

import (
	"bytes"
	_ "embed"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/internal/paramdefs"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

//go:embed studio-library.js
var studioLibraryScript []byte

func (s *studio) libraryScript(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	_, _ = w.Write(studioLibraryScript)
}

type studioLibraryItem struct {
	Path     string `json:"path"`
	Location string `json:"location"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
}

func libraryItems(lib *project.Library) ([]studioLibraryItem, error) {
	items := []studioLibraryItem{}
	for _, file := range lib.Files {
		root, walker, err := notation.ParseTree(file.Source)
		if err != nil {
			return nil, err
		}
		for i := 0; i < root.NamedChildCount(); i++ {
			n := root.NamedChild(i)
			kind := map[string]string{"instrument_decl": "instrument", "sampler_decl": "instrument", "kit_decl": "kit", "fx_decl": "fx", "preset_decl": "preset"}[walker.Type(n)]
			name := strings.Join(strings.Fields(walker.Text(walker.Field(n, "name"))), "")
			if kind != "" && !strings.HasPrefix(name, "_") {
				items = append(items, studioLibraryItem{lib.Path, lib.Kind, name, kind})
			}
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items, nil
}

func (s *studio) libraryState(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sources, err := project.ReadSources(s.path, nil)
	if err != nil {
		studioJSON(w, 422, map[string]any{"error": err.Error()})
		return
	}
	locations, err := project.ListLibraries(sources.Root)
	if err != nil {
		studioJSON(w, 422, map[string]any{"error": err.Error()})
		return
	}
	items := []studioLibraryItem{}
	errors := []string{}
	seen := map[string]bool{}
	for _, location := range locations {
		if seen[location.Path] {
			continue
		}
		seen[location.Path] = true
		lib, err := project.InspectLibrary(sources.Root, location.Path)
		if err != nil {
			errors = append(errors, err.Error())
			continue
		}
		entries, err := libraryItems(lib)
		if err != nil {
			errors = append(errors, err.Error())
			continue
		}
		for _, item := range entries {
			text := strings.ToLower(item.Path + " " + item.Name)
			if (r.URL.Query().Get("kind") == "" || r.URL.Query().Get("kind") == item.Kind) && strings.Contains(text, strings.ToLower(r.URL.Query().Get("q"))) {
				items = append(items, item)
			}
		}
	}
	tracks := []string{}
	if p, _, err := project.LoadScore(s.path, nil); err == nil && p != nil {
		for _, t := range p.Tracks {
			tracks = append(tracks, t.Name)
		}
	}
	studioJSON(w, 200, map[string]any{"items": items, "errors": errors, "tracks": tracks})
}

func studioLibrarySelection(scorePath, library, name string) (studioLibraryItem, string, error) {
	sources, err := project.ReadSources(scorePath, nil)
	if err != nil {
		return studioLibraryItem{}, "", err
	}
	lib, err := project.InspectLibrary(sources.Root, library)
	if err != nil {
		return studioLibraryItem{}, "", err
	}
	items, err := libraryItems(lib)
	if err != nil {
		return studioLibraryItem{}, "", err
	}
	for _, item := range items {
		if item.Name == name {
			return item, sources.Root, nil
		}
	}
	return studioLibraryItem{}, "", fmt.Errorf("library item is missing or private")
}

func studioLibraryImport(source []byte, library string) []byte {
	for _, imp := range notation.ReadImports(notation.SourceFile{Source: source}) {
		if imp.Path == library {
			return bytes.Clone(source)
		}
	}
	return append(bytes.Clone(source), []byte(fmt.Sprintf("\nimport %q\n", library))...)
}

// Prepare pins with the same update code as the CLI. The overlay is validated
// before the source-write path commits either file; previews never write it.
func studioLibraryPins(scorePath string, source []byte) (*project.Sources, studioAuxiliaryFile, error) {
	sources, err := project.ReadSources(scorePath, map[string][]byte{scorePath: source})
	if err != nil {
		return nil, studioAuxiliaryFile{}, err
	}
	sum, _, err := sources.UpdateLibraries("")
	if err != nil {
		return nil, studioAuxiliaryFile{}, err
	}
	filename := filepath.Join(sources.Root, "cicada.sum")
	before, err := os.ReadFile(filename)
	mode := os.FileMode(0644)
	if os.IsNotExist(err) {
		before, err = nil, nil
	} else if err == nil {
		if info, e := os.Stat(filename); e == nil {
			mode = info.Mode().Perm()
		}
	}
	// Legacy single-file scores can share a manifest and sum. Preserve pins
	// used by other scores, and by source history that can still be redone.
	oldPins, parseErr := project.ParseLibrarySum(before)
	if parseErr != nil {
		return nil, studioAuxiliaryFile{}, parseErr
	}
	newPins, parseErr := project.ParseLibrarySum(sum)
	if parseErr != nil {
		return nil, studioAuxiliaryFile{}, parseErr
	}
	for name, pin := range oldPins {
		if _, ok := newPins[name]; !ok {
			newPins[name] = pin
		}
	}
	sum = project.FormatLibrarySum(newPins)
	return sources, studioAuxiliaryFile{filename, before, sum, mode}, err
}

func libraryReference(item studioLibraryItem) string { return path.Base(item.Path) + "." + item.Name }

func libraryTarget(sources *project.Sources, item studioLibraryItem) (string, error) {
	score, ds := notation.ParseFiles(sources.Files, 2)
	if score == nil {
		return "", fmt.Errorf("library declarations could not be parsed: %v", ds)
	}
	name := strings.ReplaceAll(item.Path, "/", ".") + "." + item.Name
	if preset, ok := notation.FindPreset(score, name); ok {
		name = preset.Target
	}
	kind, ok := notation.PresetTarget(score, name)
	if !ok {
		return "", fmt.Errorf("library item has no supported target")
	}
	return kind, nil
}

func libraryEffectKind(sources *project.Sources, item studioLibraryItem) string {
	score, _ := notation.ParseFiles(sources.Files, 2)
	if score == nil {
		return ""
	}
	name := strings.ReplaceAll(item.Path, "/", ".") + "." + item.Name
	if preset, ok := notation.FindPreset(score, name); ok {
		name = preset.Target
	}
	if strings.HasPrefix(name, "builtin.") {
		return strings.TrimPrefix(name, "builtin.")
	}
	for _, effect := range score.Effects {
		if effect.Name == name {
			return effect.Kind
		}
	}
	return ""
}

func studioLibraryPreviewProject(scorePath string, item studioLibraryItem) (*project.Project, []byte, error) {
	source := []byte(fmt.Sprintf("cicada 2\ntempo 120\nkey c minor\nimport %q\n", item.Path))
	sources, _, err := studioLibraryPins(scorePath, source)
	if err != nil {
		return nil, nil, err
	}
	kind, err := libraryTarget(sources, item)
	if err != nil {
		return nil, nil, err
	}
	ref := libraryReference(item)
	binding := "track preview " + ref + " {}\n"
	pattern := "pattern phrase { c3 . . . g3 . . . c3 . . . g3 . . . }\n"
	if kind == "kit" || kind == "drums" {
		pattern = libraryPreviewBeat(sources, item)
		if kind == "drums" {
			pattern = "pattern phrase drums { bd: X...x...X...x... sd: ....x.......x... ch: x.x.x.x.x.x.x.x. }\n"
		}
	}
	if kind == "lane" {
		pattern = fmt.Sprintf("pattern phrase drums { %s: X...x...X...x... }\n", libraryEffectKind(sources, item))
	}
	if kind == "effect" {
		binding = ""
		if item.Kind == "preset" {
			binding = "fx audition " + ref + " {}\n"
			ref = "audition"
		}
		switch libraryEffectKind(sources, item) {
		case "comp":
			binding += "bus music { insert=" + ref + " }\ntrack preview acid {}\n"
		case "delay", "reverb":
			binding += "track preview acid { send " + ref + " = 0.4 }\n"
		default:
			binding += "track preview acid { insert=" + ref + " }\n"
		}
	}
	source = append(source, []byte(binding+pattern+"scene audition { preview=phrase }\nsong { audition }\n")...)
	_, pins, err := studioLibraryPins(scorePath, source)
	if err != nil {
		return nil, nil, err
	}
	p, err := compileStudioSourceWithOverrides(scorePath, source, map[string][]byte{pins.Path: pins.After})
	return p, source, err
}

func libraryPreviewBeat(sources *project.Sources, item studioLibraryItem) string {
	score, _ := notation.ParseFiles(sources.Files, 2)
	name := strings.ReplaceAll(item.Path, "/", ".") + "." + item.Name
	if preset, ok := notation.FindPreset(score, name); ok {
		name = preset.Target
	}
	var out strings.Builder
	out.WriteString("pattern phrase drums {\n")
	for _, kit := range score.Kits {
		if kit.Name == name {
			for _, lane := range kit.Bindings {
				beat := "x.x.x.x.x.x.x.x."
				if lane.Lane == "bd" {
					beat = "X...x...X...x..."
				} else if lane.Lane == "sd" {
					beat = "....x.......x..."
				}
				fmt.Fprintf(&out, "  %s: %s\n", lane.Lane, beat)
			}
		}
	}
	out.WriteString("}\n")
	return out.String()
}

func (s *studio) libraryPreview(w http.ResponseWriter, r *http.Request) {
	edit, ok := studioRequest(w, r)
	if !ok {
		return
	}
	if edit.Action == "stop" {
		s.transport.stopPreview()
		studioJSON(w, 200, map[string]any{"stopped": true})
		return
	}
	if edit.Mode != "native" && edit.Mode != "browser" {
		studioJSON(w, 400, map[string]any{"error": "preview mode must be native or browser"})
		return
	}
	var generation uint64
	if edit.Mode == "native" {
		generation = s.transport.beginPreview()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := os.ReadFile(s.path)
	if err != nil || studioRevision(current) != edit.Revision {
		studioJSON(w, 409, map[string]any{"error": "score changed; reload before previewing"})
		return
	}
	item, _, err := studioLibrarySelection(s.path, edit.Path, edit.Item)
	var p *project.Project
	if err == nil {
		p, _, err = studioLibraryPreviewProject(s.path, item)
	}
	if err != nil {
		studioJSON(w, 422, map[string]any{"error": err.Error()})
		return
	}
	if edit.Mode == "native" {
		if err := s.transport.startPreviewRequest(s.path, p, generation); err != nil {
			studioJSON(w, 409, map[string]any{"error": err.Error()})
			return
		}
		studioJSON(w, 200, map[string]any{"durationMs": 2000})
		return
	}
	s.transport.mu.Lock()
	if s.transport.playing || s.transport.browserPlaying {
		s.transport.mu.Unlock()
		studioJSON(w, http.StatusConflict, map[string]any{"error": "stop playback before previewing"})
		return
	}
	s.transport.stopPreviewLocked()
	s.transport.mu.Unlock()
	if edit.Rate != 44100 && edit.Rate != 48000 && edit.Rate != 96000 {
		studioJSON(w, 400, map[string]any{"error": "rate must be 44100, 48000, or 96000"})
		return
	}
	cfg, err := project.CompileEngine(p, edit.Rate, 128)
	var image []byte
	if err == nil {
		image, err = kernelimage.Encode(cfg)
	}
	if err != nil {
		studioJSON(w, 422, map[string]any{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(image)
}

func (s *studio) libraryInsert(w http.ResponseWriter, r *http.Request) {
	edit, ok := studioRequest(w, r)
	if !ok {
		return
	}
	sources, err := project.ReadSources(s.path, nil)
	if err != nil {
		studioJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error()})
		return
	}
	unlock, err := project.LockLibraryPins(sources.Root)
	if err != nil {
		studioJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
		return
	}
	defer unlock()
	s.applyWithResult(w, edit, func(current []byte) (studioMutation, error) {
		if _, err := compileStudioSource(s.path, current); err != nil {
			return studioMutation{}, err
		}
		item, _, err := studioLibrarySelection(s.path, edit.Path, edit.Item)
		if err != nil {
			return studioMutation{}, err
		}
		if !validMixerIdentifier(edit.Track) {
			return studioMutation{}, fmt.Errorf("select a track or enter a valid new track name")
		}
		updated := studioLibraryImport(current, item.Path)
		sources, _, err := studioLibraryPins(s.path, updated)
		if err != nil {
			return studioMutation{}, err
		}
		kind, err := libraryTarget(sources, item)
		if err != nil {
			return studioMutation{}, err
		}
		root, walker, err := notation.ParseTree(updated)
		if err != nil {
			return studioMutation{}, err
		}
		found := false
		for i := 0; i < root.NamedChildCount(); i++ {
			n := root.NamedChild(i)
			if walker.Type(n) != "track_decl" || walker.Text(walker.Field(n, "name")) != edit.Track {
				continue
			}
			found = true
			if kind != "effect" {
				field := walker.Field(n, "kind")
				if field == nil {
					return studioMutation{}, fmt.Errorf("track binding is missing")
				}
				updated = replaceSourceSpan(updated, int(field.StartByte()), int(field.EndByte()), libraryReference(item))
			}
			break
		}
		if !found {
			binding := libraryReference(item)
			if kind == "effect" {
				binding = "acid"
			}
			updated = append(updated, []byte("\ntrack "+edit.Track+" "+binding+" {}\n")...)
		}
		if kind == "effect" {
			ref := libraryReference(item)
			if item.Kind == "preset" {
				instance := "library-" + studioRevision([]byte(ref))[:8]
				if !notation.DeclarationNames([]notation.SourceFile{{Source: updated}})[instance] {
					updated = append(updated, []byte("\nfx "+instance+" "+ref+" {}\n")...)
				}
				ref = instance
			}
			switch libraryEffectKind(sources, item) {
			case "comp":
				updated, err = studioLibrarySetting(updated, "bus_decl", "music", "insert", ref)
				if err == nil {
					updated, err = studioLibrarySetting(updated, "track_decl", edit.Track, "out", "music")
				}
			case "delay", "reverb":
				updated, err = studioLibrarySetting(updated, "track_decl", edit.Track, "send "+ref, "0.4")
			default:
				updated, err = studioLibrarySetting(updated, "track_decl", edit.Track, "insert", ref)
			}
			if err != nil {
				return studioMutation{}, err
			}
		}
		_, pins, err := studioLibraryPins(s.path, updated)
		return studioMutation{Source: updated, Files: []studioAuxiliaryFile{pins}, HistoryDetail: "Library: insert " + libraryReference(item) + " on " + edit.Track}, err
	}, nil)
}

func replaceSourceSpan(source []byte, start, end int, text string) []byte {
	result := append([]byte(nil), source[:start]...)
	result = append(result, []byte(text)...)
	return append(result, source[end:]...)
}

func studioLibrarySetting(source []byte, declKind, owner, field, literal string) ([]byte, error) {
	root, w, err := notation.ParseTree(source)
	if err != nil {
		return nil, err
	}
	for i := 0; i < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		if w.Type(n) != declKind || w.Text(w.Field(n, "name")) != owner {
			continue
		}
		for j := 0; j < n.NamedChildCount(); j++ {
			p := n.NamedChild(j)
			if w.Type(p) == "mix_setting" && p.NamedChildCount() > 0 {
				p = p.NamedChild(0)
			}
			if w.Type(p) == "param_decl" && w.Text(w.Field(p, "name")) == field {
				v := w.Field(p, "value")
				return replaceSourceSpan(source, int(v.StartByte()), int(v.EndByte()), literal), nil
			}
			if w.Type(p) == "send_decl" && "send "+w.Text(w.Field(p, "to")) == field {
				v := w.Field(p, "level")
				return replaceSourceSpan(source, int(v.StartByte()), int(v.EndByte()), literal), nil
			}
		}
		at := int(n.EndByte()) - 1
		return replaceSourceSpan(source, at, at, "\n  "+field+" = "+literal+"\n"), nil
	}
	if declKind == "bus_decl" {
		return append(bytes.Clone(source), []byte("\nbus "+owner+" { "+field+" = "+literal+" }\n")...), nil
	}
	return nil, fmt.Errorf("selected track does not exist")
}

func (s *studio) librarySavePreset(w http.ResponseWriter, r *http.Request) {
	edit, ok := studioRequest(w, r)
	if !ok {
		return
	}
	s.applyWithResult(w, edit, func(current []byte) (studioMutation, error) {
		updated, err := studioSavedPresetSource(s.path, current, edit.Track, edit.Name)
		return studioMutation{Source: updated, HistoryDetail: "Preset: save " + edit.Name + " from " + edit.Track}, err
	}, nil)
}

func studioSavedPresetSource(scorePath string, source []byte, track, name string) ([]byte, error) {
	if !validMixerIdentifier(name) || strings.HasPrefix(name, "_") {
		return nil, fmt.Errorf("enter a public preset name")
	}
	if _, err := compileStudioSource(scorePath, source); err != nil {
		return nil, err
	}
	score, ds, err := parseScoreForPath(scorePath, source)
	if err != nil || hasDiagnosticErrors(ds) {
		return nil, fmt.Errorf("score must validate before saving a preset")
	}
	_, ds = notation.ResolvePresets(score)
	if hasDiagnosticErrors(ds) {
		return nil, fmt.Errorf("preset values are invalid: %v", ds)
	}
	for declaration := range notation.DeclarationNames([]notation.SourceFile{{Source: source}}) {
		if declaration == name {
			return nil, fmt.Errorf("preset name already exists")
		}
	}
	for _, t := range score.Tracks {
		if t.Name != track {
			continue
		}
		targetKind := t.Kind
		values := map[string]string{}
		if preset, ok := notation.FindPreset(score, t.Kind); ok {
			targetKind = preset.Target
			for _, p := range preset.Params {
				values[p.Name] = p.Value
			}
		}
		target := targetKind
		if origin := score.Origins[targetKind]; origin.Library != "" {
			namespace := strings.ReplaceAll(origin.Library, "/", ".")
			if strings.HasPrefix(strings.TrimPrefix(targetKind, namespace+"."), "_") {
				var err error
				source, target, err = studioSnapshotPrivateInstrument(scorePath, source, score, targetKind, name)
				if err != nil {
					return nil, err
				}
			} else {
				// A preset can target a transitive dependency. Give the new local
				// declaration a direct import without changing any pinned bytes.
				source = studioLibraryImport(source, origin.Library)
				target = path.Base(origin.Library) + strings.TrimPrefix(targetKind, namespace)
			}
		}
		var out strings.Builder
		fmt.Fprintf(&out, "\npreset %s {\n  instrument = %s\n", name, target)
		descriptors := notation.PresetDescriptors(score, targetKind)
		for _, p := range t.Params {
			values[p.Name] = p.Value
		}
		seen := map[string]bool{}
		for i := len(descriptors) - 1; i >= 0; i-- {
			d := descriptors[i]
			if seen[d.Source] {
				continue
			}
			seen[d.Source] = true
			literal, ok := values[d.Source]
			if !ok {
				continue
			}
			if code, err := paramdefs.ValidateLiteral(d, literal); err != nil {
				return nil, fmt.Errorf("%s: %w", code, err)
			}
			// Voice octave and sampler settings inherit declaration defaults,
			// which can differ from the registry. Retain their explicit values.
			preserve := d.ID == "source.octave" || d.Scope == "sampler"
			if n, _, ok := paramdefs.LiteralNumber(literal); !preserve && ok && n == d.Default {
				continue
			}
			if !preserve && d.Curve == "toggle" && (literal == "off" || literal == "false") && d.Default == 0 {
				continue
			}
			if !preserve && d.Curve == "enum" && int(d.Default) >= 0 && int(d.Default) < len(d.Values) && literal == d.Values[int(d.Default)] {
				continue
			}
			fmt.Fprintf(&out, "  %s = %s\n", d.Source, literal)
		}
		out.WriteString("}\n")
		return append(bytes.Clone(source), []byte(out.String())...), nil
	}
	return nil, fmt.Errorf("selected track does not exist")
}

// An exported preset may encapsulate a private authored instrument. Copy its
// complete declaration (parameters, bindings and output) under a local name.
func studioSnapshotPrivateInstrument(scorePath string, source []byte, score *notation.Score, target, presetName string) ([]byte, string, error) {
	origin := score.Origins[target]
	sources, err := project.ReadSources(scorePath, map[string][]byte{scorePath: source})
	if err != nil {
		return nil, "", err
	}
	names := notation.DeclarationNames([]notation.SourceFile{{Source: source}})
	name := presetName + "-voice"
	for suffix := 2; names[name] || name == presetName; suffix++ {
		name = fmt.Sprintf("%s-voice-%d", presetName, suffix)
	}
	for _, file := range sources.Files {
		if file.Library != origin.Library {
			continue
		}
		root, walker, err := notation.ParseTree(file.Source)
		if err != nil {
			return nil, "", err
		}
		for i := 0; i < root.NamedChildCount(); i++ {
			node := root.NamedChild(i)
			if walker.Type(node) != "instrument_decl" {
				continue
			}
			identifier := walker.Field(node, "name")
			if strings.ReplaceAll(origin.Library, "/", ".")+"."+walker.Text(identifier) != target {
				continue
			}
			declaration := bytes.Clone(file.Source[node.StartByte():node.EndByte()])
			declaration = replaceSourceSpan(declaration, int(identifier.StartByte()-node.StartByte()), int(identifier.EndByte()-node.StartByte()), name)
			updated := append(bytes.Clone(source), '\n')
			updated = append(updated, declaration...)
			return append(updated, '\n'), name, nil
		}
	}
	return nil, "", fmt.Errorf("private preset target cannot be snapshotted")
}
