// Package migration applies meaning-preserving source rewrites shared by the CLI and editor.
package migration

import (
	"bytes"
	"fmt"
	"reflect"
	"sort"
	"strings"

	gts "github.com/odvcencio/gotreesitter"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

type sourceEdit struct {
	start, end int
	text       string
}

func FixSource(source []byte) ([]byte, bool, error) {
	before, diagnostics := notation.Parse(source)
	if hasDiagnosticErrors(diagnostics) {
		return nil, false, fmt.Errorf("score must validate before fix: %+v", diagnostics)
	}
	projectBefore, diagnostics := project.FromScore(before)
	if projectBefore == nil || hasDiagnosticErrors(diagnostics) {
		return nil, false, fmt.Errorf("score must compile before fix: %+v", diagnostics)
	}
	root, walker, err := notation.ParseTree(source)
	if err != nil {
		return nil, false, err
	}
	var edits []sourceEdit
	movedAttrs := map[int]bool{}
	for i := 0; i < root.NamedChildCount(); i++ {
		node := root.NamedChild(i)
		switch walker.Type(node) {
		case "integer":
			start := bytes.LastIndexByte(source[:node.StartByte()], '\n') + 1
			end := int(node.EndByte())
			for end < len(source) && source[end] != '\n' {
				end++
			}
			if strings.TrimSpace(string(source[start:end])) != "cicada 1" {
				return nil, false, fmt.Errorf("cannot migrate nonstandalone legacy header")
			}
			if end < len(source) {
				end++
			}
			// The blank line after a legacy header was its section separator.
			if end < len(source) && source[end] == '\n' {
				end++
			} else if end+1 < len(source) && source[end] == '\r' && source[end+1] == '\n' {
				end += 2
			}
			edits = append(edits, sourceEdit{start: start, end: end})
		case "instrument_decl":
			var explicit bool
			for j := 0; j < node.NamedChildCount(); j++ {
				if walker.Type(node.NamedChild(j)) == "instrument_octave" {
					explicit = true
					break
				}
			}
			if explicit {
				continue
			}
			span := source[node.StartByte():node.EndByte()]
			brace := bytes.IndexByte(span, '{')
			if brace < 0 {
				return nil, false, fmt.Errorf("instrument has no opening brace")
			}
			offset := int(node.StartByte()) + brace + 1
			insert := " octave = 2"
			if bytes.IndexByte(span[brace+1:], '\n') >= 0 {
				newline := "\n"
				if bytes.Contains(source, []byte("\r\n")) {
					newline = "\r\n"
				}
				insert = newline + "  octave = 2"
			}
			edits = append(edits, sourceEdit{start: offset, end: offset, text: insert})
		case "acid_pattern", "note_pattern", "drum_pattern":
			if walker.Type(node) == "note_pattern" {
				for j := 0; j < node.ChildCount(); j++ {
					child := node.Child(j)
					if child.ChildCount() == 0 && walker.Type(child) == "notes" {
						edits = append(edits, sourceEdit{start: precedingSpace(source, int(child.StartByte())), end: int(child.EndByte())})
					}
				}
			}
			brace := -1
			for j := 0; j < node.ChildCount(); j++ {
				if walker.Type(node.Child(j)) == "{" {
					brace = int(node.Child(j).StartByte())
					break
				}
			}
			if brace < 0 {
				return nil, false, fmt.Errorf("pattern has no opening brace")
			}
			var settings []string
			for j := 0; j < node.NamedChildCount(); j++ {
				attr := node.NamedChild(j)
				if walker.Type(attr) != "pattern_attr" || int(attr.StartByte()) > brace {
					continue
				}
				nameNode, valueNode := walker.Field(attr, "name"), walker.Field(attr, "value")
				if nameNode == nil || valueNode == nil || walker.Text(nameNode) == "steps" || bytes.Contains(source[attr.StartByte():attr.EndByte()], []byte("//")) {
					continue
				}
				name, value := walker.Text(nameNode), canonicalFixUnit(walker.Text(valueNode))
				if (name == "swing" || name == "gate") && !strings.HasSuffix(value, "%") {
					value += "%"
				}
				settings = append(settings, name+" = "+value)
				edits = append(edits, sourceEdit{start: precedingSpace(source, int(attr.StartByte())), end: int(attr.EndByte())})
				movedAttrs[int(attr.StartByte())] = true
			}
			if len(settings) > 0 {
				insert := " " + strings.Join(settings, " ") + " "
				if bytes.IndexByte(source[brace+1:node.EndByte()], '\n') >= 0 {
					newline := "\n"
					if bytes.Contains(source, []byte("\r\n")) {
						newline = "\r\n"
					}
					insert = newline + "  " + strings.Join(settings, newline+"  ")
				}
				edits = append(edits, sourceEdit{start: brace + 1, end: brace + 1, text: insert})
			}
		}
	}
	var collectTokens func(*gts.Node)
	collectTokens = func(node *gts.Node) {
		switch walker.Type(node) {
		case "pattern_attr":
			if movedAttrs[int(node.StartByte())] {
				return
			}
			if name := walker.Field(node, "name"); name != nil && walker.Text(name) == "steps" && !bytes.Contains(source[node.StartByte():node.EndByte()], []byte("//")) {
				edits = append(edits, sourceEdit{start: precedingSpace(source, int(node.StartByte())), end: int(node.EndByte())})
				return
			}
		case "phrase_use":
			if value := walker.Field(node, "transpose"); value != nil && source[value.StartByte()] != '-' {
				prefix := source[node.StartByte():value.StartByte()]
				if index := bytes.Index(prefix, []byte("transpose")); index >= 0 && !bytes.Contains(prefix[index:], []byte("//")) {
					start := int(node.StartByte()) + index
					edits = append(edits, sourceEdit{start: start, end: int(value.StartByte()), text: "+"})
				}
			}
		case "phrase_decl":
			for i := 0; i < node.ChildCount(); i++ {
				child := node.Child(i)
				if child.ChildCount() == 0 && walker.Type(child) == "acid" {
					edits = append(edits, sourceEdit{start: precedingSpace(source, int(child.StartByte())), end: int(child.EndByte())})
				}
			}
		case "probability":
			if source[node.StartByte()] != '%' {
				break
			}
			start := int(node.StartByte())
			edits = append(edits, sourceEdit{start: start, end: start + 1, text: "?"})
		case "number":
			if node.ChildCount() == 0 {
				original := walker.Text(node)
				replacement := canonicalFixUnit(original)
				if replacement != original {
					edits = append(edits, sourceEdit{start: int(node.StartByte()), end: int(node.EndByte()), text: replacement})
				}
			}
		}
		if node.ChildCount() == 0 && walker.Type(node) == ";" {
			// Add a separator only when adjacent tokens would touch.
			replacement := ""
			if end := int(node.EndByte()); end < len(source) && source[end] != ' ' && source[end] != '\t' && source[end] != '\r' && source[end] != '\n' && source[end] != '}' {
				replacement = " "
			}
			edits = append(edits, sourceEdit{start: int(node.StartByte()), end: int(node.EndByte()), text: replacement})
		}
		for i := 0; i < node.ChildCount(); i++ {
			collectTokens(node.Child(i))
		}
	}
	collectTokens(root)
	if len(edits) == 0 {
		return bytes.Clone(source), false, nil
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })
	fixed := bytes.Clone(source)
	for _, edit := range edits {
		fixed = append(append(bytes.Clone(fixed[:edit.start]), edit.text...), fixed[edit.end:]...)
	}
	after, diagnostics := notation.Parse(fixed)
	if hasDiagnosticErrors(diagnostics) {
		return nil, false, fmt.Errorf("fix generated invalid score: %+v", diagnostics)
	}
	projectAfter, diagnostics := project.FromScore(after)
	if projectAfter == nil || hasDiagnosticErrors(diagnostics) || !reflect.DeepEqual(projectBefore, projectAfter) {
		return nil, false, fmt.Errorf("fix changed musical meaning")
	}
	return fixed, true, nil
}

func canonicalFixUnit(value string) string {
	switch {
	case strings.HasSuffix(value, "khz"):
		return strings.TrimSuffix(value, "khz") + "kHz"
	case strings.HasSuffix(value, "hz"):
		return strings.TrimSuffix(value, "hz") + "Hz"
	case strings.HasSuffix(value, "db"):
		return strings.TrimSuffix(value, "db") + "dB"
	default:
		return value
	}
}

func precedingSpace(source []byte, start int) int {
	for start > 0 && (source[start-1] == ' ' || source[start-1] == '\t') {
		start--
	}
	return start
}

func hasDiagnosticErrors(diagnostics []notation.Diagnostic) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			return true
		}
	}
	return false
}
