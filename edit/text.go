package edit

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"

	"m31labs.dev/cicada/notation"
)

// ReplaceText saves a complete buffer verbatim, after Apply validates it.
//
// Example: {"kind":"replacetext","source":"cicada 2\ntitle \"Replaced\"\ntempo 120\ntrack bass acid {}\npattern riff { a1 . c2 . }\nscene main { bass = riff }\nsong { main*4 }\n"}
type ReplaceText struct {
	Source string `json:"source" cicada:"required"` // the whole new score text
}

func (ReplaceText) Kind() string { return "replacetext" }

// PatchText changes whole lines of the score, or of another file of the
// project, by hunks that name the lines they replace. Unlike replacetext, it
// keeps another writer's changes to other lines. Old and new are whole lines
// with their line endings ("\r\n" in a CRLF file), and begin and end with the
// same unchanged lines around the change; an insertion needs at least one, so
// old is empty only in an empty file. New ends with a newline unless old
// reaches the end of the file (hunk_newline); end marks a hunk whose old lines
// end the file. A hunk applies where all of old stands at its line, or else at
// the one place in the file where all of old stands (several places:
// hunk_ambiguous). If unchanged lines around the change were edited since, it
// still applies at its own line when up to three of them are missing on each
// side and at least one is intact on each side; it is never fitted elsewhere.
// Otherwise it is refused and nothing is written (hunk_mismatch; the refusal's
// hunk gives the line, what was expected and what was found). Two hunks may
// not change the same lines (hunk_overlap). A patch holds at most 1,024 hunks
// and 8 MiB of old and new together (hunk_limit). A library's source is
// refused (library_file).
//
// Example: {"kind":"patchtext","hunks":[{"line":3,"old":"tempo 120\n","new":"tempo 118\n"}]}
// Example: {"kind":"patchtext","hunks":[{"line":5,"old":"seed 7\n","new":"seed 7\n// patched\n"}]}
type PatchText struct {
	Path  string `json:"path,omitempty"`          // the file to patch: a project file, by its path relative to the score's folder or as the host names it; empty for the score
	Hunks []Hunk `json:"hunks" cicada:"required"` // the changes, each naming the lines it replaces
}

func (PatchText) Kind() string { return "patchtext" }

// ReplaceDeclaration replaces one declaration's syntax span. Declaration is
// a CST node type, or "pattern" for any pattern type. Name is empty for
// unnamed declarations such as title_decl and seed_decl.
//
// Example: {"kind":"replacedeclaration","declaration":"pattern","name":"riff","text":"pattern riff { a1 . a1 . e2 . g2 . }"}
type ReplaceDeclaration struct {
	Declaration string `json:"declaration" cicada:"required" values:"declaration"`                                                                                                                                                                                                                                                // the syntax node type, such as track_decl, scene_decl or title_decl, or pattern
	Name        string `json:"name,omitempty" when:"declaration=instrument_decl|preset_decl|kit_decl|track_decl|phrase_decl|pattern|acid_pattern|note_pattern|drum_pattern|performance_pattern|scene_decl|fx_decl|bus_decl|export_decl|asset_decl|clip_decl|sampler_decl|lfo_decl|envelope_decl|sway_decl|contour_decl|mod_decl"` // the declaration's name; empty for title_decl, tempo_decl, key_decl and seed_decl
	Text        string `json:"text" cicada:"required"`                                                                                                                                                                                                                                                                            // the declaration's new text
}

func (ReplaceDeclaration) Kind() string { return "replacedeclaration" }

// declarationKinds are the values ReplaceDeclaration's declaration accepts:
// "pattern" for any pattern type, then every top-level declaration of the
// grammar (language/grammar/grammar.go, _declaration), in its order.
// TestDeclarationKindsFollowTheGrammar keeps the two equal.
var declarationKinds = valueList("declaration", "pattern",
	"import_decl", "title_decl", "tempo_decl", "key_decl", "seed_decl",
	"instrument_decl", "preset_decl", "kit_decl", "track_decl", "phrase_decl",
	"acid_pattern", "note_pattern", "drum_pattern", "performance_pattern",
	"automate_decl", "scene_decl", "song_decl", "fx_decl", "bus_decl",
	"master_decl", "export_decl", "arrange_decl", "asset_decl", "clip_decl", "sampler_decl", "live_decl",
	"lfo_decl", "envelope_decl", "sway_decl", "contour_decl", "mod_decl",
	"midi_decl")

func init() {
	Register("replacetext", func() Intent { return &ReplaceText{} })
	Register("replacedeclaration", func() Intent { return &ReplaceDeclaration{} })
	Handle("replacetext", func(ctx *Context, intent Intent) error {
		ctx.Source, ctx.plan = []byte(intent.(*ReplaceText).Source), nil
		ctx.SetLabel("Source saved")
		return nil
	})
	Handle("replacedeclaration", replaceDeclaration)
	Register("patchtext", func() Intent { return &PatchText{} })
	Handle("patchtext", patchText)
}

func patchText(ctx *Context, intent Intent) error {
	in := intent.(*PatchText)
	if len(in.Hunks) == 0 {
		return &FieldError{Field: "hunks", Err: fmt.Errorf("patchtext needs at least one hunk")}
	}
	for i, hunk := range in.Hunks {
		if hunk.missing != "" {
			return &FieldError{Field: "hunks." + hunk.missing, Err: fmt.Errorf("hunk %d has no %s; give it, empty if need be", i+1, hunk.missing)}
		}
		if hunk.Line < 1 {
			return &FieldError{Field: "hunks.line", Err: fmt.Errorf("hunk %d: lines count from 1, not %d", i+1, hunk.Line)}
		}
	}
	path := in.Path
	entry := path == "" || path == ctx.Options.Path
	if !entry {
		if _, found := ctx.FileSource(path); !found && !filepath.IsAbs(path) && ctx.Options.Path != "" {
			path = filepath.Join(filepath.Dir(ctx.Options.Path), filepath.FromSlash(path))
			entry = path == ctx.Options.Path
		}
	}
	before := ctx.Source
	if !entry {
		for _, file := range ctx.Options.Sources {
			if file.Path == path && file.Library != "" {
				return refuse("library_file", "path", "%s is a source of library %s; patch a project file instead", in.Path, file.Library)
			}
		}
		var found bool
		if before, found = ctx.FileSource(path); !found {
			return refuse("unknown_file", "path", "%s is not a file of this project", in.Path)
		}
	}
	after, err := ApplyHunks(before, in.Hunks)
	if err != nil {
		var hunk *HunkError
		if !errors.As(err, &hunk) {
			return err
		}
		refusal := hunkRefusal(hunk)
		if !entry {
			refusal.File = path
			refusal.Message += ", in " + in.Path
		}
		refusal.Message += "; nothing was changed"
		return refusal
	}
	if entry {
		ctx.Source, ctx.plan = after, nil
	} else {
		ctx.AddFile(File{Path: path, Before: before, After: after})
	}
	ctx.SetLabel("Source saved")
	return nil
}

type textDeclaration struct {
	start, end       int
	kind, name, text string
}

func textDeclarations(source []byte) ([]textDeclaration, error) {
	document, err := notation.ParseDocument(source)
	if err != nil {
		return nil, err
	}
	var declarations []textDeclaration
	for i := 0; i < document.Root.NamedChildCount(); i++ {
		node := document.Root.NamedChild(i)
		kind := document.Walker.Type(node)
		if kind == "comment" {
			continue
		}
		declarations = append(declarations, textDeclaration{int(node.StartByte()), int(node.EndByte()), kind, document.Walker.Text(document.Walker.Field(node, "name")), document.Walker.Text(node)})
	}
	return declarations, nil
}

func declarationMatches(kind, requested string) bool {
	if kind == requested {
		return true
	}
	if requested == "pattern" {
		for _, candidate := range patternTypes {
			if kind == candidate {
				return true
			}
		}
	}
	return false
}

func replaceDeclaration(ctx *Context, intent Intent) error {
	in := intent.(*ReplaceDeclaration)
	if !containsString(declarationKinds, in.Declaration) {
		return &FieldError{Field: "declaration", Err: fmt.Errorf("unknown declaration type %q; cicada intents replacedeclaration lists the types", in.Declaration)}
	}
	before, err := textDeclarations(ctx.Source)
	if err != nil {
		return err
	}
	var target *textDeclaration
	for i := range before {
		if declarationMatches(before[i].kind, in.Declaration) && before[i].name == in.Name {
			target = &before[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("unknown declaration %q", in.Name)
	}
	after, err := textDeclarations([]byte(in.Text))
	if err != nil {
		return err
	}
	if len(after) != 1 || !declarationMatches(after[0].kind, in.Declaration) || after[0].name != in.Name {
		return fmt.Errorf("replacement must contain one matching declaration")
	}
	updated, err := ReplaceSpan(ctx.Source, ctx.Options.Path, Span{target.start, target.end, in.Text, ctx.Options.Path})
	if err != nil {
		return err
	}
	ctx.Source, ctx.plan = updated, nil
	ctx.SetLabel("Source saved")
	return nil
}

// ReplacePreviewBar replaces the first generated note pattern and seed,
// preserving the header, other patterns and arrangement. Hosts compile the
// candidate before accepting it; this helper validates notation only.
func ReplacePreviewBar(original, variation string) (string, error) {
	read := func(source string) (map[string]textDeclaration, error) {
		declarations, err := textDeclarations([]byte(source))
		if err != nil {
			return nil, err
		}
		found := map[string]textDeclaration{}
		for _, d := range declarations {
			kind := d.kind
			if kind == "acid_pattern" || kind == "note_pattern" {
				kind = "pattern"
			}
			if kind != "pattern" && kind != "seed_decl" {
				continue
			}
			if _, exists := found[kind]; !exists {
				found[kind] = d
			}
		}
		if _, ok := found["pattern"]; !ok {
			return nil, fmt.Errorf("phrase preview has no note pattern")
		}
		return found, nil
	}
	before, err := read(original)
	if err != nil {
		return "", err
	}
	after, err := read(variation)
	if err != nil {
		return "", err
	}
	if before["pattern"].name != after["pattern"].name {
		return "", fmt.Errorf("phrase preview pattern changed")
	}
	pattern := before["pattern"]
	updated := original[:pattern.start] + after["pattern"].text + original[pattern.end:]
	if seed, ok := before["seed_decl"]; ok {
		newSeed, exists := after["seed_decl"]
		if !exists || seed.end > pattern.start {
			return "", fmt.Errorf("phrase preview seed is unavailable")
		}
		updated = updated[:seed.start] + newSeed.text + updated[seed.end:]
	}
	_, diagnostics := notation.Parse([]byte(updated))
	for _, d := range diagnostics {
		if d.Severity == "error" {
			return "", fmt.Errorf("variation is invalid: %s", d.Message)
		}
	}
	return updated, nil
}

// hunkRefusal turns a hunk that does not apply into the intent's refusal, with
// the hunk's fields.
func hunkRefusal(h *HunkError) *ReasonError {
	detail := &HunkRefusal{Index: h.Hunk, Line: h.Line, At: h.At, Expected: h.Expected, Found: h.Found, Matches: h.Matches, Places: h.Places}
	switch h.Reason {
	case "hunk_ambiguous":
		return &ReasonError{Reason: "hunk_ambiguous", Field: "hunks", Message: h.Message, Hunk: detail}
	case "hunk_overlap":
		other := h.Other
		detail.Other = &other
		return &ReasonError{Reason: "hunk_overlap", Field: "hunks", Message: h.Message, Hunk: detail}
	case "hunk_needs_context":
		return &ReasonError{Reason: "hunk_needs_context", Field: "hunks", Message: h.Message, Hunk: detail}
	case "hunk_newline":
		return &ReasonError{Reason: "hunk_newline", Field: "hunks", Message: h.Message, Hunk: detail}
	case "hunk_limit":
		limit := &ValueRange{Parameter: "hunks", Value: strconv.Itoa(h.Value), Min: 1, Max: float64(h.Limit), Unit: "hunks"}
		if h.Limit == MaxPatchBytes {
			limit.Parameter, limit.Min, limit.Unit = "hunk bytes", 0, "bytes"
		}
		return &ReasonError{Reason: "hunk_limit", Field: "hunks", Message: h.Message, Range: limit}
	}
	return &ReasonError{Reason: "hunk_mismatch", Field: "hunks", Message: h.Message, Hunk: detail}
}
