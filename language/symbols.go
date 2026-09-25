package language

import (
	"sort"
	"strings"
	"unicode/utf8"

	gts "github.com/odvcencio/gotreesitter"

	"m31labs.dev/cicada/notation"
)

// Symbol is a definition or reference that tags.scm finds in a score.
type Symbol struct {
	Role     string            `json:"role"` // definition or reference
	Kind     string            `json:"kind"` // instrument, parameter, binding, track, effect, phrase, pattern, or scene
	Name     string            `json:"name"`
	Position notation.Position `json:"position"`
}

// Symbols returns the definitions and references in src in source order.
// Tracks, patterns, phrases, and scenes have separate namespaces, so Kind
// tells a track called bass from a pattern called bass. A syntax error
// leaves out the symbols the parser could not place.
func Symbols(src []byte) ([]Symbol, error) {
	tree, _, err := parse(src)
	if err != nil {
		return nil, err
	}
	defer tree.Release()
	tagger, err := NewTagger()
	if err != nil {
		return nil, err
	}
	tags := tagger.TagTree(tree)
	sort.SliceStable(tags, func(i, j int) bool { return tags[i].NameRange.StartByte < tags[j].NameRange.StartByte })
	symbols := make([]Symbol, 0, len(tags))
	for i, tag := range tags {
		role, kind, _ := strings.Cut(tag.Kind, ".")
		if tag.Kind == "reference.binding" {
			kind = resolveBinding(tags, i)
		}
		line, column := position(src, int(tag.NameRange.StartByte))
		symbols = append(symbols, Symbol{Role: role, Kind: kind, Name: tag.Name, Position: notation.Position{Line: line, Column: column}})
	}
	return symbols, nil
}

// resolveBinding names what an expression reference points at: the last
// parameter or let of that name before it in the same instrument, as in
// locals.scm. An unresolved name stays a binding; validation reports it.
func resolveBinding(tags []gts.Tag, ref int) string {
	at := tags[ref].NameRange.StartByte
	var within gts.Range
	for _, tag := range tags[:ref] {
		if tag.Kind == "definition.instrument" && tag.Range.StartByte <= at && at < tag.Range.EndByte {
			within = tag.Range
		}
	}
	for i := ref - 1; i >= 0 && tags[i].NameRange.StartByte >= within.StartByte; i-- {
		tag := tags[i]
		if tag.Name == tags[ref].Name && (tag.Kind == "definition.parameter" || tag.Kind == "definition.binding") {
			return strings.TrimPrefix(tag.Kind, "definition.")
		}
	}
	return "binding"
}

// position converts a byte offset to a one-based line and Unicode scalar
// column.
func position(src []byte, offset int) (line, column int) {
	offset = min(max(offset, 0), len(src))
	start := 0
	line = 1
	for i := 0; i < offset; i++ {
		if src[i] == '\n' {
			line++
			start = i + 1
		}
	}
	return line, utf8.RuneCount(src[start:offset]) + 1
}
