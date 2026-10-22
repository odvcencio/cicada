package edit

import (
	"bytes"
	"fmt"
	"maps"

	"m31labs.dev/cicada/notation"
)

// Grid token writers still operate on one buffer. Resolve that buffer from
// the complete project, while compiling every intermediate token edit against
// the same entry and the other already-staged destinations.
func recordingSource(ctx *Context, score *notation.Score, id string) (*Context, []byte, string, error) {
	for _, pattern := range score.Patterns {
		if pattern.Name != id {
			continue
		}
		path := pattern.Position.File
		if path == "" {
			path = ctx.Options.Path
		}
		for _, file := range ctx.Options.Sources {
			if file.Path == path && file.Library != "" {
				return nil, nil, "", fmt.Errorf("create a local variation of library pattern %q before recording into it", id)
			}
		}
		before, ok := ctx.FileSource(path)
		if !ok {
			return nil, nil, "", fmt.Errorf("recording source for pattern %q is unavailable", id)
		}
		working := *ctx
		working.Source, working.plan = before, nil
		working.Options = ctx.Options
		working.Options.Path = path
		if path != ctx.Options.Path {
			working.Options.Compiler = recordingSourceCompiler{compiler: ctx.Options.Compiler, entry: bytes.Clone(ctx.Source), path: path, files: ctx.fileOverrides()}
		}
		working.parseSource = func(source []byte) (*notation.Score, []notation.Diagnostic) {
			project := *ctx
			project.files = append([]File(nil), ctx.files...)
			project.fileIndex = maps.Clone(ctx.fileIndex)
			if path == ctx.Options.Path {
				project.Source = source
			} else {
				project.AddFile(File{Path: path, Before: before, After: source})
			}
			score, ds, err := project.ParseProject()
			if err != nil {
				return nil, []notation.Diagnostic{{Severity: "error", Message: err.Error()}}
			}
			return score, ds
		}
		return &working, before, path, nil
	}
	return nil, nil, "", fmt.Errorf("unknown recorded pattern %q", id)
}

type recordingSourceCompiler struct {
	compiler Compiler
	entry    []byte
	path     string
	files    map[string][]byte
}

func (c recordingSourceCompiler) Compile(source []byte, files map[string][]byte) (*Plan, error) {
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
