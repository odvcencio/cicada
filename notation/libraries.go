package notation

import (
	"strconv"
	"strings"

	gts "github.com/odvcencio/gotreesitter"
)

type Import struct {
	Path     string
	Position Position
}
type Origin struct {
	Library  string
	Position Position
}

// ReadImports inspects source without requiring a complete song or resolving
// declarations. Imports are collected across a scope before lowering it.
func ReadImports(file SourceFile) []Import {
	root, walker, _ := ParseTree(file.Source)
	if root == nil || walker == nil {
		return nil
	}
	w := &loweringWalker{Walker: walker, file: file.Path}
	var imports []Import
	for i := 0; i < root.NamedChildCount(); i++ {
		node := root.NamedChild(i)
		if w.Type(node) == "import_decl" {
			value, _ := strconv.Unquote(w.Text(w.Field(node, "path")))
			imports = append(imports, Import{Path: value, Position: w.position(node)})
		}
	}
	return imports
}

// SourceEdition reads the optional edition marker of a loose score.
func SourceEdition(file SourceFile) int {
	root, w, _ := ParseTree(file.Source)
	if root != nil && w != nil {
		for i := 0; i < root.NamedChildCount(); i++ {
			n := root.NamedChild(i)
			if w.Type(n) == "integer" {
				value, _ := strconv.Atoi(w.Text(n))
				return value
			}
		}
	}
	return 1
}

// CheckLibrary rejects score-only declarations and asset paths outside audio/.
func CheckLibrary(file SourceFile) []Diagnostic {
	root, walker, err := ParseTree(file.Source)
	if err != nil {
		d := syntaxDiagnostic(err, file.Source)
		d.Position.File = file.Path
		return []Diagnostic{d}
	}
	w := &loweringWalker{Walker: walker, file: file.Path}
	var ds []Diagnostic
	for i := 0; i < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		kind := w.Type(n)
		switch kind {
		case "integer", "comment", "import_decl", "instrument_decl", "kit_decl", "fx_decl", "phrase_decl", "acid_pattern", "note_pattern", "drum_pattern", "sampler_decl", "asset_decl":
		default:
			ds = append(ds, Diagnostic{Code: "CICADA-LIB-DECL", Severity: "error", Message: strings.TrimSuffix(kind, "_decl") + " cannot be declared in a library", Position: w.position(n)})
		}
		if kind == "asset_decl" {
			value, _ := strconv.Unquote(w.Text(w.Field(n, "path")))
			if !ValidAssetPath(value) || !strings.HasPrefix(value, "audio/") {
				ds = append(ds, Diagnostic{Code: "CICADA-ASSET-PATH", Severity: "error", Message: "library assets must live under audio/", Position: w.position(n)})
			}
		}
		if name := w.Field(n, "name"); name != nil && strings.Contains(w.Text(name), ".") {
			ds = append(ds, Diagnostic{Code: "CICADA-LIB-DECL", Severity: "error", Message: "library declaration names must be unqualified", Position: w.position(name)})
		}
	}
	return ds
}

func (w *loweringWalker) declaration(n *gts.Node) string {
	name := strings.Join(strings.Fields(w.Text(n)), "")
	if w.library != "" {
		name = strings.ReplaceAll(w.library, "/", ".") + "." + name
	}
	if w.origins != nil && w.library != "" {
		w.origins[name] = Origin{Library: w.library, Position: w.position(n)}
	}
	return name
}

func (w *loweringWalker) reference(n *gts.Node) string {
	return w.referenceText(w.Text(n), w.position(n))
}
func (w *loweringWalker) referenceText(name string, position Position) string {
	name = strings.Join(strings.Fields(name), "")
	first, rest, qualified := strings.Cut(name, ".")
	if qualified && first == "builtin" {
		return name
	}
	if qualified {
		if namespace, found := w.bindings[first]; found {
			if strings.Contains(rest, ".") {
				*w.diagnostics = append(*w.diagnostics, Diagnostic{Code: "CICADA-LIB-REFERENCE", Severity: "error", Message: "import references must name a declaration directly owned by the bound library: " + name, Position: position})
				return name
			}
			if strings.HasPrefix(rest, "_") {
				*w.diagnostics = append(*w.diagnostics, Diagnostic{Code: "CICADA-LIB-PRIVATE", Severity: "error", Message: "library declaration " + name + " is private", Position: position})
			}
			return namespace + "." + rest
		}
		// Flattened source may contain fully qualified declarations. Only sources
		// loaded with an import scope require explicit import bindings.
		if w.bindings != nil {
			*w.diagnostics = append(*w.diagnostics, Diagnostic{Code: "CICADA-LIB-REFERENCE", Severity: "error", Message: "no import binds namespace " + first, Position: position})
		}
		return name
	}
	if w.library != "" {
		if w.declarations[name] {
			return strings.ReplaceAll(w.library, "/", ".") + "." + name
		}
		switch name {
		case "acid", "drums", "piano", "audio", "off", "keep", "music", "sfx", "master":
			return name
		}
		return strings.ReplaceAll(w.library, "/", ".") + "." + name
	}
	return name
}

// DeclarationNames collects a scope's global names before references are read.
func DeclarationNames(files []SourceFile) map[string]bool {
	names := map[string]bool{}
	for _, file := range files {
		root, w, _ := ParseTree(file.Source)
		if root == nil || w == nil {
			continue
		}
		for i := 0; i < root.NamedChildCount(); i++ {
			if name := w.Field(root.NamedChild(i), "name"); name != nil {
				names[w.Text(name)] = true
			}
		}
	}
	return names
}
