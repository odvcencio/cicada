package edit

import (
	"bytes"
	"fmt"

	"m31labs.dev/cicada/notation"
)

func sceneSource(ctx *Context, name string) (*notation.Score, []byte, string, error) {
	score, ds, err := ctx.ParseProject()
	if err != nil {
		return nil, nil, "", err
	}
	if score == nil || hasErrors(ds) {
		return nil, nil, "", fmt.Errorf("score must validate before editing a scene")
	}
	score, ds = notation.ResolvePresets(score)
	if hasErrors(ds) {
		return nil, nil, "", fmt.Errorf("score must validate before editing a scene")
	}
	for _, scene := range score.Scenes {
		if scene.Name != name {
			continue
		}
		path := scene.Position.File
		if path == "" {
			path = ctx.Options.Path
		}
		if source, ok := ctx.FileSource(path); ok {
			return score, source, path, nil
		}
		return nil, nil, "", fmt.Errorf("scene %s is outside the editable project sources", name)
	}
	return nil, nil, "", fmt.Errorf("unknown declaration %q", name)
}

// Stage an owned project file without changing another declaration's buffer.
// AddFile preserves the original Before across several intents in one batch.
func stageProjectSource(ctx *Context, path string, before, after []byte) []byte {
	if path == ctx.Options.Path {
		return after
	}
	if !bytes.Equal(before, after) {
		ctx.AddFile(File{Path: path, Before: before, After: after})
	}
	return ctx.Source
}
