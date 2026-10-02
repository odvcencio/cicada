// Package projectcopy collects score, retained passes, history and recovery roots.
package projectcopy

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"m31labs.dev/cicada/edition"
	"m31labs.dev/cicada/host/takejournal"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// SaveAs preserves exact source bytes and project-relative references. Assets
// are copied and verified before the target score becomes visible. Existing
// dependencies must match; a collision never replaces unrelated audio.
func SaveAs(score, target string) error {
	score, err := filepath.Abs(score)
	if err != nil {
		return err
	}
	score, err = filepath.EvalSymlinks(score)
	if err != nil {
		return err
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return err
	}
	if score == target {
		return nil
	}
	srcDir, err := takejournal.ProjectRoot(score)
	if err != nil {
		return err
	}
	dstDir, err := takejournal.ProjectRoot(target)
	if err != nil {
		return err
	}
	srcName, err := filepath.Rel(srcDir, score)
	if err != nil {
		return err
	}
	dstName, err := filepath.Rel(dstDir, target)
	if err != nil {
		return err
	}
	sources, err := project.ReadSources(score, nil)
	if err != nil {
		return err
	}
	if len(sources.Libraries) > 0 {
		// Reject changed or unpinned imports before copying project dependencies.
		_, ds := sources.Parse()
		for _, d := range ds {
			if d.Severity == "error" {
				return &project.SourceError{Diagnostic: d}
			}
		}
	}
	additional := map[string][]byte{}
	if sources.Manifest.ExplicitSources() {
		for _, file := range sources.Files {
			if file.Path == score || file.Library != "" {
				continue
			}
			name, err := filepath.Rel(srcDir, file.Path)
			if err != nil {
				return err
			}
			additional[name] = file.Source
		}
		if !edition.ValidSourcePath(filepath.ToSlash(dstName)) {
			return errors.New("invalid multi-file Save As destination")
		}
	}
	src, err := os.OpenRoot(srcDir)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenRoot(dstDir)
	if err != nil {
		return err
	}
	defer dst.Close()
	// Save As creates a new score. Check early for a clear error, and use an
	// exclusive installation below to cover destinations created during copying.
	if _, err := dst.Lstat(dstName); err == nil {
		return fmt.Errorf("Save As refuses existing destination %s; choose a new score path", dstName)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	source, err := src.ReadFile(srcName)
	if err != nil {
		return err
	}
	assets := map[string]project.Asset{}
	origins := map[string]string{}
	collect := func(data []byte) error {
		s, _ := notation.ParseEdition(data, 2)
		if s == nil {
			return errors.New("cannot inspect score assets")
		}
		for _, a := range s.Assets {
			if !notation.ValidAssetPath(a.Path) {
				return errors.New("asset path escapes project")
			}
			v := project.Asset{Name: a.Name, Path: a.Path, SHA256: a.SHA256, Format: a.Format, Frames: a.Frames, RateHz: a.RateHz, Channels: a.Channels}
			if old, ok := assets[a.Path]; ok && old.SHA256 != v.SHA256 {
				return fmt.Errorf("history asset path collision: %s", a.Path)
			}
			assets[a.Path] = v
		}
		return nil
	}
	if err = collect(source); err != nil {
		return err
	}
	for _, data := range additional {
		if err = collect(data); err != nil {
			return err
		}
	}
	ss, err := takejournal.Snapshots(src, score)
	if err != nil {
		return err
	}
	for _, s := range ss {
		if s.Take.Asset.Path != "" {
			if old, ok := assets[s.Take.Asset.Path]; ok && old.SHA256 != s.Take.Asset.SHA256 {
				return fmt.Errorf("take asset path collision: %s", s.Take.Asset.Path)
			}
			assets[s.Take.Asset.Path] = s.Take.Asset
			if _, e := src.Stat(s.Take.Asset.Path); errors.Is(e, os.ErrNotExist) {
				origins[s.Take.Asset.Path] = takejournal.Namespace(score) + "/" + s.Take.ID + ".wav"
			}
		}
		if len(s.Take.Candidate) > 0 {
			if err = collect(s.Take.Candidate); err != nil {
				return err
			}
		}
	}
	// Studio preserves exact displaced source inodes and revision receipts.
	oldPrefix := ".cicada-studio-" + takejournal.Revision([]byte(score))[:16] + "-"
	newPrefix := ".cicada-studio-" + takejournal.Revision([]byte(target))[:16] + "-"
	d, err := src.Open(filepath.Dir(srcName))
	if err != nil {
		return err
	}
	names, err := d.Readdirnames(-1)
	d.Close()
	if err != nil {
		return err
	}
	histories := map[string][]byte{}
	for _, name := range names {
		if !strings.HasPrefix(name, oldPrefix) {
			continue
		}
		data, err := src.ReadFile(filepath.Join(filepath.Dir(srcName), name))
		if err != nil {
			return err
		}
		histories[filepath.Join(filepath.Dir(dstName), newPrefix+strings.TrimPrefix(name, oldPrefix))] = data
		if !strings.HasSuffix(name, ".revision") {
			if err = collect(data); err != nil {
				return err
			}
		}
	}
	// A destination may be absent now but scheduled for dependency installation.
	// Check the full copy set before writing any of it, including retained passes.
	dependencies := map[string]bool{"cicada.mod": true, "cicada.sum": true}
	for name := range sources.Libraries {
		dependencies[filepath.Join("lib", filepath.FromSlash(name), "cicada.mod")] = true
	}
	for path := range additional {
		dependencies[filepath.Clean(path)] = true
	}
	for path := range assets {
		dependencies[filepath.Clean(path)] = true
	}
	for path := range histories {
		dependencies[filepath.Clean(path)] = true
	}
	for _, snapshot := range ss {
		to := takejournal.Namespace(target) + "/" + snapshot.Take.ID
		dependencies[to+".pcm"], dependencies[to+".jsonl"] = true, true
		if snapshot.Take.Asset.Path != "" {
			dependencies[to+".wav"] = true
			dependencies["audio/blobs/"+snapshot.Take.Asset.SHA256+".wav"] = true
		}
	}
	if dependencies[filepath.Clean(dstName)] {
		return fmt.Errorf("Save As destination %s conflicts with a project dependency; choose a new score path", dstName)
	}
	for path, a := range assets {
		origin := path
		if v := origins[path]; v != "" {
			origin = v
		}
		if err = takejournal.Verify(src, origin, a); err != nil {
			return fmt.Errorf("Save As asset %s: %w", path, err)
		}
		f, err := src.Open(origin)
		if err != nil {
			return err
		}
		err = install(dst, path, f, -1, false)
		f.Close()
		if err != nil {
			return err
		}
		if err = takejournal.Verify(dst, path, a); err != nil {
			return err
		}
	}
	for _, s := range ss {
		from := takejournal.Namespace(score) + "/" + s.Take.ID
		to := takejournal.Namespace(target) + "/" + s.Take.ID
		// Copy raw acknowledged bytes even when the capture is still in progress.
		f, err := src.Open(from + ".pcm")
		if err != nil {
			return err
		}
		err = install(dst, to+".pcm", f, s.Bytes, false)
		f.Close()
		if err != nil {
			return err
		}
		if s.Take.Asset.Path != "" {
			origin := s.Take.Asset.Path
			if v := origins[origin]; v != "" {
				origin = v
			}
			f, err = src.Open(origin)
			if err != nil {
				return err
			}
			err = install(dst, to+".wav", f, -1, false)
			f.Close()
			if err != nil {
				return err
			}
			blob := "audio/blobs/" + s.Take.Asset.SHA256 + ".wav"
			f, err = src.Open(origin)
			if err != nil {
				return err
			}
			err = install(dst, blob, f, -1, false)
			f.Close()
			if err != nil {
				return err
			}
		}
		log, err := src.Open(from + ".jsonl")
		if err != nil {
			return err
		}
		err = install(dst, to+".jsonl", log, s.JournalBytes, false)
		log.Close()
		if err != nil {
			return err
		}
	}
	for path, data := range histories {
		if err = install(dst, path, bytes.NewReader(data), -1, false); err != nil {
			return err
		}
	}
	for name, data := range additional {
		latest, err := src.ReadFile(name)
		if err != nil {
			return err
		}
		if !bytes.Equal(latest, data) {
			return errors.New("project source changed during Save As; retry")
		}
		if err := install(dst, name, bytes.NewReader(data), -1, false); err != nil {
			return err
		}
	}
	manifest, err := src.ReadFile("cicada.mod")
	if err == nil {
		if sources.Manifest.ExplicitSources() {
			lines := strings.SplitAfter(string(manifest), "\n")
			for i, line := range lines {
				trimmed := strings.TrimSpace(line)
				parts := strings.Fields(trimmed)
				if len(parts) < 2 {
					continue
				}
				key := parts[0]
				value := strings.TrimSpace(trimmed[len(key):])
				if key != "entry" && key != "source" {
					continue
				}
				value = strings.TrimSpace(value)
				name, err := strconv.Unquote(value)
				if err == nil && name == filepath.ToSlash(srcName) {
					lines[i] = strings.Replace(line, value, strconv.Quote(filepath.ToSlash(dstName)), 1)
				}
			}
			manifest = []byte(strings.Join(lines, ""))
		}
		existing, e := dst.ReadFile("cicada.mod")
		if errors.Is(e, os.ErrNotExist) {
			err = install(dst, "cicada.mod", bytes.NewReader(manifest), -1, false)
		} else if e != nil {
			err = e
		} else if (len(assets) > 0 || sources.Manifest.ExplicitSources()) && !bytes.Equal(existing, manifest) {
			err = errors.New("target manifest differs; choose a new project folder")
		}
		if err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	latest, err := src.ReadFile(srcName)
	if err != nil {
		return err
	}
	if !bytes.Equal(source, latest) {
		return errors.New("score changed during Save As; retry")
	}
	if _, err := sources.VendorLibraries(dstDir); err != nil {
		return err
	}
	return install(dst, dstName, bytes.NewReader(source), -1, true)
}
func install(root *os.Root, path string, reader io.Reader, n int64, exclusive bool) error {
	if !notation.ValidAssetPath(path) {
		return fmt.Errorf("invalid destination path %s", path)
	}
	parent := filepath.Dir(path)
	if err := root.MkdirAll(parent, 0700); err != nil {
		return err
	}
	for p := parent; ; p = filepath.Dir(p) {
		if err := syncDir(root, p); err != nil {
			return err
		}
		if p == "." {
			break
		}
	}
	var id [8]byte
	if _, err := rand.Read(id[:]); err != nil {
		return err
	}
	stage := path + "." + hex.EncodeToString(id[:]) + ".tmp"
	f, err := root.OpenFile(stage, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(stage)
	if n < 0 {
		_, err = io.Copy(f, reader)
	} else {
		_, err = io.CopyN(f, reader, n)
	}
	if err == nil {
		err = f.Sync()
	}
	if err != nil {
		f.Close()
		return err
	}
	if exclusive {
		if err = f.Close(); err != nil {
			return err
		}
		// Link publishes the complete staged inode atomically and refuses any
		// destination that appeared after preflight. Never exchange a dependency.
		if err = root.Link(stage, path); err != nil {
			return fmt.Errorf("Save As cannot install new score %s: %w", path, err)
		}
		return syncDir(root, parent)
	}
	if old, e := root.Open(path); e == nil {
		defer old.Close()
		if _, err = f.Seek(0, io.SeekStart); err != nil {
			f.Close()
			return err
		}
		equal, err := equalFiles(old, f)
		f.Close()
		if err != nil {
			return err
		}
		if !equal {
			return fmt.Errorf("Save As refuses existing dependency %s with different bytes", path)
		}
		return nil
	} else if !errors.Is(e, os.ErrNotExist) {
		f.Close()
		return e
	}
	f.Close()
	if err = root.Link(stage, path); err != nil {
		return err
	}
	return syncDir(root, parent)
}
func syncDir(root *os.Root, path string) error {
	return takejournal.SyncDirectory(filepath.Join(root.Name(), path))
}
func equalFiles(a, b io.Reader) (bool, error) {
	x, y := make([]byte, 32768), make([]byte, 32768)
	for {
		nx, ex := io.ReadFull(a, x)
		ny, ey := io.ReadFull(b, y)
		if ex != nil && ex != io.EOF && ex != io.ErrUnexpectedEOF {
			return false, ex
		}
		if ey != nil && ey != io.EOF && ey != io.ErrUnexpectedEOF {
			return false, ey
		}
		if nx != ny || !bytes.Equal(x[:nx], y[:ny]) {
			return false, nil
		}
		if nx < len(x) {
			return true, nil
		}
	}
}
