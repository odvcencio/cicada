package lsp

import (
	"bytes"
	"regexp"
	"sort"
	"strings"

	gts "github.com/odvcencio/gotreesitter"

	"m31labs.dev/cicada/language"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

var identifier = regexp.MustCompile(`^[a-z_][a-z0-9_-]*$`)

func symbolAt(source []byte, at position) (language.Symbol, []language.Symbol, bool) {
	symbols, err := language.Symbols(source)
	if err != nil {
		return language.Symbol{}, nil, false
	}
	for _, symbol := range symbols {
		if symbol.Kind == "preset-target" {
			score, _ := notation.Parse(source)
			symbols = resolvePresetSymbols(symbols, score)
			break
		}
	}
	offset := byteOffset(source, at)
	for _, symbol := range symbols {
		start := scalarOffset(source, symbol.Position)
		if start <= offset && offset < start+len(symbol.Name) {
			return symbol, symbols, true
		}
	}
	return language.Symbol{}, symbols, false
}

func resolvePresetSymbols(symbols []language.Symbol, score *notation.Score) []language.Symbol {
	if score == nil {
		return symbols
	}
	for i, symbol := range symbols {
		if symbol.Kind != "preset-target" {
			continue
		}
		name := symbol.Name
		if first, rest, ok := strings.Cut(name, "."); ok {
			if namespace, found := score.LibraryAliases[first]; found {
				name = namespace + "." + rest
			}
		}
		kind, found := notation.PresetTarget(score, name)
		if !found {
			continue
		}
		symbols[i].Kind = "voice"
		if kind == "effect" {
			symbols[i].Kind = "effect"
		}
	}
	return symbols
}

type byteRange struct{ start, end int }

func instrumentScope(source []byte, offset int) byteRange {
	root, walker, err := notation.ParseTree(source)
	if err != nil {
		return byteRange{}
	}
	for i := 0; i < root.NamedChildCount(); i++ {
		node := root.NamedChild(i)
		if walker.Type(node) == "instrument_decl" && int(node.StartByte()) <= offset && offset < int(node.EndByte()) {
			return byteRange{int(node.StartByte()), int(node.EndByte())}
		}
	}
	return byteRange{}
}

func sameSymbol(source []byte, selected, candidate language.Symbol, scope byteRange) bool {
	if selected.Name != candidate.Name || symbolFamily(selected.Kind) != symbolFamily(candidate.Kind) {
		return false
	}
	if selected.Kind == "parameter" || selected.Kind == "binding" {
		at := scalarOffset(source, candidate.Position)
		return scope.end > scope.start && scope.start <= at && at < scope.end
	}
	return true
}

func symbolRegion(source []byte, symbol language.Symbol) region {
	start := scalarOffset(source, symbol.Position)
	return region{Start: utf16Position(source, start), End: utf16Position(source, start+len(symbol.Name))}
}

func definition(uri string, source []byte, at position) any {
	if match, ok := parameterPathAt(source, byteOffset(source, at)); ok {
		return parameterPathDefinition(uri, source, match)
	}
	selected, symbols, ok := symbolAt(source, at)
	if !ok {
		return nil
	}
	scope := instrumentScope(source, scalarOffset(source, selected.Position))
	for _, symbol := range symbols {
		if symbol.Role == "definition" && sameSymbol(source, selected, symbol, scope) {
			return map[string]any{"uri": uri, "range": symbolRegion(source, symbol)}
		}
	}
	return nil
}

type replacement struct {
	start, end int
	text       string
}

func rename(uri string, source []byte, at position, newName string) any {
	if !identifier.MatchString(newName) {
		return nil
	}
	selected, symbols, ok := symbolAt(source, at)
	if !ok {
		if match, found := parameterPathAt(source, byteOffset(source, at)); found {
			owner, _, _ := strings.Cut(match.path, ".")
			for _, symbol := range symbols {
				if symbol.Role == "definition" && (symbol.Kind == "track" || symbol.Kind == "effect" || symbol.Kind == "bus") && symbol.Name == owner {
					selected, ok = symbol, true
					break
				}
			}
		}
	}
	if !ok || selected.Name == newName {
		return nil
	}
	scope := instrumentScope(source, scalarOffset(source, selected.Position))
	var edits []map[string]any
	var replacements []replacement
	for _, symbol := range symbols {
		if !sameSymbol(source, selected, symbol, scope) {
			continue
		}
		start := scalarOffset(source, symbol.Position)
		edits = append(edits, map[string]any{"range": symbolRegion(source, symbol), "newText": newName})
		replacements = append(replacements, replacement{start, start + len(symbol.Name), newName})
	}
	if selected.Kind == "track" {
		root, walker, err := notation.ParseTree(source)
		if err != nil {
			return nil
		}
		var visit func(*gts.Node)
		visit = func(node *gts.Node) {
			if walker.Type(node) == "parameter_path" {
				path := pathRegion{path: walker.Text(node), start: int(node.StartByte()), end: int(node.EndByte())}
				if start, end, found := pathOwnerPrefix(path, selected.Name); found {
					edits = append(edits, map[string]any{"range": region{Start: utf16Position(source, start), End: utf16Position(source, end)}, "newText": newName})
					replacements = append(replacements, replacement{start: start, end: end, text: newName})
				}
			}
			for i := 0; i < node.ChildCount(); i++ {
				visit(node.Child(i))
			}
		}
		visit(root)
	} else if selected.Kind == "effect" || selected.Kind == "bus" {
		root, walker, err := notation.ParseTree(source)
		if err != nil {
			return nil
		}
		var visit func(*gts.Node)
		visit = func(node *gts.Node) {
			if walker.Type(node) == "parameter_path" {
				path := pathRegion{path: walker.Text(node), start: int(node.StartByte()), end: int(node.EndByte())}
				if start, end, found := pathOwnerPrefix(path, selected.Name); found {
					edits = append(edits, map[string]any{"range": region{Start: utf16Position(source, start), End: utf16Position(source, end)}, "newText": newName})
					replacements = append(replacements, replacement{start: start, end: end, text: newName})
				}
			}
			for i := 0; i < node.ChildCount(); i++ {
				visit(node.Child(i))
			}
		}
		visit(root)
	}
	if len(edits) == 0 {
		return nil
	}
	sort.Slice(replacements, func(i, j int) bool { return replacements[i].start > replacements[j].start })
	changed := bytes.Clone(source)
	for _, edit := range replacements {
		changed = append(append(bytes.Clone(changed[:edit.start]), edit.text...), changed[edit.end:]...)
	}
	score, diagnostics := notation.Parse(changed)
	if score == nil || hasErrors(diagnostics) {
		return nil
	}
	if p, extra := project.FromScore(score); p == nil || hasErrors(extra) {
		return nil
	}
	return map[string]any{"changes": map[string]any{uri: edits}}
}
