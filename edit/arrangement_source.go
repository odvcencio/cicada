package edit

import (
	"fmt"

	"m31labs.dev/cicada/notation"
)

// arrangementFile returns only a supplied project buffer. Source positions
// come from the complete project, while patch offsets stay local to its owner.
// Semantic diagnostics are left to final compilation so an edit can repair an
// invalid position or overlap.
func arrangementFile(ctx *Context) ([]byte, string, error) {
	score, diagnostics, err := ctx.ParseProject()
	if err != nil {
		return nil, "", err
	}
	for _, d := range diagnostics {
		if d.Code == "CICADA-SYNTAX" && d.Severity == "error" {
			if source, ok := ctx.FileSource(d.Position.File); ok {
				if _, _, syntax := notation.ParseTree(source); syntax != nil {
					return nil, "", syntax
				}
			}
			return nil, "", fmt.Errorf("%d:%d: %s", d.Position.Line, d.Position.Column, d.Message)
		}
	}
	if score == nil || score.Arrange == nil {
		return nil, "", fmt.Errorf("this score uses scenes; named placements require an arrange block")
	}
	path := score.Arrange.Position.File
	if path == "" {
		path = ctx.Options.Path
	}
	if source, ok := ctx.FileSource(path); ok {
		return source, path, nil
	}
	return nil, "", fmt.Errorf("arrangement is outside the editable project sources")
}
