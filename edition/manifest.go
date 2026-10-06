package edition

import (
	"bufio"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Manifest describes a project. Empty Entry and Sources retain independent
// single-file loading. Sources are explicit paths, never patterns.
type Manifest struct {
	Project string
	Edition int
	Entry   string
	Sources []string
	License string
	Author  string
	Lines   map[string]int // source path to manifest directive line
}

// ManifestError is a typed, positioned manifest diagnostic.
type ManifestError struct {
	Code    string
	Line    int
	Column  int
	Message string
}

func (e *ManifestError) Error() string {
	return fmt.Sprintf("%d:%d: %s: %s", e.Line, e.Column, e.Code, e.Message)
}

var spdxIdentifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+-]*$`)

// ValidSourcePath accepts portable, project-relative .cicada paths. Reject
// traversal components even when cleaning would keep the path inside the root.
func ValidSourcePath(value string) bool {
	if value == "" || strings.ContainsAny(value, `\:*?[]`) || strings.HasPrefix(value, "/") || path.Ext(value) != ".cicada" {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == ".." || part == "." || part == "" {
			return false
		}
	}
	return !strings.ContainsRune(value, 0)
}

func (m Manifest) ExplicitSources() bool { return m.Entry != "" || len(m.Sources) != 0 }

// SourcePaths returns the entry first and unique remaining paths sorted.
func (m Manifest) SourcePaths() []string {
	seen := map[string]bool{}
	var rest []string
	for _, source := range m.Sources {
		if source != m.Entry && !seen[source] {
			rest = append(rest, source)
			seen[source] = true
		}
	}
	sort.Strings(rest)
	if m.Entry != "" {
		return append([]string{m.Entry}, rest...)
	}
	return rest
}

func ParseProjectManifest(data []byte) (Manifest, error) {
	m := Manifest{Lines: map[string]int{}}
	seen := map[string]bool{}
	fail := func(line int, code, message string) (Manifest, error) {
		return Manifest{}, &ManifestError{Code: code, Line: line, Column: 1, Message: message}
	}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		at := strings.IndexFunc(text, unicode.IsSpace)
		key, value, found := text, "", at >= 0
		if found {
			key, value = text[:at], text[at:]
		}
		value = strings.TrimSpace(value)
		if !found || value == "" {
			return fail(line, "CICADA-MANIFEST", "expected directive and value")
		}
		if key != "source" && seen[key] {
			return fail(line, "CICADA-MANIFEST", "duplicate "+key+" directive")
		}
		seen[key] = true
		switch key {
		case "project":
			if !ValidProjectName(value) {
				return fail(line, "CICADA-MANIFEST", "invalid project name")
			}
			m.Project = value
		case "cicada":
			var err error
			m.Edition, err = strconv.Atoi(value)
			if err != nil || m.Edition < 1 {
				return fail(line, "CICADA-MANIFEST", "invalid cicada edition")
			}
		case "entry", "source", "license", "author":
			decoded, err := strconv.Unquote(value)
			if key == "license" && !strings.HasPrefix(value, `"`) {
				decoded, err = value, nil
			}
			if err != nil || !strings.HasPrefix(value, `"`) && key != "license" || decoded == "" {
				return fail(line, "CICADA-MANIFEST", key+" requires a quoted string")
			}
			switch key {
			case "entry", "source":
				if !ValidSourcePath(decoded) {
					return fail(line, "CICADA-SOURCE-PATH", "expected a project-relative .cicada path without traversal or patterns")
				}
				if key == "entry" {
					m.Entry = decoded
				} else {
					for _, old := range m.Sources {
						if old == decoded {
							return fail(line, "CICADA-MANIFEST", "duplicate source "+decoded)
						}
					}
					m.Sources = append(m.Sources, decoded)
				}
				m.Lines[decoded] = line
			case "license":
				if !spdxIdentifier.MatchString(decoded) {
					return fail(line, "CICADA-MANIFEST", "license requires one SPDX identifier")
				}
				m.License = decoded
			case "author":
				m.Author = decoded
			}
		default:
			return fail(line, "CICADA-MANIFEST", fmt.Sprintf("unknown directive %q", key))
		}
	}
	if err := scanner.Err(); err != nil {
		return fail(1, "CICADA-MANIFEST", err.Error())
	}
	if m.Project == "" || m.Edition == 0 {
		return fail(1, "CICADA-MANIFEST", "manifest requires project and cicada directives")
	}
	if m.Edition != 1 && m.Edition != 2 {
		return fail(1, "CICADA-VERSION", "only cicada 1 and 2 are supported")
	}
	if len(m.Sources) > 0 && m.Entry == "" {
		return fail(1, "CICADA-MANIFEST", "an explicit source list requires entry")
	}
	return m, nil
}
