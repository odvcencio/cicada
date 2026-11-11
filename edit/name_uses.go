package edit

import (
	"fmt"
	"strings"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/taproot/walk"

	"m31labs.dev/cicada/notation"
)

// nameUse is one place the source names a track, a pattern or a scene: its
// declaration or a reference to it.
type nameUse struct {
	kind       string    // track, pattern or scene
	name       string    // the name as written
	role       string    // declaration, binding, or reference
	start, end int       // the bytes of the name (inside the quotes of a string)
	node       *gts.Node // the construct that holds the name
	use        string    // how a refusal names the construct, with its line
}

// nameUses lists the places source names a track, a pattern or a scene:
// declarations; scene bindings; parameter paths (automation, scene settings,
// modulation routes, MIDI mappings, live macros); track chains; arrangement
// placements; the song; live states, stingers and layers; MIDI track and
// launch mappings; envelope sources; and compressor sidechains. A qualified
// name belongs to a library and is left out.
func nameUses(source []byte) ([]nameUse, error) {
	root, walker, err := notation.ParseTree(source)
	if err != nil {
		return nil, err
	}
	var uses []nameUse
	line := func(node *gts.Node) int {
		row, _ := walker.Pos(node)
		return row
	}
	add := func(kind, role string, ident, node *gts.Node, use string) {
		if ident == nil || walker.Type(ident) != "identifier" {
			return
		}
		uses = append(uses, nameUse{kind: kind, name: walker.Text(ident), role: role, start: int(ident.StartByte()), end: int(ident.EndByte()), node: node, use: fmt.Sprintf("%s (line %d)", use, line(node))})
	}
	// A parameter path's owner is a track, an effect or a bus; the mixer
	// shares one namespace, so a track's name owns only its own paths.
	addPath := func(path, node *gts.Node, use string) {
		text := walker.Text(path)
		owner, _, ok := strings.Cut(text, ".")
		if !ok || owner == "" {
			return
		}
		start := int(path.StartByte())
		uses = append(uses, nameUse{kind: "track", name: owner, role: "reference", start: start, end: start + len(owner), node: node, use: fmt.Sprintf("%s (line %d)", use, line(node))})
	}
	var visit func(node *gts.Node, scene string)
	visit = func(node *gts.Node, scene string) {
		switch walker.Type(node) {
		case "track_decl":
			name := walker.Field(node, "name")
			add("track", "declaration", name, node, "track "+walker.Text(name))
			for i := 0; i < node.NamedChildCount(); i++ {
				child := node.NamedChild(i)
				if walker.Type(child) != "chain_decl" {
					continue
				}
				for j := 0; j < child.NamedChildCount(); j++ {
					add("pattern", "reference", child.NamedChild(j), child, "track "+walker.Text(name)+" chain")
				}
			}
		case "acid_pattern", "note_pattern", "drum_pattern", "performance_pattern":
			name := walker.Field(node, "name")
			add("pattern", "declaration", name, node, "pattern "+walker.Text(name))
		case "scene_decl":
			name := walker.Field(node, "name")
			add("scene", "declaration", name, node, "scene "+walker.Text(name))
			scene = walker.Text(name)
		case "scene_assignment":
			target, value := walker.Field(node, "target"), walker.Field(node, "value")
			if target != nil && target.NamedChildCount() > 0 {
				target = target.NamedChild(0)
			}
			if value != nil && value.NamedChildCount() > 0 {
				value = value.NamedChild(0)
			}
			if walker.Type(target) == "parameter_path" {
				addPath(target, node, "scene "+scene+" setting "+walker.Text(target))
				return
			}
			if notation.SceneAssignmentIsSetting(walker.Text(target), walker.Text(value)) {
				return
			}
			binding := "scene " + scene + " binding " + walker.Text(target) + " = " + walker.Text(value)
			add("track", "binding", target, node, binding)
			if text := walker.Text(value); text != "off" && text != "keep" && text != "stop" {
				add("pattern", "binding", value, node, binding)
			}
			return
		case "song_entry":
			add("scene", "reference", walker.Field(node, "scene"), node, "song entry "+walker.Text(node))
		case "automate_decl":
			addPath(walker.Field(node, "path"), node, "automation "+walker.Text(walker.Field(node, "path")))
			return
		case "mod_decl":
			target := walker.Field(node, "target")
			addPath(target, node, "modulation route "+walker.Text(walker.Field(node, "source"))+" -> "+walker.Text(target))
			return
		case "midi_cc":
			target := walker.Field(node, "target")
			addPath(target, node, "MIDI mapping "+walker.Text(target))
			return
		case "midi_track":
			add("track", "reference", walker.Field(node, "target"), node, "MIDI mapping -> "+walker.Text(walker.Field(node, "target")))
			return
		case "midi_note":
			use := "MIDI mapping " + strings.Join(strings.Fields(walker.Text(node)), " ")
			add("scene", "reference", walker.Field(node, "scene"), node, use)
			add("track", "reference", walker.Field(node, "track"), node, use)
			add("pattern", "reference", walker.Field(node, "pattern"), node, use)
			return
		case "live_macro_map":
			addPath(walker.Field(node, "target"), node, "live macro "+walker.Text(walker.Field(node, "name")))
			return
		case "live_state":
			add("scene", "reference", walker.Field(node, "scene"), node, "live state "+walker.Text(walker.Field(node, "name")))
			return
		case "live_stinger":
			use := "live stinger " + walker.Text(walker.Field(node, "name"))
			add("track", "reference", walker.Field(node, "track"), node, use)
			add("pattern", "reference", walker.Field(node, "pattern"), node, use)
			return
		case "live_layers":
			for i := 0; i < node.NamedChildCount(); i++ {
				if layer := node.NamedChild(i); walker.Type(layer) == "live_layer" {
					add("track", "reference", walker.Field(layer, "track"), layer, "live layers "+walker.Text(walker.Field(node, "macro")))
				}
			}
			return
		case "place_decl":
			use := "placement " + walker.Text(walker.Field(node, "name"))
			add("track", "reference", walker.Field(node, "track"), node, use)
			add("pattern", "reference", walker.Field(node, "content"), node, use)
			return
		case "envelope_decl":
			name := walker.Text(walker.Field(node, "name"))
			for _, value := range paramValues(walker, node, "source") {
				add("track", "reference", value, node, "envelope "+name+" source")
			}
			return
		case "fx_decl":
			name := walker.Text(walker.Field(node, "name"))
			for _, value := range paramValues(walker, node, "sidechain") {
				if walker.Type(value) == "string" {
					text := walker.Text(value)
					if len(text) >= 2 {
						start := int(value.StartByte()) + 1
						uses = append(uses, nameUse{kind: "track", name: text[1 : len(text)-1], role: "reference", start: start, end: start + len(text) - 2, node: node, use: fmt.Sprintf("effect %s sidechain (line %d)", name, line(node))})
					}
					continue
				}
				add("track", "reference", value, node, "effect "+name+" sidechain")
			}
			return
		}
		for i := 0; i < node.NamedChildCount(); i++ {
			visit(node.NamedChild(i), scene)
		}
	}
	visit(root, "")
	return uses, nil
}

// paramValues returns the value nodes of a declaration's settings with one
// name, unwrapping a value node to the identifier or string inside it.
func paramValues(walker *walk.Walker, decl *gts.Node, name string) []*gts.Node {
	var values []*gts.Node
	for i := 0; i < decl.NamedChildCount(); i++ {
		param := decl.NamedChild(i)
		if walker.Type(param) != "param_decl" || walker.Text(walker.Field(param, "name")) != name {
			continue
		}
		value := walker.Field(param, "value")
		for value != nil && walker.Type(value) == "value" && value.NamedChildCount() > 0 {
			value = value.NamedChild(0)
		}
		if value != nil {
			values = append(values, value)
		}
	}
	return values
}

// usesOf filters uses to one kind and name.
func usesOf(uses []nameUse, kind, name string) []nameUse {
	var matched []nameUse
	for _, use := range uses {
		if use.kind == kind && use.name == name {
			matched = append(matched, use)
		}
	}
	return matched
}
