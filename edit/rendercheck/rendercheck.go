// Package rendercheck implements edit.RenderCheck by rendering a score to PCM24
// and hashing the bytes. It is impure by design (it renders audio), so the edit
// package's purity gate does not cover it.
package rendercheck

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/render"
)

type check struct {
	path      string
	overrides map[string][]byte
}

// New returns a render check for the entry score at path. Hash substitutes the
// supplied bytes for the entry file, so the project manifest and libraries
// apply as they do when the score is on disk. Callers cache with Cached.
func New(path string) edits.RenderCheck { return NewWith(path, nil) }

// NewWith is New with a snapshot of unsaved buffers (absolute path -> bytes)
// that replace project files, the manifest and cicada.sum while rendering.
func NewWith(path string, overrides map[string][]byte) edits.RenderCheck {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return check{path: path, overrides: overrides}
}

// layer returns base with staged files laid over it.
func layer(base, staged map[string][]byte) map[string][]byte {
	if len(staged) == 0 {
		return base
	}
	all := make(map[string][]byte, len(base)+len(staged))
	for name, data := range base {
		all[name] = data
	}
	for name, data := range staged {
		all[name] = data
	}
	return all
}

func merged(overrides map[string][]byte, path string, source []byte) map[string][]byte {
	all := make(map[string][]byte, len(overrides)+1)
	for name, data := range overrides {
		all[name] = data
	}
	all[path] = source
	return all
}

func (c check) Hash(source []byte) (string, error) { return c.HashWith(source, nil) }

// HashWith renders source with the batch's staged files laid over the
// buffers; staged files win.
func (c check) HashWith(source []byte, files map[string][]byte) (string, error) {
	score, ds, err := project.LoadScore(c.path, merged(layer(c.overrides, files), c.path, source))
	if err != nil {
		return "", err
	}
	for _, d := range ds {
		if d.Severity == "error" {
			return "", fmt.Errorf("%d:%d %s: %s", d.Position.Line, d.Position.Column, d.Code, d.Message)
		}
	}
	if score == nil {
		return "", fmt.Errorf("score did not load")
	}
	dir := filepath.Dir(c.path)
	dither := false
	sum := sha256.New()
	_, err = render.WAV(score, render.Options{SampleRate: 48000, Bits: 24, Dither: &dither, Block: 4096, AssetRoot: dir, AssetDir: dir}, sum)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

const cachedLimit = 32

type cachedResult struct {
	hash string
	err  error
}

type cached struct {
	inner edits.RenderCheck
	key   func(source []byte, files map[string][]byte) (string, bool)
	mu    sync.Mutex
	by    map[string]cachedResult
}

// Cached remembers results by the full render input identity of the score at
// path (see Identity, which also covers the files a batch has staged), so a long-lived host renders each distinct input set
// once and a change to a peer source, library, manifest or asset is never
// served from an older approval. When the inputs cannot be identified it does
// not cache. It keeps at most 32 results and starts over at the limit. Errors
// are cached too: the same inputs fail the same way.
func Cached(path string, inner edits.RenderCheck) edits.StagedRenderCheck {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return CachedBy(func(source []byte, files map[string][]byte) (string, bool) { return Identity(path, source, files) }, inner)
}

// CachedBy is Cached with a caller-supplied key. A key function that returns
// false disables caching for that call.
func CachedBy(key func(source []byte, files map[string][]byte) (string, bool), inner edits.RenderCheck) edits.StagedRenderCheck {
	return &cached{inner: inner, key: key}
}

func (c *cached) Hash(source []byte) (string, error) { return c.HashWith(source, nil) }

// HashWith keys on the staged files too, so a changed auxiliary file in a
// batch is never answered from an older render.
func (c *cached) HashWith(source []byte, files map[string][]byte) (string, error) {
	render := func() (string, error) {
		if staged, ok := c.inner.(edits.StagedRenderCheck); ok {
			return staged.HashWith(source, files)
		}
		return c.inner.Hash(source)
	}
	key, ok := c.key(source, files)
	if !ok {
		return render()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if result, ok := c.by[key]; ok {
		return result.hash, result.err
	}
	hash, err := render()
	if c.by == nil || len(c.by) >= cachedLimit {
		c.by = make(map[string]cachedResult)
	}
	c.by[key] = cachedResult{hash, err}
	return hash, err
}

const identityFileLimit = 5000

// Identity digests every input a render of source at path reads: all project
// source files with the entry replaced by source and any unsaved buffers in
// overrides (absolute path -> bytes) applied, the manifest and cicada.sum,
// library sources and their assets, each declared asset (path, size and
// modification time) and each sampler pack directory. The bool is false when
// the set cannot be enumerated reliably (unreadable manifest, missing asset,
// oversized pack tree); callers must then not cache. Content of assets and
// packs is pinned by hashes in the source, so size and modification time only
// guard against a file replaced behind a still-valid declaration.
func Identity(path string, source []byte, overrides map[string][]byte) (string, bool) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	sources, err := project.ReadSources(abs, merged(overrides, abs, source))
	if err != nil {
		return "", false
	}
	h := sha256.New()
	put := func(tag string, parts ...[]byte) {
		h.Write([]byte(tag))
		for _, part := range parts {
			fmt.Fprintf(h, ":%d:", len(part))
			h.Write(part)
		}
		h.Write([]byte{'\n'})
	}
	put("entry", []byte(abs))
	for _, file := range sources.Files {
		put("file", []byte(file.Path), []byte(file.Library), file.Source)
	}
	if sources.ManifestPath != "" {
		data, ok := overrides[sources.ManifestPath]
		if !ok {
			var err error
			if data, err = os.ReadFile(sources.ManifestPath); err != nil {
				return "", false
			}
		}
		put("manifest", []byte(sources.ManifestPath), data)
	}
	sumPath := filepath.Join(sources.Root, "cicada.sum")
	if sum, ok := overrides[sumPath]; ok {
		put("sum", sum)
	} else if sum, err := os.ReadFile(sumPath); err == nil {
		put("sum", sum)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", false
	}
	stat := func(tag, name string) bool {
		info, err := os.Stat(name)
		if err != nil {
			return false
		}
		put(tag, []byte(name), []byte(fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())))
		return true
	}
	libraries := append([]string(nil), sources.LibraryOrder...)
	sort.Strings(libraries)
	for _, name := range libraries {
		library := sources.Libraries[name]
		if library == nil {
			return "", false
		}
		put("library", []byte(name), []byte(fmt.Sprintf("%+v", library.LibraryPin)), []byte(library.Root), library.ManifestSource)
		for _, file := range library.Files {
			put("libfile", []byte(file.Path), file.Source)
		}
		for _, asset := range library.Assets {
			if !stat("libasset", filepath.Join(library.Root, filepath.FromSlash(asset))) {
				return "", false
			}
		}
	}
	var score *notation.Score
	if sources.ManifestPath == "" && len(sources.Libraries) == 0 {
		score, _ = notation.ParseSource(sources.Files[0])
	} else {
		editionNumber := sources.Manifest.Edition
		if editionNumber == 0 {
			editionNumber = notation.SourceEdition(sources.Files[0])
		}
		score, _ = notation.ParseFiles(sources.Files, editionNumber)
	}
	if score == nil {
		return "", false
	}
	for _, asset := range score.Assets {
		root := asset.Root
		if root == "" {
			root = sources.Root
		}
		if !notation.ValidAssetPath(asset.Path) || !stat("asset", filepath.Join(root, filepath.FromSlash(asset.Path))) {
			return "", false
		}
	}
	dir := filepath.Dir(abs)
	for _, sampler := range score.Samplers {
		if sampler.Pack == "" {
			continue
		}
		manifest := filepath.Join(dir, filepath.FromSlash(sampler.Pack))
		count := 0
		walk := func(name string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if count++; count > identityFileLimit {
				return errors.New("pack tree too large")
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			put("pack", []byte(name), []byte(fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())))
			return nil
		}
		if filepath.WalkDir(filepath.Dir(manifest), walk) != nil {
			return "", false
		}
	}
	return hex.EncodeToString(h.Sum(nil)), true
}
