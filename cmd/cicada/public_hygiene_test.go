package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

var planningDocumentName = regexp.MustCompile(`(?i)(^|[-_. /])(roadmap|plans?|planning|designs?|research|status|reports?|evidence|audits?|handoffs?|proposals?|moonshot|sota|future|later|accepted)([-_. /0-9]|$)`)
var documentNameBoundary = regexp.MustCompile(`([a-z0-9])([A-Z])`)
var planningHeading = regexp.MustCompile(`(?i)\b(roadmap|coming\s+next|what\s+comes\s+next|planning|research|audits?|handoffs?|proposals?|moonshot|sota)\b|^(plans?|designs?|status|reports?|future|later)(\s|$)`)
var setextHeading = regexp.MustCompile(`^\s*(?:={3,}|-{3,})\s*$`)
var publicTextRules = []struct {
	name    string
	pattern *regexp.Regexp
}{
	{"recording anecdote", regexp.MustCompile(`(?i)pen` + `cil`)},
	{"internal process wording", regexp.MustCompile(`(?i)roadmap\s+lanes?|owner[- ](?:accepted|approved)|owner\s+listening|listening\s+acceptance|\btranche\b|\blane\s+[a-e](?:['’]s|\s+(?:owns|implements|provides))`)},
	{"unimplemented example fence", regexp.MustCompile("(?im)^\\s*(?:`{3,}|~{3,})\\s*cicada-" + "accepted\\b")},
	{"attribution text", regexp.MustCompile(`(?i)co[-]authored[-]by|generated\s+with|noreply@anthropic[.]com`)},
	{"private home path", regexp.MustCompile(`(?i)(?:/home/|/users/)[a-z0-9_.-]+/`)},
	{"machine-specific name", regexp.MustCompile(`(?i)\b(?:build` + `box|chi-[0-9]+)\b`)},
}

func publicDocument(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".txt", ".rst", ".adoc", ".pdf", ".ebnf":
		return true
	case ".json", ".jsonl", ".csv", ".log":
		return strings.HasPrefix(filepath.ToSlash(path), "docs/")
	}
	return false
}

func publicSource(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go", ".js", ".cjs", ".mjs", ".ts", ".tsx", ".html", ".css", ".py", ".sh", ".scm", ".cicada", ".yml", ".yaml", ".gsx", ".fw", ".c", ".h", ".cpp", ".rs":
		return true
	}
	return filepath.Base(path) == "Makefile"
}

func publicHygieneViolations(path string, data []byte) []string {
	var violations []string
	path = filepath.ToSlash(path)
	doc := publicDocument(path)
	if publicTextRules[0].pattern.MatchString(path) {
		violations = append(violations, "recording anecdote path")
	}
	if strings.HasPrefix(path, "docs/design/") || strings.Contains(path, "/evidence/") || doc && planningDocumentName.MatchString(documentNameBoundary.ReplaceAllString(path, "${1}-${2}")) {
		violations = append(violations, "planning document path")
	}
	switch path {
	case "docs/audio/piano-abx.md", "docs/sampler/quality.md", "docs/sampler/full-kit-quality.md", "experimental/expressive/web/browser-check.txt":
		violations = append(violations, "persisted validation document")
	}
	if !(doc || publicSource(path)) || !utf8.Valid(data) {
		return violations
	}
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		for _, rule := range publicTextRules {
			if rule.pattern.MatchString(line) {
				violations = append(violations, fmt.Sprintf("line %d: %s", i+1, rule.name))
			}
		}
		if doc {
			heading := strings.TrimSpace(line)
			if strings.HasPrefix(heading, "#") {
				heading = strings.TrimSpace(strings.TrimLeft(heading, "#"))
			} else if i+1 < len(lines) && setextHeading.MatchString(lines[i+1]) {
				// Setext headings use the previous line as their title.
			} else {
				continue
			}
			if planningHeading.MatchString(heading) {
				violations = append(violations, fmt.Sprintf("line %d: planning heading", i+1))
			}
		}
	}
	return violations
}

func TestDocumentationHygiene(t *testing.T) {
	root := repositoryRoot()
	if _, err := os.Lstat(filepath.Join(root, "docs", "design")); err == nil {
		t.Error("remove the planning document directory")
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	command := exec.Command("git", "rev-parse", "--git-path", "index")
	command.Dir = root
	index, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	indexPath := strings.TrimSpace(string(index))
	if !filepath.IsAbs(indexPath) {
		indexPath = filepath.Join(root, indexPath)
	}
	// Register the index and directory listings as Go test cache inputs so new
	// tracked files invalidate a prior passing result, including in worktrees.
	// A worktree index can be outside the module; changing directory also
	// registers its directory metadata, which Git updates when replacing it.
	t.Chdir(filepath.Dir(indexPath))
	if _, err := os.ReadFile(indexPath); err != nil {
		t.Fatal(err)
	}
	command = exec.Command("git", "ls-files", "-z")
	command.Dir = root
	files, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	directories := make(map[string]bool)
	for _, path := range strings.Split(string(files), "\x00") {
		if path == "" {
			continue
		}
		for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
			if !directories[dir] {
				if _, err := os.ReadDir(filepath.Join(root, dir)); err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
				directories[dir] = true
			}
			if dir == "." {
				break
			}
		}
		data, err := os.ReadFile(filepath.Join(root, path))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, violation := range publicHygieneViolations(path, data) {
			t.Errorf("%s: %s", path, violation)
		}
	}
}
