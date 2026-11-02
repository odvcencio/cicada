package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"m31labs.dev/cicada/host/projectcopy"
	"m31labs.dev/cicada/host/takejournal"
	"m31labs.dev/cicada/internal/filetarget"
	"m31labs.dev/cicada/project"
)

type bundleFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

type bundleManifest struct {
	Format          string            `json:"format"`
	Entry           string            `json:"entry"`
	Libraries       []bundleLibrary   `json:"libraries"`
	Files           []bundleFile      `json:"files"`
	RecoverySources map[string]string `json:"recovery_sources,omitempty"`
}

type bundleLibrary struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	SHA256 string `json:"sha256"`
}

// bundleCommand uses the same checked dependency copying as Save As, then
// publishes the completed archive exclusively. The staging directory is never
// a checkout and is removed even if validation or publication fails.
func bundleCommand(args []string) error {
	if len(args) == 2 && args[0] == "--restore-history" {
		return restoreBundleHistory(args[1])
	}
	if len(args) != 2 || !strings.HasSuffix(strings.ToLower(args[1]), ".zip") {
		return fmt.Errorf("usage: cicada bundle <score.cicada> <bundle.zip>")
	}
	if _, err := loadProject(args[0]); err != nil {
		return err
	}
	target, err := filetarget.Create(args[1])
	if err != nil {
		return err
	}
	if _, err := os.Lstat(target.Physical); err == nil {
		return fmt.Errorf("bundle refuses existing destination %s", args[1])
	} else if !os.IsNotExist(err) {
		return err
	}
	stage, err := os.MkdirTemp("", "cicada-bundle-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	entry := filepath.Join(stage, "main.cicada")
	if err := projectcopy.SaveAs(args[0], entry); err != nil {
		return err
	}
	// Verify the staged score resolves without access to the original project.
	if _, err := loadProject(entry); err != nil {
		return fmt.Errorf("bundle staged score: %w", err)
	}
	sources, err := project.ReadSources(entry, nil)
	if err != nil {
		return err
	}
	libraries := make([]bundleLibrary, 0, len(sources.Libraries))
	for _, lib := range sources.Libraries {
		libraries = append(libraries, bundleLibrary{Path: lib.Path, Kind: lib.Kind, SHA256: lib.SHA256})
	}
	sort.Slice(libraries, func(i, j int) bool { return libraries[i].Path < libraries[j].Path })
	archive, err := os.CreateTemp(filepath.Dir(target.Physical), ".cicada-bundle-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(archive.Name())
	if err := writeBundle(archive, stage, libraries); err != nil {
		archive.Close()
		return err
	}
	if err := archive.Sync(); err != nil {
		archive.Close()
		return err
	}
	if err := archive.Close(); err != nil {
		return err
	}
	if err := os.Link(archive.Name(), target.Physical); err != nil {
		return fmt.Errorf("bundle cannot publish new destination: %w", err)
	}
	return nil
}

func writeBundle(output io.Writer, stage string, libraries []bundleLibrary) error {
	entry, err := filetarget.Resolve(filepath.Join(stage, "main.cicada"), filetarget.Existing)
	if err != nil {
		return err
	}
	sources, err := project.ReadSources(entry.Physical, nil)
	if err != nil {
		return err
	}
	type recoverySource struct{ id, path, portable string }
	recoveryPrefixes := map[string]recoverySource{}
	for _, file := range sources.Files {
		if file.Library != "" {
			continue
		}
		resolved, err := filetarget.Resolve(file.Path, filetarget.Existing)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(filepath.Dir(entry.Physical), resolved.Physical)
		if err != nil || !filepath.IsLocal(rel) {
			return fmt.Errorf("bundle recovery source is outside the project: %s", file.Path)
		}
		rel = filepath.ToSlash(rel)
		id := takejournal.Revision([]byte(rel))[:16]
		prefix := ".cicada-studio-" + takejournal.Revision([]byte(resolved.Physical))[:16] + "-"
		key := filepath.ToSlash(filepath.Join(filepath.Dir(rel), prefix))
		portable := filepath.ToSlash(filepath.Join(filepath.Dir(rel), ".cicada-bundle-history-"+id+"-"))
		recoveryPrefixes[key] = recoverySource{id: id, path: rel, portable: portable}
	}
	archive := zip.NewWriter(output)
	manifest := bundleManifest{Format: "cicada.bundle/1", Entry: "main.cicada", Libraries: libraries, Files: []bundleFile{}}
	written := map[string]bool{}
	write := func(name string, source io.Reader) error {
		if written[name] {
			return fmt.Errorf("bundle has duplicate file %s", name)
		}
		written[name] = true
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetModTime(time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC))
		header.SetMode(0644)
		writer, err := archive.CreateHeader(header)
		if err != nil {
			return err
		}
		_, err = io.Copy(writer, source)
		return err
	}
	err = filepath.WalkDir(stage, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(stage, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("bundle refuses non-regular file %s", rel)
		}
		name := filepath.ToSlash(rel)
		for prefix, source := range recoveryPrefixes {
			if strings.HasPrefix(name, prefix) && !strings.Contains(strings.TrimPrefix(name, prefix), "/") {
				if manifest.RecoverySources == nil {
					manifest.RecoverySources = map[string]string{}
				}
				if prior, exists := manifest.RecoverySources[source.id]; exists && prior != source.path {
					return fmt.Errorf("bundle recovery source identifier collision: %s", source.path)
				}
				manifest.RecoverySources[source.id] = source.path
				name = source.portable + strings.TrimPrefix(name, prefix)
				break
			}
		}
		if name == "cicada-bundle.json" {
			return fmt.Errorf("bundle manifest conflicts with project file %s", name)
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		hash := sha256.New()
		counter := &bundleCounter{reader: io.TeeReader(file, hash)}
		err = write(name, counter)
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		manifest.Files = append(manifest.Files, bundleFile{Path: name, SHA256: hex.EncodeToString(hash.Sum(nil)), Bytes: counter.count})
		return nil
	})
	if err != nil {
		archive.Close()
		return err
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		archive.Close()
		return err
	}
	if err := write("cicada-bundle.json", strings.NewReader(string(data)+"\n")); err != nil {
		archive.Close()
		return err
	}
	return archive.Close()
}

type bundleCounter struct {
	reader io.Reader
	count  int64
}

func (c *bundleCounter) Read(p []byte) (int, error) {
	n, err := c.reader.Read(p)
	c.count += int64(n)
	return n, err
}
