package edit

import (
	"bytes"
	"fmt"
	"sort"
	"unicode/utf8"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/taproot/walk"
	"m31labs.dev/cicada/notation"
)

// Offset converts a 1-based line and column (columns count runes) to a byte
// offset in source. An empty position file denotes a standalone parse of source.
// Project positions must belong to targetFile. Ports studioSourceOffset.
func Offset(source []byte, targetFile string, at notation.Position) (int, error) {
	if at.File != "" && at.File != targetFile {
		return 0, fmt.Errorf("source position belongs to another source file; edit its owning file")
	}
	line, offset := 1, 0
	for offset < len(source) && line < at.Line {
		if source[offset] == '\n' {
			line++
		}
		offset++
	}
	for column := 1; offset < len(source) && column < at.Column && source[offset] != '\n'; column++ {
		_, size := utf8.DecodeRune(source[offset:])
		offset += size
	}
	return offset, nil
}

// Span replaces source[Start:End] with Text. File identifies the source that
// supplied the coordinates; it must equal the patch target, even for insertions.
type Span struct {
	Start, End int
	Text       string
	File       string
}

// PatchSpans applies disjoint spans from the end so offsets refer to the
// original source throughout. Everything outside the spans stays
// byte-identical. Ports patchStudioSpans.
func PatchSpans(source []byte, targetFile string, spans []Span) ([]byte, error) {
	for _, span := range spans {
		if span.File != targetFile {
			return nil, fmt.Errorf("source span belongs to another source file; edit its owning file")
		}
	}
	spans = append([]Span(nil), spans...)
	sort.Slice(spans, func(i, j int) bool { return spans[i].Start > spans[j].Start })
	updated := bytes.Clone(source)
	limit := len(source)
	for _, span := range spans {
		if span.Start < 0 || span.End < span.Start || span.End > limit {
			return nil, fmt.Errorf("overlapping or invalid source patches")
		}
		var err error
		updated, err = ReplaceSpan(updated, targetFile, span)
		if err != nil {
			return nil, err
		}
		limit = span.Start
	}
	return updated, nil
}

// ReplaceSpan ports replaceSongSpan; the error text is kept verbatim for parity.
func ReplaceSpan(source []byte, targetFile string, span Span) ([]byte, error) {
	if span.File != targetFile {
		return nil, fmt.Errorf("source span belongs to another source file; edit its owning file")
	}
	start, end, text := span.Start, span.End, span.Text
	if start < 0 || end < start || end > len(source) {
		return nil, fmt.Errorf("song source span is invalid")
	}
	updated := make([]byte, 0, len(source)-end+start+len(text))
	updated = append(updated, source[:start]...)
	updated = append(updated, text...)
	updated = append(updated, source[end:]...)
	return updated, nil
}

// Newline returns "\r\n" when the source uses it, else "\n".
func Newline(source []byte) string {
	if bytes.Contains(source, []byte("\r\n")) {
		return "\r\n"
	}
	return "\n"
}

// declaration finds a top-level declaration of one of the given node types by
// name. Ports studioDeclaration.
func declaration(source []byte, types []string, name string) (*gts.Node, *walk.Walker, error) {
	root, walker, err := notation.ParseTree(source)
	if err != nil {
		return nil, nil, err
	}
	for i := 0; i < root.NamedChildCount(); i++ {
		node := root.NamedChild(i)
		for _, kind := range types {
			if walker.Type(node) == kind && walker.Text(walker.Field(node, "name")) == name {
				return node, walker, nil
			}
		}
	}
	return nil, nil, fmt.Errorf("unknown declaration %q", name)
}
