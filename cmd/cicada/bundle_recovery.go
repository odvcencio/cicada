package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"m31labs.dev/cicada/host/takejournal"
	"m31labs.dev/cicada/internal/filetarget"
)

var bundleRecoveryName = regexp.MustCompile(`^\.cicada-(?:studio-([0-9a-f]{16})-|bundle-history-)([A-Za-z0-9._-]+)$`)

// restoreBundleHistory copies inventoried recovery versions into the namespace
// Studio uses for this extracted score. The archive originals remain intact.
// A partial restore is safe to retry; existing files are never overwritten.
func restoreBundleHistory(score string) error {
	target, err := filetarget.Resolve(score, filetarget.Existing)
	if err != nil {
		return err
	}
	projectDir, err := takejournal.ProjectRoot(target.Physical)
	if err != nil {
		return err
	}
	rootTarget, err := filetarget.Resolve(projectDir, filetarget.Existing)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(rootTarget.Physical)
	if err != nil {
		return err
	}
	defer root.Close()
	manifestBytes, err := root.ReadFile("cicada-bundle.json")
	if err != nil {
		return fmt.Errorf("read bundle inventory: %w", err)
	}
	var manifest bundleManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return fmt.Errorf("invalid bundle inventory: %w", err)
	}
	entryRel, err := filepath.Rel(rootTarget.Physical, target.Physical)
	if err != nil || !filepath.IsLocal(entryRel) || manifest.Format != "cicada.bundle/1" || manifest.Entry != filepath.ToSlash(entryRel) {
		return errors.New("bundle inventory does not identify this score")
	}
	type destination struct{ dir, prefix string }
	destinations := map[string]destination{}
	for id, source := range manifest.RecoverySources {
		if !filepath.IsLocal(source) || filepath.ToSlash(filepath.Clean(source)) != source || strings.Contains(source, `\`) || takejournal.Revision([]byte(source))[:16] != id {
			return fmt.Errorf("invalid bundle recovery source: %s", source)
		}
		info, err := root.Lstat(filepath.FromSlash(source))
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("bundle recovery score is missing or not regular: %s", source)
		}
		resolved, err := filetarget.Resolve(filepath.Join(rootTarget.Physical, filepath.FromSlash(source)), filetarget.Existing)
		if err != nil {
			return err
		}
		physicalRel, err := filepath.Rel(rootTarget.Physical, resolved.Physical)
		if err != nil || !filepath.IsLocal(physicalRel) {
			return fmt.Errorf("bundle recovery score escapes project: %s", source)
		}
		destinations[id] = destination{dir: path.Dir(source), prefix: ".cicada-studio-" + takejournal.Revision([]byte(resolved.Physical))[:16] + "-"}
	}
	entryDestination := destination{dir: path.Dir(manifest.Entry), prefix: ".cicada-studio-" + takejournal.Revision([]byte(target.Physical))[:16] + "-"}
	files := map[string][]byte{}
	seen := map[string]bool{}
	legacyNamespaces := map[string]string{}
	for _, item := range manifest.Files {
		dir, base := path.Split(item.Path)
		dir = strings.TrimSuffix(dir, "/")
		if dir == "" {
			dir = "."
		}
		match := bundleRecoveryName.FindStringSubmatch(base)
		if match == nil {
			continue
		}
		if seen[item.Path] {
			return fmt.Errorf("duplicate recovery file in bundle inventory: %s", item.Path)
		}
		seen[item.Path] = true
		dest, suffix := entryDestination, match[2]
		if match[1] == "" && len(manifest.RecoverySources) != 0 {
			if len(suffix) < 18 || suffix[16] != '-' {
				return fmt.Errorf("invalid portable recovery filename: %s", item.Path)
			}
			id := suffix[:16]
			var ok bool
			dest, ok = destinations[id]
			if !ok || dest.dir != dir {
				return fmt.Errorf("unknown bundle recovery source: %s", item.Path)
			}
			suffix = suffix[17:]
		} else {
			if dir != entryDestination.dir {
				return fmt.Errorf("legacy recovery file is outside the entry directory: %s", item.Path)
			}
			current := match[1]
			if current == "" {
				current = "portable"
			}
			if prior := legacyNamespaces[dir]; prior != "" && prior != current {
				return errors.New("bundle inventory has multiple recovery namespaces")
			}
			legacyNamespaces[dir] = current
		}
		info, err := root.Lstat(item.Path)
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("bundle recovery file is missing or not regular: %s", item.Path)
		}
		data, err := root.ReadFile(item.Path)
		if err != nil {
			return err
		}
		hash := sha256.Sum256(data)
		if int64(len(data)) != item.Bytes || hex.EncodeToString(hash[:]) != item.SHA256 {
			return fmt.Errorf("bundle recovery file does not match its inventory: %s", item.Path)
		}
		name := filepath.FromSlash(path.Join(dest.dir, dest.prefix+suffix))
		if _, exists := files[name]; exists {
			return fmt.Errorf("duplicate restored recovery file: %s", name)
		}
		files[name] = data
	}
	for name, data := range files {
		info, err := root.Lstat(name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("restore refuses existing recovery destination: %s", name)
		}
		current, err := root.ReadFile(name)
		if err != nil || !bytes.Equal(current, data) {
			return fmt.Errorf("restore refuses different recovery destination: %s", name)
		}
	}
	for name, data := range files {
		file, err := os.CreateTemp(rootTarget.Physical, ".cicada-bundle-restore-*")
		if err != nil {
			return err
		}
		temp := filepath.Base(file.Name())
		_, writeErr := io.Copy(file, bytes.NewReader(data))
		if writeErr == nil {
			writeErr = file.Sync()
		}
		if closeErr := file.Close(); writeErr == nil {
			writeErr = closeErr
		}
		if writeErr != nil {
			root.Remove(temp)
			return fmt.Errorf("restore %s failed; original bundle files remain: %w", name, writeErr)
		}
		err = root.Link(temp, name)
		removeErr := root.Remove(temp)
		if errors.Is(err, os.ErrExist) {
			// Recheck in case a different writer won the race after preflight.
			info, statErr := root.Lstat(name)
			if statErr == nil && info.Mode().IsRegular() {
				current, readErr := root.ReadFile(name)
				if readErr == nil && bytes.Equal(current, data) && removeErr == nil {
					continue
				}
			}
			return fmt.Errorf("restore refuses existing recovery destination: %s", name)
		}
		if err != nil {
			return err
		}
		if removeErr != nil {
			return fmt.Errorf("restored %s but could not remove staging file %s: %w", name, temp, removeErr)
		}
	}
	if len(files) != 0 {
		parents := map[string]bool{}
		for name := range files {
			parents[filepath.Dir(name)] = true
		}
		for parent := range parents {
			if err := takejournal.SyncDirectory(filepath.Join(rootTarget.Physical, parent)); err != nil {
				return err
			}
		}
	}
	fmt.Printf("Restored %d recovery files for %s. Bundle originals remain in place.\n", len(files), score)
	return nil
}
