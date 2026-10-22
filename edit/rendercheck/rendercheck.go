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
	target    string
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

func (c check) ForSource(path string, files map[string][]byte) (edits.RenderCheck, error) {
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute
	}
	return check{path: c.path, target: path, overrides: layer(c.overrides, files)}, nil
}

func (c *cached) ForSource(path string, files map[string][]byte) (edits.RenderCheck, error) {
	provider, ok := c.inner.(edits.SourceRenderCheck)
	if !ok {
		return nil, edits.ErrNoRenderCheck
	}
	return provider.ForSource(path, files)
}

func (c check) sourcePath() string {
	if c.target != "" {
		return c.target
	}
	return c.path
}

// Keep the governing project path separate from the buffer being replaced.
// A listed source can live below another manifest; that manifest must not
// change which project the split verification renders.
func (c check) sourceSnapshot(source []byte, files map[string][]byte) (*snapshot, bool) {
	overrides := merged(layer(c.overrides, files), c.sourcePath(), source)
	entry, ok := overrides[c.path]
	if !ok {
		var err error
		entry, err = os.ReadFile(c.path)
		if err != nil {
			return nil, false
		}
	}
	return take(c.path, entry, overrides)
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
	return c.renderFrom(merged(layer(c.overrides, files), c.sourcePath(), source))
}

// renderFrom renders the project that overrides describe.
func (c check) renderFrom(overrides map[string][]byte) (string, error) {
	return c.renderPinned(overrides, nil)
}

// renderPinned renders with the manifest resolution fixed when pin is set.
func (c check) renderPinned(overrides map[string][]byte, pin *project.Resolution) (string, error) {
	var (
		score *notation.Score
		ds    []notation.Diagnostic
		err   error
	)
	if pin != nil {
		score, ds, err = project.LoadScorePinned(c.path, overrides, *pin)
	} else {
		score, ds, err = project.LoadScore(c.path, overrides)
	}
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
	pairs map[string]bool
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
func Cached(path string, inner edits.RenderCheck) edits.SnapshotRenderCheck {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return CachedBy(func(source []byte, files map[string][]byte) (string, bool) { return Identity(path, source, files) }, inner)
}

// CachedBy is Cached with a caller-supplied key. A key function that returns
// false disables caching for that call.
func CachedBy(key func(source []byte, files map[string][]byte) (string, bool), inner edits.RenderCheck) edits.SnapshotRenderCheck {
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
	// The key names exactly the inputs this render reads. It is taken before
	// and after the render; a result is kept only if both are available and
	// equal, because otherwise the renderer may have read different inputs than
	// the key describes.
	key, ok := c.key(source, files)
	if !ok {
		return "", edits.ErrProjectChanged
	}
	c.mu.Lock()
	if result, hit := c.by[key]; hit {
		c.mu.Unlock()
		return result.hash, result.err
	}
	c.mu.Unlock()
	hash, err := render()
	if after, ok := c.key(source, files); !ok || after != key {
		return "", edits.ErrProjectChanged
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.by == nil || len(c.by) >= cachedLimit {
		c.by = make(map[string]cachedResult)
	}
	c.by[key] = cachedResult{hash, err}
	return hash, err
}

// PairIdentity names a comparison's inputs as they are now.
func (c *cached) PairIdentity(before, after []byte, files map[string][]byte) (string, bool) {
	if snap, ok := c.inner.(edits.SnapshotRenderCheck); ok {
		return snap.PairIdentity(before, after, files)
	}
	a, ok := c.key(before, files)
	if !ok {
		return "", false
	}
	b, ok := c.key(after, files)
	return a + "|" + b, ok
}

// Compare answers from the cache when the project's current pair identity was
// already rendered, and otherwise renders both candidates from one snapshot and
// remembers the result under that snapshot's identity, which is the identity of
// what was actually rendered.
func (c *cached) Compare(before, after []byte, files map[string][]byte) (bool, string, error) {
	snap, ok := c.inner.(edits.SnapshotRenderCheck)
	if !ok {
		a, err := c.HashWith(before, files)
		if err != nil {
			return false, "", err
		}
		b, err := c.HashWith(after, files)
		if err != nil {
			return false, "", err
		}
		identity, ok := c.PairIdentity(before, after, files)
		if !ok {
			return false, "", edits.ErrProjectChanged
		}
		return a == b, identity, nil
	}
	current, ok := snap.PairIdentity(before, after, files)
	if !ok {
		return false, "", edits.ErrProjectChanged
	}
	c.mu.Lock()
	if same, hit := c.pairs[current]; hit {
		c.mu.Unlock()
		return same, current, nil
	}
	c.mu.Unlock()
	same, identity, err := snap.Compare(before, after, files)
	if err != nil {
		return false, "", err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pairs == nil || len(c.pairs) >= cachedLimit {
		c.pairs = make(map[string]bool)
	}
	c.pairs[identity] = same
	return same, identity, nil
}

const identityFileLimit = 5000

// AfterSnapshot, when set, runs after a comparison has captured its snapshot
// and before it renders. Tests use it to change the project mid-render.
var AfterSnapshot func()

// AfterRender, when set, runs after a comparison's renders and before it
// restates assets. Tests use it to put a changed file back.
var AfterRender func()

// snapshot is one consistent capture of the source inputs of a render: every
// project source, the manifest and cicada.sum, and library sources, read once.
// Renders load them as overrides, so no source file is read from disk again.
type snapshot struct {
	files    map[string][]byte
	resolved project.Resolution // the manifest that governed the snapshot; renders do not rediscover it
	identity string
	// assets restats the declared assets and sampler packs, which still load
	// from disk. It returns false when they cannot be enumerated.
	assets  func() (string, bool)
	assetID string
}

// Identity digests every input a render of source at path reads (see take).
// The bool is false when the set cannot be enumerated reliably (unreadable
// manifest, missing asset, oversized pack tree); callers must then not cache.
func Identity(path string, source []byte, overrides map[string][]byte) (string, bool) {
	snap, ok := take(path, source, overrides)
	if !ok {
		return "", false
	}
	return snap.identity, true
}

// take reads the project once: all project source files with the entry
// replaced by source and any unsaved buffers in overrides (absolute path ->
// bytes) applied, the manifest and cicada.sum, and library sources. The
// identity hashes those bytes plus each declared asset and each sampler pack
// tree by path, size and modification time. Content of assets and packs is
// pinned by hashes in the source, so size and modification time only guard
// against a file replaced behind a still-valid declaration.
func take(path string, source []byte, overrides map[string][]byte) (*snapshot, bool) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, false
	}
	files := merged(overrides, abs, source)
	first, err := project.ReadSources(abs, files)
	if err != nil {
		return nil, false
	}
	// Capture the manifest and cicada.sum once, then read the sources again
	// with them as overrides so the parse and the snapshot agree.
	if first.ManifestPath != "" {
		if _, supplied := files[first.ManifestPath]; !supplied {
			data, err := os.ReadFile(first.ManifestPath)
			if err != nil {
				return nil, false
			}
			files[first.ManifestPath] = data
		}
	}
	sumPath := filepath.Join(first.Root, "cicada.sum")
	if _, supplied := files[sumPath]; !supplied {
		if sum, err := os.ReadFile(sumPath); err == nil {
			files[sumPath] = sum
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, false
		}
	}
	resolved := project.Resolution{ManifestPath: first.ManifestPath}
	sources, err := project.ReadSourcesPinned(abs, files, resolved)
	if err != nil {
		return nil, false
	}
	for _, file := range sources.Files {
		files[file.Path] = file.Source
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
	// The resolved project: its manifest (or none), root and source graph.
	put("resolution", []byte(sources.ManifestPath), []byte(sources.Root))
	for _, file := range sources.Files {
		put("graph", []byte(file.Path))
	}
	for _, file := range sources.Files {
		put("file", []byte(file.Path), []byte(file.Library), file.Source)
	}
	if sources.ManifestPath != "" {
		put("manifest", []byte(sources.ManifestPath), files[sources.ManifestPath])
	}
	if sum, ok := files[sumPath]; ok {
		put("sum", sum)
	}
	libraries := append([]string(nil), sources.LibraryOrder...)
	sort.Strings(libraries)
	var statTargets []string
	for _, name := range libraries {
		library := sources.Libraries[name]
		if library == nil {
			return nil, false
		}
		put("library", []byte(name), []byte(fmt.Sprintf("%+v", library.LibraryPin)), []byte(library.Root), library.ManifestSource)
		for _, file := range library.Files {
			put("libfile", []byte(file.Path), file.Source)
			files[file.Path] = file.Source
		}
		for _, asset := range library.Assets {
			statTargets = append(statTargets, filepath.Join(library.Root, filepath.FromSlash(asset)))
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
		return nil, false
	}
	for _, asset := range score.Assets {
		root := asset.Root
		if root == "" {
			root = sources.Root
		}
		if !notation.ValidAssetPath(asset.Path) {
			return nil, false
		}
		statTargets = append(statTargets, filepath.Join(root, filepath.FromSlash(asset.Path)))
	}
	var packDirs []string
	dir := filepath.Dir(abs)
	for _, sampler := range score.Samplers {
		if sampler.Pack != "" {
			packDirs = append(packDirs, filepath.Dir(filepath.Join(dir, filepath.FromSlash(sampler.Pack))))
		}
	}
	// Assets and packs still load from disk, so they are restated after a render.
	assets := func() (string, bool) {
		d := sha256.New()
		for _, name := range statTargets {
			info, err := os.Stat(name)
			if err != nil {
				return "", false
			}
			fmt.Fprintf(d, "asset:%s:%d:%d\n", name, info.Size(), info.ModTime().UnixNano())
		}
		for _, root := range packDirs {
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
				fmt.Fprintf(d, "pack:%s:%d:%d\n", name, info.Size(), info.ModTime().UnixNano())
				return nil
			}
			if filepath.WalkDir(root, walk) != nil {
				return "", false
			}
		}
		return hex.EncodeToString(d.Sum(nil)), true
	}
	assetID, ok := assets()
	if !ok {
		return nil, false
	}
	put("assets", []byte(assetID))
	return &snapshot{files: files, resolved: resolved, identity: hex.EncodeToString(h.Sum(nil)), assets: assets, assetID: assetID}, true
}

func hashOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// pairIdentity names the project and both candidate sources.
func pairIdentity(snap *snapshot, target string, after []byte) string {
	return snap.identity + "|" + hashOf([]byte(target)) + "|" + hashOf(after)
}

// PairIdentity names the inputs of a comparison of before and after as the
// project is now. A comparison's recorded identity is compared with it before
// anything is written.
func (c check) PairIdentity(before, after []byte, files map[string][]byte) (string, bool) {
	snap, ok := c.sourceSnapshot(before, files)
	if !ok {
		return "", false
	}
	return pairIdentity(snap, c.sourcePath(), after), true
}

// Compare renders both candidates from one snapshot of the project, so a file
// changed (or changed and restored) while rendering cannot make the two renders
// read different inputs. It returns whether they match and the snapshot's pair
// identity. Assets and packs load from disk and are restated afterwards.
func (c check) Compare(before, after []byte, files map[string][]byte) (bool, string, error) {
	snap, ok := c.sourceSnapshot(before, files)
	if !ok {
		return false, "", edits.ErrProjectChanged
	}
	if AfterSnapshot != nil {
		AfterSnapshot()
	}
	render := func(entry []byte) (string, error) {
		return c.renderPinned(merged(snap.files, c.sourcePath(), entry), &snap.resolved)
	}
	a, err := render(before)
	if err != nil {
		return false, "", err
	}
	b, err := render(after)
	if err != nil {
		return false, "", err
	}
	if AfterRender != nil {
		AfterRender()
	}
	if now, ok := snap.assets(); !ok || now != snap.assetID {
		return false, "", edits.ErrProjectChanged
	}
	return a == b, pairIdentity(snap, c.sourcePath(), after), nil
}
