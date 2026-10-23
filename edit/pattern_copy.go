package edit

import (
	"bytes"
	"fmt"
)

func patternCopyName(plan *Plan, name string) error {
	if !patternName.MatchString(name) {
		return fmt.Errorf("name must start with a lowercase letter or underscore and contain at most 64 letters, digits, underscores, or hyphens")
	}
	if name == "off" || name == "keep" || name == "stop" {
		return fmt.Errorf("choose a lowercase pattern name of at most 64 letters, digits, underscores, or hyphens; off, keep, and stop are scene controls")
	}
	for _, pattern := range plan.Patterns {
		if pattern.ID == name {
			return fmt.Errorf("pattern %q already exists", name)
		}
	}
	for _, clip := range plan.Clips {
		if clip.ID == name {
			return fmt.Errorf("clip %q already exists; choose another pattern name", name)
		}
	}
	return nil
}

func localPatternCopy(ctx *Context, id, name, track string) (string, error) {
	plan, err := ctx.CurrentPlan()
	if err != nil {
		return "", err
	}
	if err := patternCopyName(plan, name); err != nil {
		return "", err
	}
	for _, parent := range plan.Patterns {
		if parent.ID != id {
			continue
		}
		var clone string
		if track != "" && parent.CopyForTrack != nil {
			clone, err = parent.CopyForTrack(name, track)
		} else if parent.CopySource != nil {
			clone, err = parent.CopySource(name)
		} else {
			return "", fmt.Errorf("compiled pattern copying is unavailable")
		}
		if err != nil {
			return "", err
		}
		declarations, err := textDeclarations([]byte(clone))
		if err != nil || len(declarations) != 1 || !declarationMatches(declarations[0].kind, "pattern") || declarations[0].name != name {
			return "", fmt.Errorf("pattern copy must contain one matching declaration")
		}
		return clone, nil
	}
	return "", fmt.Errorf("unknown pattern %q", id)
}

func appendPatternCopy(source []byte, clone string) []byte {
	newline := Newline(source)
	copy := bytes.ReplaceAll([]byte(clone), []byte("\r\n"), []byte("\n"))
	copy = bytes.ReplaceAll(copy, []byte("\n"), []byte(newline))
	return append(bytes.Clone(source), []byte(newline+newline+string(copy)+newline)...)
}
