package main

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/taproot/walk"
	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/notation"
)

type studioPlaceSettings struct {
	At      string `json:"at,omitempty"`
	Length  string `json:"length,omitempty"`
	Track   string `json:"track,omitempty"`
	Content string `json:"content,omitempty"`
}

func (s *studio) editArrangement(w http.ResponseWriter, r *http.Request) {
	edit, ok := studioRequest(w, r)
	if !ok {
		return
	}
	intent, ok := arrangementIntent(edit)
	if !ok {
		studioJSON(w, http.StatusBadRequest, map[string]any{"error": "unknown placement action"})
		return
	}
	edit.Label = studioEditLabel(edit)
	s.applyIntents(w, edit, edits.Envelope{Intents: []edits.Intent{intent}}, edits.ParamWriterAuto, nil)
}

// arrangementIntent maps a placement request to its intent. The second result
// is false for an unknown action.
func arrangementIntent(edit studioEdit) (edits.Intent, bool) {
	var settings *edits.PlacementSettings
	if edit.Placement != nil {
		settings = &edits.PlacementSettings{At: edit.Placement.At, Length: edit.Placement.Length, Track: edit.Placement.Track, Content: edit.Placement.Content}
	}
	placement, marker := edits.EntityID("placement:"+edit.Name), edits.EntityID("marker:"+edit.Name)
	switch edit.Action {
	case "update":
		return &edits.UpdatePlacement{Entity: placement, Settings: settings}, true
	case "append":
		return &edits.AddPlacement{Name: edit.NewName, Settings: settings}, true
	case "duplicate":
		return &edits.CopyPlacement{Entity: placement, Name: edit.NewName, Settings: settings}, true
	case "delete":
		return &edits.RemovePlacement{Entity: placement}, true
	case "marker":
		return &edits.SetMarker{Entity: marker, Settings: settings}, true
	case "append-marker":
		return &edits.AddMarker{Name: edit.NewName, Settings: settings}, true
	case "delete-marker":
		return &edits.RemoveMarker{Entity: marker}, true
	}
	return nil, false
}

// Placements are authored regions, not song entries. Patch their CST fields
// together and let the normal transaction compile the complete project before
// publication, including imported patterns. Comments and other declarations
// retain their exact bytes; moving and resizing is one Undo entry.
func editedArrangementSource(source []byte, edit studioEdit) ([]byte, error) {
	root, walker, err := notation.ParseTree(source)
	if err != nil {
		return nil, err
	}
	if root.HasError() {
		return nil, fmt.Errorf("fix score syntax before editing placements")
	}
	var arrangement, selected *gts.Node
	names := map[string]bool{}
	placements := 0
	marker := strings.Contains(edit.Action, "marker")
	kind := "place_decl"
	if marker {
		kind = "marker_decl"
	}
	for i := 0; i < root.NamedChildCount(); i++ {
		node := root.NamedChild(i)
		if walker.Type(node) != "arrange_decl" {
			continue
		}
		arrangement = node
		for j := 0; j < node.NamedChildCount(); j++ {
			child := node.NamedChild(j)
			childKind := walker.Type(child)
			if childKind != "place_decl" && childKind != "marker_decl" {
				continue
			}
			name := walker.Text(walker.Field(child, "name"))
			names[name] = true
			if childKind == "place_decl" {
				placements++
			}
			if childKind == kind && name == edit.Name {
				selected = child
			}
		}
	}
	if arrangement == nil {
		return nil, fmt.Errorf("this score uses scenes; named placements require an arrange block")
	}
	appendNew := edit.Action == "append" || edit.Action == "append-marker"
	if !appendNew && selected == nil {
		return nil, fmt.Errorf("unknown arrangement region %q", edit.Name)
	}
	if edit.Action == "delete" || edit.Action == "delete-marker" {
		if !marker && placements < 2 {
			return nil, fmt.Errorf("keep at least one placement in the arrangement")
		}
		return replaceSongSpan(source, int(selected.StartByte()), int(selected.EndByte()), nil)
	}
	settings := edit.Placement
	if settings == nil {
		return nil, fmt.Errorf("placement settings are required")
	}
	if settings.At != "" {
		if _, err := notation.MusicalTicks(settings.At, true); err != nil {
			return nil, fmt.Errorf("start: %w", err)
		}
	}
	if settings.Length != "" {
		if marker {
			return nil, fmt.Errorf("markers have a position, not a length")
		}
		if _, err := notation.MusicalTicks(settings.Length, false); err != nil {
			return nil, fmt.Errorf("length: %w", err)
		}
	}
	for _, reference := range []string{settings.Track, settings.Content} {
		if reference != "" {
			for _, part := range strings.Split(reference, ".") {
				if !studioPatternName.MatchString(part) {
					return nil, fmt.Errorf("choose an existing track and pattern or clip")
				}
			}
		}
	}
	if marker && (settings.Track != "" || settings.Content != "") {
		return nil, fmt.Errorf("markers do not have track or content fields")
	}
	if appendNew || edit.Action == "duplicate" {
		if !studioPatternName.MatchString(edit.NewName) || names[edit.NewName] {
			return nil, fmt.Errorf("choose a unique region name of at most 64 lowercase letters, digits, underscores, or hyphens, starting with a letter or underscore")
		}
		if settings.At == "" {
			return nil, fmt.Errorf("choose a start position")
		}
		var declaration []byte
		if edit.Action == "duplicate" {
			start := int(selected.StartByte())
			patches, err := placementFieldPatches(selected, walker, settings, start)
			if err != nil {
				return nil, err
			}
			name := walker.Field(selected, "name")
			patches = append(patches, studioSpanEdit{int(name.StartByte()) - start, int(name.EndByte()) - start, edit.NewName})
			declaration, err = patchStudioSpans(source[start:selected.EndByte()], patches)
			if err != nil {
				return nil, err
			}
		} else if marker {
			declaration = []byte(fmt.Sprintf("marker %s { at = %s }", edit.NewName, settings.At))
		} else {
			if settings.Track == "" || settings.Content == "" || settings.Length == "" {
				return nil, fmt.Errorf("choose a track, pattern or clip, and length")
			}
			declaration = []byte(fmt.Sprintf("place %s %s %s { at = %s length = %s }", edit.NewName, settings.Track, settings.Content, settings.At, settings.Length))
		}
		newline := "\n"
		if bytes.Contains(source, []byte("\r\n")) {
			newline = "\r\n"
		}
		// Append inside the existing braces, leaving every existing comment
		// beside its original region instead of copying or moving it.
		at := int(arrangement.EndByte()) - 1
		prefix := ""
		if at > 0 && source[at-1] != '\n' {
			prefix = newline
		}
		return replaceSongSpan(source, at, at, []byte(prefix+"  "+string(declaration)+newline))
	}
	patches, err := placementFieldPatches(selected, walker, settings, 0)
	if err != nil {
		return nil, err
	}
	if len(patches) == 0 {
		return nil, fmt.Errorf("choose a placement field to edit")
	}
	return patchStudioSpans(source, patches)
}

func placementFieldPatches(node *gts.Node, walker *walk.Walker, settings *studioPlaceSettings, offset int) ([]studioSpanEdit, error) {
	var patches []studioSpanEdit
	for _, field := range []struct{ name, value string }{{"track", settings.Track}, {"content", settings.Content}} {
		if field.value != "" {
			value := walker.Field(node, field.name)
			if value == nil {
				return nil, fmt.Errorf("region has no %s field", field.name)
			}
			patches = append(patches, studioSpanEdit{int(value.StartByte()) - offset, int(value.EndByte()) - offset, field.value})
		}
	}
	for _, field := range []struct{ name, value string }{{"at", settings.At}, {"length", settings.Length}} {
		if field.value == "" {
			continue
		}
		var value *gts.Node
		for i := 0; i < node.NamedChildCount(); i++ {
			param := node.NamedChild(i)
			if walker.Type(param) == "param_decl" && walker.Text(walker.Field(param, "name")) == field.name {
				value = walker.Field(param, "value")
				break
			}
		}
		if value == nil {
			return nil, fmt.Errorf("region has no %s field", field.name)
		}
		patches = append(patches, studioSpanEdit{int(value.StartByte()) - offset, int(value.EndByte()) - offset, field.value})
	}
	return patches, nil
}
