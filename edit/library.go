package edit

import (
	"bytes"
	"fmt"
	"strings"

	"m31labs.dev/cicada/notation"
)

// InsertLibraryItem edits text for a library item resolved by the host.
// PresetInstance names an effect instance when Reference is an effect preset.
type InsertLibraryItem struct {
	Track          string `json:"track"`
	ImportPath     string `json:"importpath"`
	Reference      string `json:"reference"`
	ItemKind       string `json:"itemkind"`
	EffectKind     string `json:"effectkind,omitempty"`
	PresetInstance string `json:"presetinstance,omitempty"`
}

func (InsertLibraryItem) Kind() string { return "insertlibraryitem" }

// SavePreset appends the declaration prepared by the host, including any
// direct import or snapshot of a private library instrument.
type SavePreset struct {
	Track       string `json:"track"`
	Name        string `json:"name"`
	Declaration string `json:"declaration"`
}

func (SavePreset) Kind() string { return "savepreset" }
func init() {
	Register("insertlibraryitem", func() Intent { return &InsertLibraryItem{} })
	Register("savepreset", func() Intent { return &SavePreset{} })
	Handle("insertlibraryitem", insertLibraryItem)
	Handle("savepreset", func(ctx *Context, intent Intent) error {
		in := intent.(*SavePreset)
		if !ValidMixerIdentifier(in.Name) || strings.HasPrefix(in.Name, "_") {
			return fmt.Errorf("enter a public preset name")
		}
		if notation.DeclarationNames([]notation.SourceFile{{Source: ctx.Source}})[in.Name] {
			return fmt.Errorf("preset name already exists")
		}
		ctx.Source = append(bytes.Clone(ctx.Source), in.Declaration...)
		ctx.plan = nil
		ctx.SetLabel("Preset: save " + in.Name + " from " + in.Track)
		return nil
	})
}

func insertLibraryItem(ctx *Context, intent Intent) error {
	in := intent.(*InsertLibraryItem)
	if !ValidMixerIdentifier(in.Track) {
		return fmt.Errorf("select a track or enter a valid new track name")
	}
	updated := LibraryImport(ctx.Source, in.ImportPath)
	root, w, err := notation.ParseTree(updated)
	if err != nil {
		return err
	}
	found := false
	for i := 0; i < root.NamedChildCount(); i++ {
		node := root.NamedChild(i)
		if w.Type(node) != "track_decl" || w.Text(w.Field(node, "name")) != in.Track {
			continue
		}
		found = true
		if in.ItemKind != "effect" {
			field := w.Field(node, "kind")
			if field == nil {
				return fmt.Errorf("track binding is missing")
			}
			updated, err = ReplaceSpan(updated, ctx.Options.Path, Span{int(field.StartByte()), int(field.EndByte()), in.Reference, ctx.Options.Path})
			if err != nil {
				return err
			}
		}
		break
	}
	if !found {
		binding := in.Reference
		if in.ItemKind == "effect" {
			binding = "acid"
		}
		updated = append(updated, []byte("\ntrack "+in.Track+" "+binding+" {}\n")...)
	}
	if in.ItemKind == "effect" {
		ref := in.Reference
		if in.PresetInstance != "" {
			if !notation.DeclarationNames([]notation.SourceFile{{Source: updated}})[in.PresetInstance] {
				updated = append(updated, []byte("\nfx "+in.PresetInstance+" "+ref+" {}\n")...)
			}
			ref = in.PresetInstance
		}
		switch in.EffectKind {
		case "comp":
			updated, err = librarySetting(updated, ctx.Options.Path, "bus_decl", "music", "insert", ref)
			if err == nil {
				updated, err = librarySetting(updated, ctx.Options.Path, "track_decl", in.Track, "out", "music")
			}
		case "delay", "reverb":
			updated, err = librarySetting(updated, ctx.Options.Path, "track_decl", in.Track, "send "+ref, "0.4")
		default:
			updated, err = librarySetting(updated, ctx.Options.Path, "track_decl", in.Track, "insert", ref)
		}
		if err != nil {
			return err
		}
	}
	ctx.Source, ctx.plan = updated, nil
	ctx.SetLabel("Library: insert " + in.Reference + " on " + in.Track)
	return nil
}

// LibraryImport adds a direct import once without changing existing bytes.
func LibraryImport(source []byte, library string) []byte {
	for _, imp := range notation.ReadImports(notation.SourceFile{Source: source}) {
		if imp.Path == library {
			return bytes.Clone(source)
		}
	}
	return append(bytes.Clone(source), []byte(fmt.Sprintf("\nimport %q\n", library))...)
}

// LibrarySetting patches one track or bus setting.
func LibrarySetting(source []byte, declKind, owner, field, literal string) ([]byte, error) {
	return librarySetting(source, "", declKind, owner, field, literal)
}

func librarySetting(source []byte, targetFile, declKind, owner, field, literal string) ([]byte, error) {
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
				return ReplaceSpan(source, targetFile, Span{int(v.StartByte()), int(v.EndByte()), literal, targetFile})
			}
			if w.Type(p) == "send_decl" && "send "+w.Text(w.Field(p, "to")) == field {
				v := w.Field(p, "level")
				return ReplaceSpan(source, targetFile, Span{int(v.StartByte()), int(v.EndByte()), literal, targetFile})
			}
		}
		at := int(n.EndByte()) - 1
		return ReplaceSpan(source, targetFile, Span{at, at, "\n  " + field + " = " + literal + "\n", targetFile})
	}
	if declKind == "bus_decl" {
		return append(bytes.Clone(source), []byte("\nbus "+owner+" { "+field+" = "+literal+" }\n")...), nil
	}
	return nil, fmt.Errorf("selected track does not exist")
}
