package project

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"m31labs.dev/cicada/edition"
	"m31labs.dev/cicada/notation"
)

// The standard namespace is embedded in the host, never in the audio kernel.
//
//go:embed std
var standardLibraries embed.FS

// LibraryEngineEdition and LibraryCapabilities describe this host's engine.
// No optional graph capability bits are present in this kernel image version.
const LibraryEngineEdition = 2
const LibraryCapabilities uint64 = 0

// LibraryPin records portable identity, resolution kind and exact content.
type LibraryPin struct{ Path, Kind, SHA256 string }

type Library struct {
	LibraryPin
	Root           string
	Manifest       edition.Manifest
	Files          []notation.SourceFile
	ManifestSource []byte
	Assets         []string
	content        map[string][]byte
}

// UserLibraryDir follows the per-OS config directory, with an explicit override.
func UserLibraryDir() (string, error) {
	if value := os.Getenv("CICADA_LIBRARY"); value != "" {
		return filepath.Abs(value)
	}
	config, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(config, "cicada", "lib"), nil
}

func libraryNamespace(value string) string { return strings.ReplaceAll(value, "/", ".") }

func (s *Sources) readLibraries(overrides map[string][]byte) error {
	s.Bindings = map[string]map[string]string{}
	s.Libraries = map[string]*Library{}
	active := map[string]bool{}
	declarations := map[string]map[string]bool{}
	if s.Manifest.Library != "" {
		for _, file := range s.Files {
			if ds := notation.CheckLibrary(file); len(ds) > 0 {
				return &SourceError{Diagnostic: ds[0]}
			}
		}
	}
	var visit func([]notation.SourceFile, string) error
	visit = func(files []notation.SourceFile, scope string) error {
		bindings := map[string]string{}
		declarations[scope] = notation.DeclarationNames(files)
		s.Bindings[scope] = bindings
		for _, file := range files {
			imports := notation.ReadImports(file)
			for _, imp := range imports {
				fail := func(code, message string, cause error) error {
					return sourceError(imp.Position.File, imp.Position.Line, imp.Position.Column, code, message, cause)
				}
				if !edition.ValidLibraryPath(imp.Path) {
					return fail("CICADA-LIB-PATH", "expected a slash-separated library path without traversal", nil)
				}
				alias := path.Base(imp.Path)
				// Canonical declaration IDs use every path component. The builtin
				// prefix is reserved for engine drum recipes, as is its import alias.
				if alias == "builtin" || strings.HasPrefix(imp.Path, "builtin/") {
					return fail("CICADA-LIB-SHADOW", "builtin is a reserved namespace", nil)
				}
				if declarations[scope][alias] {
					return fail("CICADA-LIB-SHADOW", "import namespace conflicts with declaration "+alias, nil)
				}
				if old, ok := bindings[alias]; ok && old != libraryNamespace(imp.Path) {
					return fail("CICADA-LIB-SHADOW", "imports share namespace "+alias, nil)
				}
				bindings[alias] = libraryNamespace(imp.Path)
				s.Imports = append(s.Imports, imp)
				if active[imp.Path] {
					return fail("CICADA-LIB-CYCLE", "import cycle through "+imp.Path, nil)
				}
				if cached := s.Libraries[imp.Path]; cached != nil {
					ed := s.Manifest.Edition
					if scope != "" {
						ed = s.Libraries[scope].Manifest.Edition
					}
					if ed == 0 {
						ed = notation.SourceEdition(s.Files[0])
					}
					if cached.Manifest.Edition > ed {
						return fail("CICADA-VERSION", "library source edition exceeds the score edition", nil)
					}
					continue
				}
				lib, err := s.resolveLibrary(imp.Path, overrides)
				if err != nil {
					code := "CICADA-LIB-MISSING"
					if d, ok := err.(*SourceError); ok {
						code = d.Diagnostic.Code
					}
					return fail(code, err.Error(), err)
				}
				if lib.Manifest.Library != imp.Path {
					return fail("CICADA-LIB-PATH", "library manifest path does not match import "+imp.Path, nil)
				}
				if lib.Manifest.EngineEdition > LibraryEngineEdition || lib.Manifest.Capabilities&^LibraryCapabilities != 0 {
					return fail("CICADA-LIB-CAPABILITY", "library engine edition or capability requirements are unsupported", nil)
				}
				ed := s.Manifest.Edition
				if scope != "" {
					ed = s.Libraries[scope].Manifest.Edition
				}
				if ed == 0 {
					ed = notation.SourceEdition(s.Files[0])
				}
				if lib.Manifest.Edition > ed {
					return fail("CICADA-VERSION", "library source edition exceeds the score edition", nil)
				}
				s.Libraries[imp.Path] = lib
				active[imp.Path] = true
				if err := visit(lib.Files, imp.Path); err != nil {
					return err
				}
				active[imp.Path] = false
				s.LibraryOrder = append(s.LibraryOrder, imp.Path)
			}
		}
		return nil
	}
	if err := visit(s.Files, ""); err != nil {
		return err
	}
	for i := range s.Files {
		s.Files[i].Declarations = declarations[""]
		if len(s.Bindings[""]) > 0 {
			s.Files[i].Bindings = s.Bindings[""]
		}
	}
	for _, name := range s.LibraryOrder {
		lib := s.Libraries[name]
		for _, file := range lib.Files {
			file.Library, file.Bindings, file.Root, file.Edition = name, s.Bindings[name], lib.Root, lib.Manifest.Edition
			file.Declarations = declarations[name]
			s.Files = append(s.Files, file)
		}
	}
	return nil
}

func (s *Sources) resolveLibrary(name string, overrides map[string][]byte) (*Library, error) {
	user, err := UserLibraryDir()
	if err != nil {
		return nil, err
	}
	type location struct {
		kind, root string
		files      fs.FS
		close      func() error
	}
	var candidates []location
	if strings.HasPrefix(name, "std/") {
		sub, err := fs.Sub(standardLibraries, name)
		if err == nil {
			if _, err := fs.Stat(sub, "cicada.mod"); err == nil {
				candidates = append(candidates, location{kind: "std", root: name, files: sub})
			}
		}
	}
	for _, base := range []struct{ kind, dir string }{{"project", filepath.Join(s.Root, "lib")}, {"user", user}} {
		root, err := os.OpenRoot(base.dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		child, openErr := root.OpenRoot(filepath.FromSlash(name))
		root.Close()
		if os.IsNotExist(openErr) {
			continue
		}
		if openErr != nil {
			return nil, sourceError("", 1, 1, "CICADA-LIB-PATH", "library path must stay inside its search root", openErr)
		}
		if _, err := child.Stat("cicada.mod"); os.IsNotExist(err) {
			child.Close()
			continue
		} else if err != nil {
			child.Close()
			return nil, err
		}
		candidates = append(candidates, location{kind: base.kind, root: filepath.Join(base.dir, filepath.FromSlash(name)), files: child.FS(), close: child.Close})
	}
	defer func() {
		for _, c := range candidates {
			if c.close != nil {
				c.close()
			}
		}
	}()
	if len(candidates) == 0 {
		return nil, fmt.Errorf("library %q not found", name)
	}
	if len(candidates) > 1 {
		// A project pin explicitly selects a vendored copy. A user pin may
		// coexist with an identical project copy during an idempotent vendor.
		pins, err := s.readSum()
		if err != nil {
			return nil, err
		}
		pin, pinned := pins[name]
		if pinned && len(candidates) == 2 && candidates[0].kind == "project" && candidates[1].kind == "user" {
			local, err := readLibrary(name, "project", candidates[0].root, candidates[0].files, overrides)
			if err != nil {
				return nil, err
			}
			if pin.Kind == "project" {
				return local, nil
			}
			personal, err := readLibrary(name, "user", candidates[1].root, candidates[1].files, overrides)
			if err != nil {
				return nil, err
			}
			if pin.Kind == "user" && local.SHA256 == personal.SHA256 && personal.SHA256 == pin.SHA256 {
				return personal, nil
			}
		}
		return nil, sourceError("", 1, 1, "CICADA-LIB-SHADOW", "library "+name+" exists in more than one search location", nil)
	}
	c := candidates[0]
	return readLibrary(name, c.kind, c.root, c.files, overrides)
}

// InspectLibrary resolves and hashes a library without requiring a score.
func InspectLibrary(projectDir, name string) (*Library, error) {
	if !edition.ValidLibraryPath(name) {
		return nil, fmt.Errorf("CICADA-LIB-PATH: invalid library path")
	}
	lib, err := (&Sources{Root: projectDir}).resolveLibrary(name, nil)
	if err == nil && lib.Manifest.Library != name {
		return nil, sourceError("", 1, 1, "CICADA-LIB-PATH", "library manifest path does not match "+name, nil)
	}
	return lib, err
}

func readLibrary(name, kind, root string, files fs.FS, overrides map[string][]byte) (*Library, error) {
	data, err := fs.ReadFile(files, "cicada.mod")
	if err != nil {
		return nil, err
	}
	m, err := edition.ParseProjectManifest(data)
	if err != nil {
		return nil, sourceError("", 1, 1, "CICADA-MANIFEST", err.Error(), err)
	}
	lib := &Library{LibraryPin: LibraryPin{Path: name, Kind: kind}, Root: root, Manifest: m, ManifestSource: data}
	content := map[string][]byte{"cicada.mod": data}
	for _, source := range m.SourcePaths() {
		data, err := fs.ReadFile(files, source)
		if err != nil {
			return nil, sourceError("", 1, 1, "CICADA-LIB-SOURCE", "cannot read library source "+source, err)
		}
		full := filepath.Join(root, filepath.FromSlash(source))
		if changed, ok := overrides[full]; ok {
			data = changed
		}
		content[source] = data
		file := notation.SourceFile{Path: full, Source: data}
		if ds := notation.CheckLibrary(file); len(ds) != 0 {
			return nil, &SourceError{Diagnostic: ds[0]}
		}
		lib.Files = append(lib.Files, file)
	}
	audioFiles := map[string]bool{}
	if _, err := fs.Stat(files, "audio"); err == nil {
		if err := fs.WalkDir(files, "audio", func(name string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			if !entry.Type().IsRegular() {
				return fmt.Errorf("library audio must contain regular files")
			}
			lib.Assets = append(lib.Assets, name)
			if _, source := content[name]; !source {
				content[name] = nil
				audioFiles[name] = true
			}
			return nil
		}); err != nil {
			return nil, sourceError("", 1, 1, "CICADA-LIB-PATH", err.Error(), err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	names := make([]string, 0, len(content))
	for name := range content {
		names = append(names, name)
	}
	sort.Strings(names)
	hash := sha256.New()
	hash.Write([]byte("cicada.library/1\x00"))
	var size [8]byte
	for _, name := range names {
		binary.BigEndian.PutUint64(size[:], uint64(len(name)))
		hash.Write(size[:])
		hash.Write([]byte(name))
		if !audioFiles[name] {
			binary.BigEndian.PutUint64(size[:], uint64(len(content[name])))
			hash.Write(size[:])
			hash.Write(content[name])
			continue
		}
		file, err := files.Open(name)
		if err != nil {
			return nil, err
		}
		info, err := file.Stat()
		if err != nil {
			file.Close()
			return nil, err
		}
		binary.BigEndian.PutUint64(size[:], uint64(info.Size()))
		hash.Write(size[:])
		n, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if copyErr != nil {
			return nil, copyErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if n != info.Size() {
			return nil, fmt.Errorf("library audio changed during hashing")
		}
	}
	lib.SHA256 = hex.EncodeToString(hash.Sum(nil))
	lib.content = content
	return lib, nil
}

// ParseLibrarySum accepts the generated, line-oriented lock format.
func ParseLibrarySum(data []byte) (map[string]LibraryPin, error) {
	pins := map[string]LibraryPin{}
	for i, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		fail := func() (map[string]LibraryPin, error) {
			return nil, sourceError("cicada.sum", i+1, 1, "CICADA-LIB-SUM", "expected path std|project|user sha256:<64 lowercase hex digits>", nil)
		}
		if len(fields) != 3 || !edition.ValidLibraryPath(fields[0]) || (fields[1] != "std" && fields[1] != "project" && fields[1] != "user") {
			return fail()
		}
		hash := strings.TrimPrefix(fields[2], "sha256:")
		decoded, err := hex.DecodeString(hash)
		if err != nil || len(decoded) != 32 || fields[2] != "sha256:"+strings.ToLower(hash) {
			return fail()
		}
		if _, found := pins[fields[0]]; found {
			return fail()
		}
		pins[fields[0]] = LibraryPin{Path: fields[0], Kind: fields[1], SHA256: hash}
	}
	return pins, nil
}

func FormatLibrarySum(pins map[string]LibraryPin) []byte {
	names := make([]string, 0, len(pins))
	for name := range pins {
		names = append(names, name)
	}
	sort.Strings(names)
	var b bytes.Buffer
	b.WriteString("# cicada.sum: generated by cicada lib update\n")
	for _, name := range names {
		p := pins[name]
		fmt.Fprintf(&b, "%s %s sha256:%s\n", p.Path, p.Kind, p.SHA256)
	}
	return b.Bytes()
}

func (s *Sources) verifyLibraries() []notation.Diagnostic {
	if len(s.Imports) == 0 {
		return nil
	}
	pins, err := s.readSum()
	if err != nil {
		return []notation.Diagnostic{err.(*SourceError).Diagnostic}
	}
	var ds []notation.Diagnostic
	for _, imp := range s.Imports {
		lib := s.Libraries[imp.Path]
		pin, found := pins[imp.Path]
		if found && pin == lib.LibraryPin {
			continue
		}
		message := "unpinned library " + imp.Path + "; run cicada lib update " + imp.Path
		if found {
			message = fmt.Sprintf("library %s changed: %s sha256:%s -> %s sha256:%s; run cicada lib update %s", imp.Path, pin.Kind, pin.SHA256, lib.Kind, lib.SHA256, imp.Path)
		}
		ds = append(ds, notation.Diagnostic{Code: "CICADA-LIB-HASH", Severity: "error", Message: message, Position: imp.Position})
	}
	return ds
}

func (s *Sources) readSum() (map[string]LibraryPin, error) {
	filename := filepath.Join(s.Root, "cicada.sum")
	if s.librarySum != nil {
		pins, err := ParseLibrarySum(s.librarySum)
		if err != nil {
			err.(*SourceError).Diagnostic.Position.File = filename
		}
		return pins, err
	}
	data, err := os.ReadFile(filename)
	if os.IsNotExist(err) {
		return map[string]LibraryPin{}, nil
	}
	if err != nil {
		return nil, sourceError(filename, 1, 1, "CICADA-LIB-SUM", err.Error(), err)
	}
	pins, err := ParseLibrarySum(data)
	if err != nil {
		err.(*SourceError).Diagnostic.Position.File = filename
	}
	return pins, err
}

// UpdateLibraries changes only the requested pin (or every imported pin for an
// empty path). It returns reviewable old/new records; callers write atomically.
func (s *Sources) UpdateLibraries(name string) ([]byte, []string, error) {
	pins, err := s.readSum()
	if err != nil {
		return nil, nil, err
	}
	if name != "" && s.Libraries[name] == nil {
		return nil, nil, fmt.Errorf("library %q is not imported", name)
	}
	names := append([]string(nil), s.LibraryOrder...)
	sort.Strings(names)
	var changes []string
	if name == "" {
		var stale []string
		for old := range pins {
			if s.Libraries[old] == nil {
				stale = append(stale, old)
			}
		}
		sort.Strings(stale)
		for _, old := range stale {
			delete(pins, old)
			changes = append(changes, old+": removed unused pin")
		}
	}
	for _, current := range names {
		if name != "" && name != current {
			continue
		}
		pin := s.Libraries[current].LibraryPin
		old, found := pins[current]
		if found && old == pin {
			continue
		}
		before := "unpinned"
		if found {
			before = old.Kind + " sha256:" + old.SHA256
		}
		changes = append(changes, fmt.Sprintf("%s: %s -> %s sha256:%s", current, before, pin.Kind, pin.SHA256))
		pins[current] = pin
	}
	return FormatLibrarySum(pins), changes, nil
}

// LibraryLocation identifies an available library without loading its source.
type LibraryLocation struct{ Path, Kind string }

// ListLibraries lists manifests in path order, retaining each location of a
// shadowed path. It neither verifies nor changes the project's pins.
func ListLibraries(projectDir string) ([]LibraryLocation, error) {
	var libraries []LibraryLocation
	var discoveryErr error
	collect := func(files fs.FS, kind string) error {
		return fs.WalkDir(files, ".", func(name string, entry fs.DirEntry, err error) error {
			if err != nil {
				discoveryErr = errors.Join(discoveryErr, err)
				return nil
			}
			if !entry.IsDir() && path.Base(name) == "cicada.mod" && edition.ValidLibraryPath(path.Dir(name)) {
				libraries = append(libraries, LibraryLocation{Path: path.Dir(name), Kind: kind})
			}
			return nil
		})
	}
	if err := collect(standardLibraries, "std"); err != nil {
		discoveryErr = errors.Join(discoveryErr, err)
	}
	user, err := UserLibraryDir()
	if err != nil {
		discoveryErr = errors.Join(discoveryErr, err)
	}
	for _, base := range []struct{ kind, dir string }{{"project", filepath.Join(projectDir, "lib")}, {"user", user}} {
		if base.dir == "" {
			continue
		}
		root, err := os.OpenRoot(base.dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			discoveryErr = errors.Join(discoveryErr, err)
			continue
		}
		err = collect(root.FS(), base.kind)
		root.Close()
		if err != nil {
			discoveryErr = errors.Join(discoveryErr, err)
		}
	}
	// Stable order preserves std, project, user precedence for a shared path.
	sort.SliceStable(libraries, func(i, j int) bool { return libraries[i].Path < libraries[j].Path })
	return libraries, discoveryErr
}

// LibraryPaths lists unique available paths for completion. Resolution still
// checks shadowing when a path is imported.
func LibraryPaths(projectDir string) []string {
	libraries, _ := ListLibraries(projectDir)
	seen := map[string]bool{}
	for _, library := range libraries {
		seen[library.Path] = true
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
