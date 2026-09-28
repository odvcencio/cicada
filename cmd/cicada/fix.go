package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	ed "m31labs.dev/cicada/edition"
	"m31labs.dev/cicada/migration"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

type fixScoreEdit struct {
	path    string
	before  []byte
	fixed   []byte
	changed bool
	mode    os.FileMode
}

// fixCommand migrates edition-1 spellings wherever they appear, including in
// a project whose manifest already selects edition 2.
func fixCommand(args []string) error {
	return fixCommandWithWriter(args, writeFixedScore)
}

func fixCommandWithWriter(args []string, writeScore func(string, []byte, os.FileMode) error) error {
	path, all, check, err := parseFixArgs(args)
	if err != nil {
		return err
	}
	root, currentEdition, manifest, err := fixProjectLocation(path)
	if err != nil {
		return err
	}
	paths, err := projectScorePaths(root)
	if err != nil {
		return err
	}
	if len(paths) == 0 && path == "" {
		return fmt.Errorf("no .cicada scores found in %s", root)
	}
	if path != "" {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		path = filepath.Clean(absolute)
		found := false
		for _, candidate := range paths {
			if filepath.Clean(candidate) == path {
				found = true
				break
			}
		}
		if !found {
			paths = append(paths, path)
			sort.Strings(paths)
		}
	}

	candidates := []string{}
	if path != "" {
		candidates = append(candidates, path)
	}
	if all {
		for _, candidate := range paths {
			if candidate == path {
				continue
			}
			legacy, err := isEditionOneScore(candidate, currentEdition, manifest != "")
			if err != nil {
				return err
			}
			if legacy {
				candidates = append(candidates, candidate)
			}
		}
	}
	if len(candidates) == 0 {
		return fmt.Errorf("no edition-1 scores found in %s", root)
	}
	sort.Strings(candidates)

	edits := make([]fixScoreEdit, 0, len(candidates))
	for _, candidate := range candidates {
		before, err := os.ReadFile(candidate)
		if err != nil {
			return err
		}
		fixed, changed, fixErr := fixSourceForProject(before, currentEdition)
		if fixErr != nil {
			return fmt.Errorf("%s: %w", candidate, fixErr)
		}
		if err := validateSourceEdition(fixed, 2); err != nil {
			return fmt.Errorf("%s: migrated score does not validate as edition 2: %w", candidate, err)
		}
		mode := os.FileMode(0)
		if changed {
			info, err := os.Stat(candidate)
			if err != nil {
				return err
			}
			mode = info.Mode().Perm()
		}
		edits = append(edits, fixScoreEdit{path: candidate, before: before, fixed: fixed, changed: changed, mode: mode})
	}

	newManifest := manifest == "" && (hasEditionOneEdit(edits, currentEdition) || all && hasLooseEditionOneScore(edits))
	manifestUpgrade := manifest != "" && currentEdition == 1
	if newManifest {
		manifest = filepath.Join(root, "cicada.mod")
	}
	if manifestUpgrade {
		manifestSource, err := os.ReadFile(manifest)
		if err != nil {
			return err
		}
		if _, _, err := ed.UpgradeManifestEdition(manifestSource); err != nil {
			return fmt.Errorf("%s: %w", manifest, err)
		}
	}

	needsWrite := newManifest || manifestUpgrade
	changedScores := 0
	for _, edit := range edits {
		if edit.changed {
			needsWrite = true
			changedScores++
		}
	}
	if !needsWrite {
		if path != "" {
			fmt.Println("already fixed:", path)
		} else {
			fmt.Println("already fixed:", root)
		}
		return nil
	}

	if !all {
		var siblings []string
		for _, candidate := range paths {
			if candidate == path {
				continue
			}
			legacy, err := isEditionOneScore(candidate, currentEdition, manifest != "" && !newManifest)
			if err != nil {
				return err
			}
			if legacy {
				relative, err := filepath.Rel(root, candidate)
				if err != nil {
					relative = candidate
				}
				siblings = append(siblings, relative)
			}
		}
		if len(siblings) != 0 {
			sort.Strings(siblings)
			return fmt.Errorf("refusing to fix %s while other edition-1 scores share %s: %s; run `cicada fix --all` to migrate them together", path, root, strings.Join(siblings, ", "))
		}
	}

	if check {
		return fmt.Errorf("fix needed: %s (score changes: %d, manifest needed: %t, manifest edition upgrade needed: %t)", root, changedScores, newManifest, manifestUpgrade)
	}

	var originalManifest []byte
	manifestMode := os.FileMode(0644)
	if manifestUpgrade {
		originalManifest, err = os.ReadFile(manifest)
		if err != nil {
			return err
		}
		if info, err := os.Stat(manifest); err == nil {
			manifestMode = info.Mode().Perm()
		} else {
			return err
		}
	}
	var manifestSource []byte
	if newManifest {
		name := filepath.Base(filepath.Clean(root))
		if path != "" {
			name = strings.TrimSuffix(filepath.Base(path), ".cicada")
		}
		if !ed.ValidProjectName(name) {
			return fmt.Errorf("cannot derive project name from %q", root)
		}
		manifestSource = []byte("project " + name + "\ncicada 2\n")
	} else if manifestUpgrade {
		manifestSource, _, err = ed.UpgradeManifestEdition(originalManifest)
		if err != nil {
			return fmt.Errorf("%s: %w", manifest, err)
		}
	}

	var written []fixScoreEdit
	rollback := func() {
		for index := len(written) - 1; index >= 0; index-- {
			edit := written[index]
			_ = writeFixedScore(edit.path, edit.before, edit.mode)
		}
	}
	for _, edit := range edits {
		if !edit.changed {
			continue
		}
		if err := writeScore(edit.path, edit.fixed, edit.mode); err != nil {
			rollback()
			return fmt.Errorf("%s: %w", edit.path, err)
		}
		written = append(written, edit)
	}
	if newManifest || manifestUpgrade {
		if err := writeFixedScore(manifest, manifestSource, manifestMode); err != nil {
			rollback()
			return fmt.Errorf("%s: %w", manifest, err)
		}
	}

	if changedScores == 1 && path != "" {
		fmt.Printf("fixed %s; edition manifest %s\n", path, manifest)
	} else {
		fmt.Printf("fixed %d score(s); edition manifest %s\n", changedScores, manifest)
	}
	return nil
}

func parseFixArgs(args []string) (path string, all, check bool, err error) {
	for _, arg := range args {
		switch arg {
		case "--all":
			all = true
		case "--check":
			check = true
		default:
			if strings.HasPrefix(arg, "-") || path != "" {
				return "", false, false, fmt.Errorf("usage: cicada fix [<score.cicada>] [--all] [--check]")
			}
			path = arg
		}
	}
	if path == "" && !all || path != "" && filepath.Ext(path) != ".cicada" {
		return "", false, false, fmt.Errorf("usage: cicada fix <score.cicada> [--all] [--check] | cicada fix --all [--check]")
	}
	return path, all, check, nil
}

func fixProjectLocation(path string) (root string, edition int, manifest string, err error) {
	if path != "" {
		edition, manifest, err = scoreEdition(path)
		if err != nil {
			return "", 0, "", err
		}
		if manifest != "" {
			return filepath.Dir(manifest), edition, manifest, nil
		}
		absolute, err := filepath.Abs(filepath.Dir(path))
		if err != nil {
			return "", 0, "", err
		}
		root = absolute
		source, err := os.ReadFile(path)
		if err != nil {
			return "", 0, "", err
		}
		if sourceEditionHeader(source) == 2 {
			edition = 2
		}
		return root, edition, "", nil
	}
	root, err = projectRoot()
	if err != nil {
		return "", 0, "", err
	}
	edition, manifest, err = scoreEdition(filepath.Join(root, "main.cicada"))
	if err == nil && manifest == "" {
		if source, readErr := os.ReadFile(filepath.Join(root, "main.cicada")); readErr == nil && sourceEditionHeader(source) == 2 {
			edition = 2
		}
	}
	return root, edition, manifest, err
}

func sourceEditionHeader(source []byte) int {
	for _, line := range strings.Split(string(source), "\n") {
		text := strings.TrimSpace(strings.TrimPrefix(line, "\ufeff"))
		if text == "" || strings.HasPrefix(text, "//") {
			continue
		}
		fields := strings.Fields(text)
		if len(fields) == 2 && fields[0] == "cicada" {
			edition, _ := strconv.Atoi(fields[1])
			return edition
		}
		return 0
	}
	return 0
}

func isEditionOneScore(path string, projectEdition int, hasManifest bool) (bool, error) {
	source, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	if hasManifest && projectEdition == 1 || !hasManifest && sourceEditionHeader(source) != 2 {
		return true, nil
	}
	if sourceEditionHeader(source) == 1 {
		return true, nil
	}
	_, changed, err := fixSourceForProject(source, projectEdition)
	return err == nil && changed, nil
}

func hasEditionOneEdit(edits []fixScoreEdit, projectEdition int) bool {
	if projectEdition == 1 {
		return len(edits) != 0
	}
	for _, edit := range edits {
		if edit.changed || sourceEditionHeader(edit.before) == 1 {
			return true
		}
	}
	return false
}

func hasLooseEditionOneScore(edits []fixScoreEdit) bool {
	for _, edit := range edits {
		if sourceEditionHeader(edit.before) != 2 {
			return true
		}
	}
	return false
}

func validateSourceEdition(source []byte, edition int) error {
	score, diagnostics := notation.ParseEdition(source, edition)
	if hasDiagnosticErrors(diagnostics) {
		return fmt.Errorf("%+v", diagnostics)
	}
	compiled, projectDiagnostics := project.FromScore(score)
	if compiled == nil || hasDiagnosticErrors(projectDiagnostics) {
		return fmt.Errorf("%+v", projectDiagnostics)
	}
	if _, err := project.CompileEngine(compiled, 48_000, 128); err != nil {
		return err
	}
	return nil
}

func fixSourceForProject(source []byte, projectEdition int) ([]byte, bool, error) {
	fixed, changed, err := fixSource(source)
	if err == nil {
		return fixed, changed, nil
	}
	if projectEdition != 2 || sourceEditionHeader(source) != 2 {
		return nil, false, err
	}
	if validateSourceEdition(source, 2) == nil {
		return source, false, nil
	}
	withoutHeader, removed := removeEditionHeader(source, 2)
	if !removed {
		return nil, false, err
	}
	fixed, changed, rewriteErr := fixSource(withoutHeader)
	if rewriteErr != nil || !changed {
		if rewriteErr != nil {
			return nil, false, rewriteErr
		}
		return nil, false, err
	}
	return fixed, true, nil
}

func removeEditionHeader(source []byte, edition int) ([]byte, bool) {
	lines := strings.SplitAfter(string(source), "\n")
	offset := 0
	for index, line := range lines {
		body := strings.TrimSuffix(line, "\n")
		body = strings.TrimSuffix(body, "\r")
		text := strings.TrimSpace(body)
		if text == "" || strings.HasPrefix(text, "//") {
			offset += len(line)
			continue
		}
		if text != fmt.Sprintf("cicada %d", edition) {
			return source, false
		}
		end := offset + len(line)
		if index+1 < len(lines) {
			next := strings.TrimSuffix(lines[index+1], "\n")
			next = strings.TrimSuffix(next, "\r")
			if strings.TrimSpace(next) == "" {
				end += len(lines[index+1])
			}
		}
		result := make([]byte, 0, len(source)-(end-offset))
		result = append(result, source[:offset]...)
		result = append(result, source[end:]...)
		return result, true
	}
	return source, false
}

func writeFixedScore(path string, source []byte, mode os.FileMode) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".cicada-fix-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(mode); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(source); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func fixSource(source []byte) ([]byte, bool, error) {
	return migration.FixSource(source)
}
