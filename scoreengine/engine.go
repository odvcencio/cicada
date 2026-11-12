// Package scoreengine is the edit path of a Studio tab: it holds the project's
// files in memory, applies edit envelopes with the same code and checks as the
// service, and keeps Undo, Redo and the commits the service has not
// acknowledged. It reads no disk and no audio, links no net/http and no
// syscall/js, and tests natively and as js/wasm.
package scoreengine

import (
	"errors"
	"fmt"
	"path"
	"strings"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/host/schedule/rules"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// Root is the folder under which project code sees the snapshot's files. The
// engine's own API names files by project-relative, slash-separated paths;
// absolute paths exist only at the service boundary.
const Root = "/project"

// Snapshot is the project as the service sent it.
type Snapshot struct {
	Entry string            // the entry score, project-relative
	Files map[string][]byte // sources, cicada.mod, cicada.sum, library sources; project-relative
}

// Options tune an engine.
type Options struct{}

// Engine holds one tab's working copy of a project.
type Engine struct {
	files    map[string][]byte // by project-relative path
	entry    string
	source   []byte
	compiler *compiler
}

// Open loads a snapshot and compiles its entry.
func Open(s Snapshot, opts Options) (*Engine, error) {
	entry, err := cleanPath(s.Entry)
	if err != nil {
		return nil, err
	}
	files := make(map[string][]byte, len(s.Files))
	memory := make(map[string][]byte, len(s.Files))
	for name, data := range s.Files {
		clean, err := cleanPath(name)
		if err != nil {
			return nil, err
		}
		files[clean] = data
		memory[absolute(clean)] = data
	}
	source, ok := files[entry]
	if !ok {
		return nil, fmt.Errorf("snapshot has no entry %s", entry)
	}
	e := &Engine{files: files, entry: entry, source: source}
	e.compiler = &compiler{files: project.MemoryFiles{Files: memory}, path: absolute(entry), packs: noPacks{}}
	if _, err := e.compiler.Compile(source, nil); err != nil {
		return nil, err
	}
	return e, nil
}

// Apply applies an envelope to the working copy and makes the result the new
// working copy.
func (e *Engine) Apply(env edits.Envelope, writer edits.ParamWriter) (*edits.Result, error) {
	memory := e.compiler.files
	path := absolute(e.entry)
	sources, err := project.ReadSourcesIn(memory, path, map[string][]byte{path: e.source})
	if err != nil {
		return nil, err
	}
	opts := edits.Options{Compiler: e.compiler, Path: path, ParamWriter: writer, Sources: sources.Files, ManifestPath: sources.ManifestPath}
	opts.ParseProject = func(source []byte, files map[string][]byte) (*notation.Score, []notation.Diagnostic, error) {
		overrides := cloneFiles(files)
		overrides[path] = source
		return project.LoadScoreIn(memory, path, overrides)
	}
	result, err := edits.Apply(e.source, env, opts)
	if err != nil {
		return nil, err
	}
	e.source = result.Source
	return result, nil
}

// Source is the working text of a project file.
func (e *Engine) Source(name string) []byte {
	if name == e.entry {
		return e.source
	}
	return e.files[name]
}

// Revision is the working revision of the entry.
func (e *Engine) Revision() string { return edits.Revision(e.source) }

func absolute(name string) string { return Root + "/" + name }

func cleanPath(name string) (string, error) {
	clean := path.Clean(name)
	if name == "" || strings.HasPrefix(clean, "/") || clean == "." || strings.HasPrefix(clean, "../") || clean == ".." {
		return "", fmt.Errorf("snapshot path %q is not project-relative", name)
	}
	return clean, nil
}

// noPacks refuses every sampler pack until the snapshot carries manifests.
type noPacks struct{}

func (noPacks) Check(path, pin string) error {
	return errors.New("sampler pack " + path + " is not in the snapshot")
}

var _ rules.Packs = noPacks{}
