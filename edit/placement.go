package edit

import (
	"fmt"
	"strings"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/taproot/walk"
	"m31labs.dev/cicada/notation"
)

// PlacementSettings are the authored fields of a placement or marker. An empty
// field is left as it is. At and Length keep the literal the author typed
// (for example "@5.1.1" or "3bars").
type PlacementSettings struct {
	At      string `json:"at,omitempty"`
	Length  string `json:"length,omitempty"`
	Track   string `json:"track,omitempty"`
	Content string `json:"content,omitempty"`
}

// UpdatePlacement patches the named fields of one placement in place.
// Unlike MoveClip and ResizeClip, it keeps the typed literals and changes any
// number of fields in one revision, so a move and a trim never pass through an
// overlapping state.
type UpdatePlacement struct {
	Entity   EntityID           `json:"entity"`
	Settings *PlacementSettings `json:"settings"`
}

func (UpdatePlacement) Kind() string { return "updateplacement" }

// AddPlacement appends a placement at the end of the arrange block.
type AddPlacement struct {
	Name     string             `json:"name"`
	Settings *PlacementSettings `json:"settings"`
}

func (AddPlacement) Kind() string { return "addplacement" }

// CopyPlacement appends a copy of a placement under a new name; settings
// override the copied fields.
type CopyPlacement struct {
	Entity   EntityID           `json:"entity"`
	Name     string             `json:"name"`
	Settings *PlacementSettings `json:"settings"`
}

func (CopyPlacement) Kind() string { return "copyplacement" }

// RemovePlacement removes the declaration of one placement and nothing else.
// DeleteClip also removes the comment lines above the placement.
type RemovePlacement struct {
	Entity EntityID `json:"entity"`
}

func (RemovePlacement) Kind() string { return "removeplacement" }

// SetMarker moves one marker.
type SetMarker struct {
	Entity   EntityID           `json:"entity"`
	Settings *PlacementSettings `json:"settings"`
}

func (SetMarker) Kind() string { return "setmarker" }

// AddMarker appends a marker at the end of the arrange block.
type AddMarker struct {
	Name     string             `json:"name"`
	Settings *PlacementSettings `json:"settings"`
}

func (AddMarker) Kind() string { return "addmarker" }

// RemoveMarker removes the declaration of one marker.
type RemoveMarker struct {
	Entity EntityID `json:"entity"`
}

func (RemoveMarker) Kind() string { return "removemarker" }

func init() {
	Register("updateplacement", func() Intent { return &UpdatePlacement{} })
	Register("addplacement", func() Intent { return &AddPlacement{} })
	Register("copyplacement", func() Intent { return &CopyPlacement{} })
	Register("removeplacement", func() Intent { return &RemovePlacement{} })
	Register("setmarker", func() Intent { return &SetMarker{} })
	Register("addmarker", func() Intent { return &AddMarker{} })
	Register("removemarker", func() Intent { return &RemoveMarker{} })
	for _, kind := range []string{"updateplacement", "addplacement", "copyplacement", "removeplacement", "setmarker", "addmarker", "removemarker"} {
		Handle(kind, editArrangement)
	}
}

// arrangementEntityName takes the declaration name from a placement or marker
// entity. It reads the name without ParseEntityID so that an empty or odd name
// reaches the "unknown arrangement region" check, as the route always did.
func arrangementEntityName(id EntityID, kind string) (string, error) {
	if id.Kind() != kind {
		return "", fmt.Errorf("%s edits need a %s entity, got %q", kind, kind, id)
	}
	return id.Name(), nil
}

func editArrangement(ctx *Context, intent Intent) error {
	var op arrangementOp
	var label string
	var err error
	switch in := intent.(type) {
	case *UpdatePlacement:
		op.action, op.settings = "update", in.Settings
		op.name, err = arrangementEntityName(in.Entity, KindPlacement)
		label = "Placement " + op.name + " updated"
	case *AddPlacement:
		op.action, op.newName, op.settings = "append", in.Name, in.Settings
		label = "Placement " + in.Name + " added"
	case *CopyPlacement:
		op.action, op.newName, op.settings = "duplicate", in.Name, in.Settings
		op.name, err = arrangementEntityName(in.Entity, KindPlacement)
		label = "Placement " + op.name + " copied as " + in.Name
	case *RemovePlacement:
		op.action = "delete"
		op.name, err = arrangementEntityName(in.Entity, KindPlacement)
		label = "Placement " + op.name + " removed"
	case *SetMarker:
		op.action, op.settings = "marker", in.Settings
		op.name, err = arrangementEntityName(in.Entity, KindMarker)
		label = "Marker " + op.name + " moved"
	case *AddMarker:
		op.action, op.newName, op.settings = "append-marker", in.Name, in.Settings
		label = "Marker " + in.Name + " added"
	case *RemoveMarker:
		op.action = "delete-marker"
		op.name, err = arrangementEntityName(in.Entity, KindMarker)
		label = "Marker " + op.name + " removed"
	}
	if err != nil {
		return err
	}
	updated, err := arrangementSource(ctx.Source, ctx.Options.Path, op)
	if err != nil {
		return err
	}
	ctx.Source, ctx.plan = updated, nil
	ctx.SetLabel(label)
	return nil
}

type arrangementOp struct {
	action, name, newName string
	settings              *PlacementSettings
}

// arrangementSource patches placement fields together and lets the normal
// transaction compile the complete project before publication, including
// imported patterns. Comments and other declarations keep their exact bytes.
// It ports editedArrangementSource.
func arrangementSource(source []byte, file string, op arrangementOp) ([]byte, error) {
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
	marker := strings.Contains(op.action, "marker")
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
			if childKind == kind && name == op.name {
				selected = child
			}
		}
	}
	if arrangement == nil {
		return nil, fmt.Errorf("this score uses scenes; named placements require an arrange block")
	}
	appendNew := op.action == "append" || op.action == "append-marker"
	if !appendNew && selected == nil {
		return nil, fmt.Errorf("unknown arrangement region %q", op.name)
	}
	if op.action == "delete" || op.action == "delete-marker" {
		if !marker && placements < 2 {
			return nil, fmt.Errorf("keep at least one placement in the arrangement")
		}
		return ReplaceSpan(source, file, Span{int(selected.StartByte()), int(selected.EndByte()), "", file})
	}
	settings := op.settings
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
				if !patternName.MatchString(part) {
					return nil, fmt.Errorf("choose an existing track and pattern or clip")
				}
			}
		}
	}
	if marker && (settings.Track != "" || settings.Content != "") {
		return nil, fmt.Errorf("markers do not have track or content fields")
	}
	if appendNew || op.action == "duplicate" {
		if !patternName.MatchString(op.newName) || names[op.newName] {
			return nil, fmt.Errorf("choose a unique region name of at most 64 lowercase letters, digits, underscores, or hyphens, starting with a letter or underscore")
		}
		if settings.At == "" {
			return nil, fmt.Errorf("choose a start position")
		}
		var declaration []byte
		if op.action == "duplicate" {
			start := int(selected.StartByte())
			patches, err := placementFieldSpans(selected, walker, settings, start, file)
			if err != nil {
				return nil, err
			}
			name := walker.Field(selected, "name")
			patches = append(patches, Span{int(name.StartByte()) - start, int(name.EndByte()) - start, op.newName, file})
			declaration, err = PatchSpans(source[start:selected.EndByte()], file, patches)
			if err != nil {
				return nil, err
			}
		} else if marker {
			declaration = []byte(fmt.Sprintf("marker %s { at = %s }", op.newName, settings.At))
		} else {
			if settings.Track == "" || settings.Content == "" || settings.Length == "" {
				return nil, fmt.Errorf("choose a track, pattern or clip, and length")
			}
			declaration = []byte(fmt.Sprintf("place %s %s %s { at = %s length = %s }", op.newName, settings.Track, settings.Content, settings.At, settings.Length))
		}
		newline := Newline(source)
		// Append inside the existing braces, leaving every existing comment
		// beside its original region instead of copying or moving it.
		at := int(arrangement.EndByte()) - 1
		prefix := ""
		if at > 0 && source[at-1] != '\n' {
			prefix = newline
		}
		return ReplaceSpan(source, file, Span{at, at, prefix + "  " + string(declaration) + newline, file})
	}
	patches, err := placementFieldSpans(selected, walker, settings, 0, file)
	if err != nil {
		return nil, err
	}
	if len(patches) == 0 {
		return nil, fmt.Errorf("choose a placement field to edit")
	}
	return PatchSpans(source, file, patches)
}

// placementFieldSpans ports placementFieldPatches.
func placementFieldSpans(node *gts.Node, walker *walk.Walker, settings *PlacementSettings, offset int, file string) ([]Span, error) {
	var patches []Span
	for _, field := range []struct{ name, value string }{{"track", settings.Track}, {"content", settings.Content}} {
		if field.value != "" {
			value := walker.Field(node, field.name)
			if value == nil {
				return nil, fmt.Errorf("region has no %s field", field.name)
			}
			patches = append(patches, Span{int(value.StartByte()) - offset, int(value.EndByte()) - offset, field.value, file})
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
		patches = append(patches, Span{int(value.StartByte()) - offset, int(value.EndByte()) - offset, field.value, file})
	}
	return patches, nil
}
