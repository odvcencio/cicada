package edit

import (
	"bytes"
	"fmt"
	"strings"

	"m31labs.dev/cicada/notation"
)

// InsertLibraryItem imports a library and binds a track to one of its items.
// On an existing track a new voice goes through settrackvoice's checks that
// do not need the library's declarations (drum lanes, accent and slide
// marks), with acceptchanges and refusals with the same reasons; the compile
// that ends the batch checks the rest.
// The host resolves the library first. PresetInstance names an effect
// instance when Reference is an effect preset. cicada apply does not pin
// libraries: the project's cicada.sum must already pin the library, or the
// edit is refused with CICADA-LIB-HASH (Studio pins it before applying).
//
// Example: {"kind":"insertlibraryitem","track":"glass","importpath":"std/synth","reference":"synth.glassbass","itemkind":"instrument"}
type InsertLibraryItem struct {
	Track          string `json:"track" cicada:"required"`      // the track to bind, or the name of a new track
	ImportPath     string `json:"importpath" cicada:"required"` // the library's import path, such as std/synth
	Reference      string `json:"reference" cicada:"required"`  // the qualified item, such as synth.glassbass
	ItemKind       string `json:"itemkind"`                     // what the item is, such as instrument, preset or effect
	EffectKind     string `json:"effectkind,omitempty"`         // the effect's kind, for an effect item
	PresetInstance string `json:"presetinstance,omitempty"`     // the effect instance's name, for an effect preset
	AcceptChanges  bool   `json:"acceptchanges,omitempty"`      // on an existing track, accept the listed changes to the sound, as settrackvoice does
}

func (InsertLibraryItem) Kind() string { return "insertlibraryitem" }

// SavePreset appends the declaration prepared by the host, including any
// direct import or snapshot of a private library instrument.
//
// Example: {"kind":"savepreset","track":"bass","name":"dark-acid","declaration":"\npreset dark-acid {\n  instrument = acid\n  cutoff = 400Hz\n}\n"}
type SavePreset struct {
	Track       string `json:"track"`                  // the track the preset comes from, for the history label
	Name        string `json:"name" cicada:"required"` // the preset's public name
	Declaration string `json:"declaration"`            // the preset declaration text to append
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
			before := w.Text(field)
			updated, err = ReplaceSpan(updated, ctx.Options.Path, Span{int(field.StartByte()), int(field.EndByte()), in.Reference, ctx.Options.Path})
			if err != nil {
				return err
			}
			// A new voice on an existing track goes through settrackvoice's
			// checks: nothing changes silently.
			if before != in.Reference {
				if updated, err = checkLibraryVoice(ctx, updated, in, before); err != nil {
					return err
				}
			}
		}
		break
	}
	if !found {
		binding := in.Reference
		if in.ItemKind == "effect" {
			// An effect needs a sound to process. The piano plays on every kernel,
			// including the core one that has no keyboard module.
			binding = "piano"
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

// checkLibraryVoice checks the voice change insertlibraryitem makes on an
// existing track, as settrackvoice does, apart from what needs the library's
// declarations: the compile that ends the batch checks that.
func checkLibraryVoice(ctx *Context, updated []byte, in *InsertLibraryItem, before string) ([]byte, error) {
	score, _, err := ctx.ParseProject()
	if err != nil || score == nil {
		return updated, err
	}
	st := newStaged(ctx)
	st.set(ctx.Options.Path, updated)
	node, walker, err := declaration(updated, []string{"track_decl"}, in.Track)
	if err != nil {
		return nil, err
	}
	change, err := checkVoiceChange(st, score, in.Track, before, in.Reference, ctx.Options.Path, int(node.StartByte()), int(walker.Field(node, "kind").EndByte()), nil, false)
	if err != nil {
		return nil, err
	}
	if len(change.invalid) > 0 {
		return nil, &ReasonError{Reason: "voice_change_invalid", Field: "reference", Uses: change.invalid, Message: fmt.Sprintf("track %s cannot change to %s: %s", in.Track, in.Reference, strings.Join(change.invalid, "; "))}
	}
	if len(change.changes) > 0 && !in.AcceptChanges {
		return nil, &ReasonError{Reason: "voice_changes_sound", Field: "reference", Uses: change.changes, Message: fmt.Sprintf("changing track %s to %s changes what it plays: %s; give acceptchanges to accept that", in.Track, in.Reference, strings.Join(change.changes, "; "))}
	}
	for _, c := range change.changes {
		ctx.warn("%s", c)
	}
	return updated, nil
}
