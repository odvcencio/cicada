// Package edition resolves project language editions and validates manifests.
package edition

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var projectName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

func ValidProjectName(name string) bool { return projectName.MatchString(name) }

// ScoreEdition finds the closest manifest. A loose score defaults to edition
// 1; an explicit source header is resolved by the notation parser.
func ScoreEdition(scorePath string) (int, string, error) {
	dir, err := filepath.Abs(filepath.Dir(scorePath))
	if err != nil {
		return 0, "", err
	}
	for {
		path := filepath.Join(dir, "cicada.mod")
		data, err := os.ReadFile(path)
		if err == nil {
			edition, parseErr := ParseManifest(data)
			if parseErr != nil {
				return 0, path, fmt.Errorf("%s: %w", path, parseErr)
			}
			return edition, path, nil
		}
		if !os.IsNotExist(err) {
			return 0, path, err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return 1, "", nil
		}
		dir = parent
	}
}

// ParseManifest preserves the edition-only API for existing callers.
func ParseManifest(data []byte) (int, error) {
	manifest, err := ParseProjectManifest(data)
	return manifest.Edition, err
}

// UpgradeManifestEdition rewrites an edition-1 manifest to edition 2 while
// preserving its project name, whitespace, comments, and line endings.
func UpgradeManifestEdition(source []byte) ([]byte, bool, error) {
	lines := strings.SplitAfter(string(source), "\n")
	found := false
	changed := false
	for i, line := range lines {
		ending := ""
		body := line
		if strings.HasSuffix(body, "\n") {
			ending, body = "\n", strings.TrimSuffix(body, "\n")
		}
		if strings.HasSuffix(body, "\r") {
			ending = "\r" + ending
			body = strings.TrimSuffix(body, "\r")
		}
		parts := strings.Fields(body)
		if len(parts) == 2 && parts[0] == "cicada" {
			if found {
				return nil, false, fmt.Errorf("duplicate cicada edition")
			}
			found = true
			if parts[1] == "2" {
				continue
			}
			if parts[1] != "1" {
				return nil, false, fmt.Errorf("CICADA-VERSION: only cicada 1 can be migrated")
			}
			at := strings.Index(body, parts[1])
			body = body[:at] + "2" + body[at+len(parts[1]):]
			lines[i] = body + ending
			changed = true
		}
	}
	if !found {
		return nil, false, fmt.Errorf("manifest has no cicada edition directive")
	}
	return []byte(strings.Join(lines, "")), changed, nil
}
