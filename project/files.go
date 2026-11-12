package project

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ProjectFiles is where a project's files are read from: the operating
// system's files (OSFiles), or files held in memory (MemoryFiles). Paths are
// absolute and in the operating system's form. A missing file or folder is an
// error for which os.IsNotExist holds.
type ProjectFiles interface {
	// ReadFile reads the file at path.
	ReadFile(path string) ([]byte, error)
	// OpenRoot opens the folder dir for reads that cannot leave it, through
	// ".." or a symbolic link, as os.OpenRoot does.
	OpenRoot(dir string) (ProjectRoot, error)
	// UserLibraryDir is the folder of the user's own libraries.
	UserLibraryDir() (string, error)
}

// ProjectRoot is a folder opened by ProjectFiles.OpenRoot. Names are relative
// to it, and a name that leads outside it is refused.
type ProjectRoot interface {
	ReadFile(name string) ([]byte, error)
	Stat(name string) (fs.FileInfo, error)
	OpenRoot(name string) (ProjectRoot, error)
	// FS reads the folder through slash-separated names (io/fs).
	FS() fs.FS
	Close() error
}

// OSFiles reads the operating system's files. ReadSources, LoadScore and the
// other functions without a ProjectFiles argument use it.
type OSFiles struct{}

func (OSFiles) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }

func (OSFiles) OpenRoot(dir string) (ProjectRoot, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	return osRoot{root}, nil
}

func (OSFiles) UserLibraryDir() (string, error) { return UserLibraryDir() }

type osRoot struct{ *os.Root }

func (r osRoot) OpenRoot(name string) (ProjectRoot, error) {
	child, err := r.Root.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	return osRoot{child}, nil
}

// MemoryFiles holds a project's files in memory, keyed by clean absolute path:
// the sources, cicada.mod, cicada.sum and the files of the libraries the
// project imports from its lib folder or the user's. A folder exists when it
// holds a file. Nothing is read from disk, and a score loaded from memory is
// not checked against its audio files: the asset hashes, frames, rates and
// channels it declares are trusted (VerifyAssets runs only on OSFiles).
type MemoryFiles struct {
	Files map[string][]byte
	// UserLibraries is the folder of the user's own libraries, named as the
	// host names it (UserLibraryDir); empty when the files hold none.
	UserLibraries string
}

func (m MemoryFiles) ReadFile(name string) ([]byte, error) {
	if data, ok := m.Files[filepath.Clean(name)]; ok {
		return data, nil
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

func (m MemoryFiles) OpenRoot(dir string) (ProjectRoot, error) {
	dir = filepath.Clean(dir)
	if dir == "." || !m.holdsFolder(dir) {
		return nil, &fs.PathError{Op: "open", Path: dir, Err: fs.ErrNotExist}
	}
	return memoryRoot{files: m.Files, dir: dir}, nil
}

func (m MemoryFiles) UserLibraryDir() (string, error) { return m.UserLibraries, nil }

func (m MemoryFiles) holdsFolder(dir string) bool {
	prefix := strings.TrimSuffix(dir, string(filepath.Separator)) + string(filepath.Separator)
	for name := range m.Files {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// errPathEscapes matches os.Root's refusal of a name that leaves the root.
var errPathEscapes = errors.New("path escapes from parent")

type memoryRoot struct {
	files map[string][]byte
	dir   string
}

// full joins a name to the root, refusing one that leaves it.
func (r memoryRoot) full(op, name string) (string, error) {
	if !filepath.IsLocal(name) {
		return "", &fs.PathError{Op: op, Path: name, Err: errPathEscapes}
	}
	return filepath.Join(r.dir, name), nil
}

func (r memoryRoot) ReadFile(name string) ([]byte, error) {
	full, err := r.full("openat", name)
	if err != nil {
		return nil, err
	}
	if data, ok := r.files[full]; ok {
		return data, nil
	}
	return nil, &fs.PathError{Op: "openat", Path: name, Err: fs.ErrNotExist}
}

func (r memoryRoot) Stat(name string) (fs.FileInfo, error) {
	full, err := r.full("statat", name)
	if err != nil {
		return nil, err
	}
	if data, ok := r.files[full]; ok {
		return memoryInfo{name: filepath.Base(full), size: int64(len(data))}, nil
	}
	if (MemoryFiles{Files: r.files}).holdsFolder(full) {
		return memoryInfo{name: filepath.Base(full), dir: true}, nil
	}
	return nil, &fs.PathError{Op: "statat", Path: name, Err: fs.ErrNotExist}
}

func (r memoryRoot) OpenRoot(name string) (ProjectRoot, error) {
	full, err := r.full("openat", name)
	if err != nil {
		return nil, err
	}
	if !(MemoryFiles{Files: r.files}).holdsFolder(full) {
		return nil, &fs.PathError{Op: "openat", Path: name, Err: fs.ErrNotExist}
	}
	return memoryRoot{files: r.files, dir: full}, nil
}

func (r memoryRoot) FS() fs.FS  { return memoryFS(r) }
func (memoryRoot) Close() error { return nil }

// memoryFS reads a memoryRoot through io/fs names.
type memoryFS memoryRoot

func (f memoryFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	full := filepath.Join(f.dir, filepath.FromSlash(name))
	if data, ok := f.files[full]; ok {
		return &memoryFile{info: memoryInfo{name: path.Base(name), size: int64(len(data))}, data: data}, nil
	}
	prefix := strings.TrimSuffix(full, string(filepath.Separator)) + string(filepath.Separator)
	children := map[string]bool{}
	for key := range f.files {
		rest, ok := strings.CutPrefix(key, prefix)
		if !ok {
			continue
		}
		child, _, nested := strings.Cut(rest, string(filepath.Separator))
		children[child] = children[child] || nested
	}
	if len(children) == 0 {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	entries := make([]fs.DirEntry, 0, len(children))
	for child, dir := range children {
		info := memoryInfo{name: child, dir: dir}
		if !dir {
			info.size = int64(len(f.files[prefix+child]))
		}
		entries = append(entries, fs.FileInfoToDirEntry(info))
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return &memoryDir{info: memoryInfo{name: path.Base(name), dir: true}, entries: entries}, nil
}

type memoryInfo struct {
	name string
	size int64
	dir  bool
}

func (i memoryInfo) Name() string       { return i.name }
func (i memoryInfo) Size() int64        { return i.size }
func (i memoryInfo) ModTime() time.Time { return time.Time{} }
func (i memoryInfo) IsDir() bool        { return i.dir }
func (i memoryInfo) Sys() any           { return nil }
func (i memoryInfo) Mode() fs.FileMode {
	if i.dir {
		return fs.ModeDir | 0o555
	}
	return 0o444
}

type memoryFile struct {
	info memoryInfo
	data []byte
	at   int
}

func (f *memoryFile) Stat() (fs.FileInfo, error) { return f.info, nil }
func (f *memoryFile) Close() error               { return nil }
func (f *memoryFile) Read(b []byte) (int, error) {
	if f.at >= len(f.data) {
		return 0, io.EOF
	}
	n := copy(b, f.data[f.at:])
	f.at += n
	return n, nil
}

type memoryDir struct {
	info    memoryInfo
	entries []fs.DirEntry
	at      int
}

func (d *memoryDir) Stat() (fs.FileInfo, error) { return d.info, nil }
func (d *memoryDir) Close() error               { return nil }
func (d *memoryDir) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.info.name, Err: fs.ErrInvalid}
}
func (d *memoryDir) ReadDir(n int) ([]fs.DirEntry, error) {
	rest := d.entries[d.at:]
	if n <= 0 {
		d.at = len(d.entries)
		return rest, nil
	}
	if len(rest) == 0 {
		return nil, io.EOF
	}
	if n > len(rest) {
		n = len(rest)
	}
	d.at += n
	return rest[:n], nil
}
