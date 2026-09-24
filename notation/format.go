package notation

import (
	"bytes"
	"fmt"
	"strings"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/taproot/walk"
)

// Document keeps the exact source alongside its concrete syntax tree. Editors
// can inspect the tree and still print the original text byte for byte.
type Document struct {
	Root   *gts.Node
	Walker *walk.Walker
	source []byte
}

func ParseDocument(source []byte) (*Document, error) {
	owned := bytes.Clone(source)
	root, walker, err := ParseTree(owned)
	if err != nil {
		return nil, err
	}
	return &Document{Root: root, Walker: walker, source: owned}, nil
}

func Print(document *Document) []byte {
	if document == nil {
		return nil
	}
	return bytes.Clone(document.source)
}

// Format normalizes whitespace while retaining comments, string contents,
// pitch spelling, and note modifiers. It only accepts grammar-valid source.
func Format(document *Document) ([]byte, error) {
	if document == nil || document.Root == nil || document.Walker == nil {
		return nil, fmt.Errorf("nil Cicada document")
	}
	var sections []string
	var comments []string
	for i := 0; i < document.Root.NamedChildCount(); i++ {
		node := document.Root.NamedChild(i)
		kind := document.Walker.Type(node)
		if kind == "comment" {
			comments = append(comments, strings.TrimSpace(document.Walker.Text(node)))
			continue
		}
		var section string
		if kind == "integer" {
			section = "cicada " + document.Walker.Text(node)
		} else {
			start, end := node.StartByte(), node.EndByte()
			if int(end) > len(document.source) || start > end {
				return nil, fmt.Errorf("invalid syntax span")
			}
			section = formatDeclaration(document.source[start:end])
		}
		if len(comments) > 0 {
			section = strings.Join(comments, "\n") + "\n" + section
			comments = nil
		}
		sections = append(sections, section)
	}
	if len(comments) > 0 {
		sections = append(sections, strings.Join(comments, "\n"))
	}
	return []byte(strings.Join(sections, "\n\n") + "\n"), nil
}

type formatToken struct {
	text    string
	comment bool
}

func lexFormat(source []byte) []formatToken {
	var tokens []formatToken
	for i := 0; i < len(source); {
		c := source[i]
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			i++
			continue
		}
		start := i
		if c == '/' && i+1 < len(source) && source[i+1] == '/' {
			i += 2
			for i < len(source) && source[i] != '\n' {
				i++
			}
			tokens = append(tokens, formatToken{text: string(source[start:i]), comment: true})
			continue
		}
		if c == '"' {
			i++
			for i < len(source) {
				if source[i] == '\\' && i+1 < len(source) {
					i += 2
				} else if source[i] == '"' {
					i++
					break
				} else {
					i++
				}
			}
			tokens = append(tokens, formatToken{text: string(source[start:i])})
			continue
		}
		if strings.ContainsRune("{}:;=(),|", rune(c)) {
			i++
			tokens = append(tokens, formatToken{text: string(c)})
			continue
		}
		for i < len(source) {
			if source[i] == ' ' || source[i] == '\t' || source[i] == '\r' || source[i] == '\n' || strings.ContainsRune("{}:;=(),|", rune(source[i])) || (source[i] == '/' && i+1 < len(source) && source[i+1] == '/') {
				break
			}
			i++
		}
		tokens = append(tokens, formatToken{text: string(source[start:i])})
	}
	return tokens
}

type formatFrame struct {
	kind       string
	assignment int
	entryCount int
}

func formatDeclaration(source []byte) string {
	tokens := lexFormat(source)
	var lines []string
	var line string
	var frames []formatFrame
	indent := 0
	flush := func() {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			lines = append(lines, strings.Repeat("  ", indent)+trimmed)
		}
		line = ""
	}
	word := func(value string) {
		if line != "" && !strings.HasSuffix(line, " ") && !strings.HasSuffix(line, "(") {
			line += " "
		}
		line += value
	}
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		if token.comment {
			flush()
			line = token.text
			flush()
			continue
		}
		value := token.text
		if len(frames) > 0 {
			frame := &frames[len(frames)-1]
			if (frame.kind == "track" || frame.kind == "fx" || frame.kind == "scene") && frame.assignment == 3 && value != "}" {
				flush()
				frame.assignment = 0
			}
			if frame.kind == "song" && frame.entryCount > 0 && value != "}" {
				flush()
				frame.entryCount = 0
			}
			if frame.kind == "pattern" && value == "use" && strings.TrimSpace(line) != "" {
				flush()
			}
		}
		switch value {
		case "{":
			kind := ""
			if parts := strings.Fields(line); len(parts) > 0 {
				kind = parts[0]
			}
			if i+1 < len(tokens) && tokens[i+1].text == "}" {
				line = strings.TrimRight(line, " ") + " {}"
				i++
				continue
			}
			line = strings.TrimRight(line, " ") + " {"
			flush()
			indent++
			frames = append(frames, formatFrame{kind: kind})
		case "}":
			flush()
			if indent > 0 {
				indent--
			}
			if len(frames) > 0 {
				frames = frames[:len(frames)-1]
			}
			line = "}"
			flush()
		case ";":
			line = strings.TrimRight(line, " ") + ";"
			flush()
		case "=":
			line = strings.TrimRight(line, " ") + " = "
			if len(frames) > 0 {
				frame := &frames[len(frames)-1]
				if frame.kind == "track" || frame.kind == "fx" || frame.kind == "scene" {
					frame.assignment = 2
				}
			}
		case ":":
			line = strings.TrimRight(line, " ") + ": "
		case "(":
			line = strings.TrimRight(line, " ") + "("
		case ")":
			line = strings.TrimRight(line, " ") + ")"
		case ",":
			line = strings.TrimRight(line, " ") + ", "
		case "|":
			line = strings.TrimRight(line, " ") + " | "
		default:
			word(value)
			if len(frames) > 0 {
				frame := &frames[len(frames)-1]
				if (frame.kind == "track" || frame.kind == "fx" || frame.kind == "scene") && frame.assignment == 2 {
					frame.assignment = 3
				}
				if frame.kind == "song" {
					frame.entryCount++
				}
			}
		}
	}
	flush()
	return strings.Join(lines, "\n")
}
