package language

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	gts "github.com/odvcencio/gotreesitter"
)

// ErrorCapture names spans over text the parser could not place.
const ErrorCapture = "error"

// Span is one highlight capture over a byte range of the source. Spans nest
// like the syntax tree: an accented note is a markup.strong span around its
// constant.pitch.degree and operator.accent spans.
type Span struct {
	Start   int    `json:"start"`
	End     int    `json:"end"`
	Capture string `json:"capture"`
	pattern int
}

// Highlight parses src and returns its highlight spans, each enclosing span
// before the spans inside it. A parameter or let reference takes the capture
// of the definition it resolves to, as locals.scm describes, and text the
// parser could not place gets an ErrorCapture span. The error result reports
// only a failure to load the grammar or queries; syntax errors become spans.
func Highlight(src []byte) ([]Span, error) {
	tree, q, err := parse(src)
	if err != nil {
		return nil, err
	}
	defer tree.Release()
	var spans []Span
	byNode := make(map[nodeKey]int)
	for _, match := range q.highlights.Execute(tree) {
		for _, capture := range match.Captures {
			start, end := capture.ByteRange()
			if start == end {
				continue
			}
			byNode[keyOf(capture.Node)] = len(spans)
			spans = append(spans, Span{Start: int(start), End: int(end), Capture: capture.Name, pattern: match.PatternIndex})
		}
	}
	resolveLocals(q, tree, spans, byNode)
	spans = append(spans, errorSpans(tree.RootNode(), q.lang, len(src))...)
	sort.SliceStable(spans, func(i, j int) bool {
		a, b := spans[i], spans[j]
		if a.Start != b.Start {
			return a.Start < b.Start
		}
		if a.End != b.End {
			return a.End > b.End
		}
		return a.pattern < b.pattern
	})
	return spans, nil
}

type nodeKey struct {
	start, end uint32
	symbol     gts.Symbol
}

func keyOf(n *gts.Node) nodeKey {
	return nodeKey{start: n.StartByte(), end: n.EndByte(), symbol: n.Symbol()}
}

// resolveLocals gives each reference the capture of the definition it names,
// as tree-sitter's highlighter does with a locals query: the innermost scope
// is searched first, and only definitions earlier in the source count.
func resolveLocals(q *compiled, tree *gts.Tree, spans []Span, byNode map[nodeKey]int) {
	type definition struct {
		name    string
		start   uint32
		capture string
	}
	type scope struct {
		start, end  uint32
		definitions []definition
	}
	var scopes []*scope
	var definitions, references []*gts.Node
	for _, match := range q.locals.Execute(tree) {
		for _, capture := range match.Captures {
			switch {
			case capture.Name == "local.scope":
				scopes = append(scopes, &scope{start: capture.Node.StartByte(), end: capture.Node.EndByte()})
			case strings.HasPrefix(capture.Name, "local.definition"):
				definitions = append(definitions, capture.Node)
			case capture.Name == "local.reference":
				references = append(references, capture.Node)
			}
		}
	}
	// Narrowest first, so the first scope that contains a node is its own.
	sort.Slice(scopes, func(i, j int) bool { return scopes[i].end-scopes[i].start < scopes[j].end-scopes[j].start })
	source := tree.Source()
	for _, node := range definitions {
		index, ok := byNode[keyOf(node)]
		if !ok {
			continue
		}
		for _, s := range scopes {
			if s.start <= node.StartByte() && node.EndByte() <= s.end {
				s.definitions = append(s.definitions, definition{name: node.Text(source), start: node.StartByte(), capture: spans[index].Capture})
				break
			}
		}
	}
	for _, node := range references {
		index, ok := byNode[keyOf(node)]
		if !ok {
			continue
		}
		name := node.Text(source)
	search:
		for _, s := range scopes {
			if s.start > node.StartByte() || node.EndByte() > s.end {
				continue
			}
			for i := len(s.definitions) - 1; i >= 0; i-- {
				if d := s.definitions[i]; d.name == name && d.start < node.StartByte() {
					spans[index].Capture = d.capture
					break search
				}
			}
		}
	}
}

// errorSpans covers ERROR nodes, and one byte at each missing token, so a
// broken score still shows where parsing gave up.
func errorSpans(root *gts.Node, lang *gts.Language, size int) []Span {
	if root == nil || !root.HasErrorOrMissing() || size == 0 {
		return nil
	}
	var spans []Span
	var walk func(n *gts.Node)
	walk = func(n *gts.Node) {
		if n.IsMissing() {
			start := min(int(n.StartByte()), size-1)
			spans = append(spans, Span{Start: start, End: start + 1, Capture: ErrorCapture, pattern: -1})
			return
		}
		if (n.IsError() || n.Type(lang) == "ERROR") && n.EndByte() > n.StartByte() {
			spans = append(spans, Span{Start: int(n.StartByte()), End: int(n.EndByte()), Capture: ErrorCapture, pattern: -1})
		}
		for i := 0; i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(root)
	return spans
}

// WriteSpans lists spans one per line as line:column, capture, and quoted
// text. Columns count Unicode scalars from one, like Cicada diagnostics.
func WriteSpans(w io.Writer, src []byte, spans []Span) error {
	var b strings.Builder
	for _, span := range spans {
		line, column := position(src, span.Start)
		fmt.Fprintf(&b, "%-8s %-28s %s\n", strconv.Itoa(line)+":"+strconv.Itoa(column), span.Capture, strconv.Quote(string(src[span.Start:span.End])))
	}
	_, err := io.WriteString(w, b.String())
	return err
}
