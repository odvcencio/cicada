package edit

import (
	"bytes"
	"fmt"
	"maps"

	"m31labs.dev/cicada/notation"
)

// ownedSourceContext gives single-buffer token writers the complete project
// parser/compiler while keeping their offsets and output in one owned buffer.
func ownedSourceContext(ctx *Context, path, id string) (*Context, []byte, error) {
	if path == "" {
		path = ctx.Options.Path
	}
	for _, file := range ctx.Options.Sources {
		if file.Path == path && file.Library != "" {
			return nil, nil, fmt.Errorf("create a local variation of library pattern %q before editing it", id)
		}
	}
	before, ok := ctx.FileSource(path)
	if !ok {
		return nil, nil, fmt.Errorf("source for pattern %q is unavailable", id)
	}
	working := *ctx
	working.Source, working.plan = before, nil
	working.files = append([]File(nil), ctx.files...)
	working.fileIndex = maps.Clone(ctx.fileIndex)
	working.Options = ctx.Options
	working.Options.Path = path
	if path != ctx.Options.Path {
		working.Options.Compiler = ownedSourceCompiler{compiler: ctx.Options.Compiler, entry: bytes.Clone(ctx.Source), path: path, files: ctx.fileOverrides()}
	}
	working.parseProject = func(source []byte, overrides map[string][]byte) (*notation.Score, []notation.Diagnostic, error) {
		project := *ctx
		project.files = append([]File(nil), ctx.files...)
		project.fileIndex = maps.Clone(ctx.fileIndex)
		for file, data := range overrides {
			if file == ctx.Options.Path {
				project.Source = data
			} else {
				before, _ := ctx.FileSource(file)
				project.AddFile(File{Path: file, Before: before, After: data})
			}
		}
		if path == ctx.Options.Path {
			project.Source = source
		} else {
			project.AddFile(File{Path: path, Before: before, After: source})
		}
		return project.ParseProject()
	}
	return &working, before, nil
}

func recordingSource(ctx *Context, score *notation.Score, id string) (*Context, []byte, string, error) {
	for _, pattern := range score.Patterns {
		if pattern.Name != id {
			continue
		}
		path := pattern.Position.File
		if path == "" {
			path = ctx.Options.Path
		}
		working, before, err := ownedSourceContext(ctx, path, id)
		return working, before, path, err
	}
	return nil, nil, "", fmt.Errorf("unknown recorded pattern %q", id)
}

type ownedSourceCompiler struct {
	compiler Compiler
	entry    []byte
	path     string
	files    map[string][]byte
}

func (c ownedSourceCompiler) Compile(source []byte, files map[string][]byte) (*Plan, error) {
	overrides := maps.Clone(c.files)
	if overrides == nil {
		overrides = map[string][]byte{}
	}
	for path, data := range files {
		overrides[path] = data
	}
	overrides[c.path] = source
	return c.compiler.Compile(c.entry, overrides)
}
