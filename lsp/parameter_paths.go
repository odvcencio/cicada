package lsp

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	gts "github.com/odvcencio/gotreesitter"
	"m31labs.dev/cicada/internal/paramdefs"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

type pathRegion struct {
	path       string
	start, end int
}

func parameterPathAt(source []byte, offset int) (pathRegion, bool) {
	root, walker, err := notation.ParseTree(source)
	if err != nil {
		return pathRegion{}, false
	}
	var found pathRegion
	var visit func(*gts.Node)
	visit = func(node *gts.Node) {
		if walker.Type(node) == "parameter_path" {
			start, end := int(node.StartByte()), int(node.EndByte())
			if start <= offset && offset < end {
				found = pathRegion{path: walker.Text(node), start: start, end: end}
			}
			return
		}
		for i := 0; i < node.ChildCount(); i++ {
			visit(node.Child(i))
		}
	}
	visit(root)
	return found, found.end > found.start
}

func scenePathContext(source []byte, path string) (*notation.Score, *project.Project, project.ResolvedParam, error) {
	score, _ := notation.Parse(source)
	if score == nil {
		return nil, nil, project.ResolvedParam{}, fmt.Errorf("score has syntax or validation errors")
	}
	compiled, diagnostics := project.FromScore(score)
	if compiled == nil || hasErrors(diagnostics) {
		return nil, nil, project.ResolvedParam{}, fmt.Errorf("score has project errors")
	}
	resolved, err := project.ResolveParameterPath(compiled, path)
	if err != nil {
		return nil, nil, project.ResolvedParam{}, err
	}
	return score, compiled, resolved, nil
}

func parameterPathHover(source []byte, match pathRegion) string {
	_, _, resolved, err := scenePathContext(source, match.path)
	if err != nil {
		return ""
	}
	d := resolved.Descriptor
	unit := d.Unit
	if unit == "" {
		unit = "unitless"
	}
	return fmt.Sprintf("**%s** — %s `%s`\n\nUnit: %s  \nRange: %s  \nDefault: %s  \nLive: %t  \nDisplay step: %s",
		match.path, resolved.OwnerKind, resolved.Owner, unit,
		formatRange(d.Min, d.Max, d.Unit), formatDescriptorDefault(d), d.Live, formatDescriptorStep(d.DisplayStep, d.Unit))
}

func parameterPathDefinition(uri string, source []byte, match pathRegion) any {
	_, _, resolved, err := scenePathContext(source, match.path)
	if err != nil {
		return nil
	}
	root, walker, err := notation.ParseTree(source)
	if err != nil {
		return nil
	}
	var ownerNode *gts.Node
	var visit func(*gts.Node)
	visit = func(node *gts.Node) {
		if ownerNode != nil {
			return
		}
		switch walker.Type(node) {
		case "track_decl":
			if resolved.OwnerKind == "track" && walker.Text(walker.Field(node, "name")) == resolved.Owner {
				ownerNode = walker.Field(node, "name")
			}
		case "fx_decl":
			if resolved.OwnerKind == "effect" && walker.Text(walker.Field(node, "name")) == resolved.Owner {
				ownerNode = walker.Field(node, "name")
			}
		case "tempo_decl":
			if resolved.OwnerKind == "global" && resolved.Owner == "tempo" {
				ownerNode = node
			}
		}
		for i := 0; i < node.ChildCount(); i++ {
			visit(node.Child(i))
		}
	}
	visit(root)
	if ownerNode == nil {
		return nil
	}
	start, end := int(ownerNode.StartByte()), int(ownerNode.EndByte())
	return map[string]any{"uri": uri, "range": region{Start: utf16Position(source, start), End: utf16Position(source, end)}}
}

func parameterPathCompletion(source []byte, at position) []map[string]any {
	offset := byteOffset(source, at)
	lineStart := offset
	for lineStart > 0 && source[lineStart-1] != '\n' {
		lineStart--
	}
	prefix := strings.TrimSpace(string(source[lineStart:offset]))
	if !strings.HasSuffix(prefix, ".") {
		return []map[string]any{}
	}
	if split := strings.LastIndexAny(prefix, " \t{}"); split >= 0 {
		prefix = prefix[split+1:]
	}
	token := strings.TrimSuffix(prefix, ".")
	owner, settingPrefix, hasNested := strings.Cut(token, ".")
	if owner == "" {
		return []map[string]any{}
	}
	trackKind := ""
	trackRe := regexp.MustCompile(`(?m)^\s*track\s+` + regexp.QuoteMeta(owner) + `\s+([a-z_][a-z0-9_-]*)\s*\{`)
	trackMatch := trackRe.FindSubmatch(source)
	trackFound := len(trackMatch) == 2
	if trackFound {
		trackKind = string(trackMatch[1])
	}
	effectRe := regexp.MustCompile(`(?m)^\s*fx\s+` + regexp.QuoteMeta(owner) + `\s*\{`)
	effectFound := effectRe.Match(source)
	if trackFound == effectFound {
		return []map[string]any{}
	}
	var items []map[string]any
	for _, descriptor := range paramdefs.Registry {
		if !descriptor.Live {
			continue
		}
		var setting string
		if trackFound && descriptor.Scope == "track" && descriptorHasVoice(descriptor, trackKind) {
			if hasNested {
				if !strings.HasPrefix(descriptor.Path, settingPrefix) {
					continue
				}
				setting = strings.TrimPrefix(descriptor.Path, settingPrefix)
			} else {
				setting = descriptor.Path
			}
		} else if effectFound && descriptor.Scope == "global" && strings.HasPrefix(descriptor.Path, owner+".") {
			setting = strings.TrimPrefix(descriptor.Path, owner+".")
			if hasNested {
				if !strings.HasPrefix(setting, settingPrefix) {
					continue
				}
				setting = strings.TrimPrefix(setting, settingPrefix)
			}
		}
		if setting == "" {
			continue
		}
		items = append(items, map[string]any{
			"label": setting, "insertText": setting,
			"detail": fmt.Sprintf("%s; %g..%g; default %s", descriptor.Unit, descriptor.Min, descriptor.Max, formatDescriptorDefault(descriptor)),
		})
	}
	return items
}

func descriptorHasVoice(descriptor paramdefs.Descriptor, kind string) bool {
	for _, voice := range descriptor.Voices {
		if voice == kind {
			return true
		}
	}
	return false
}

func pathOwnerPrefix(path pathRegion, owner string) (int, int, bool) {
	if !strings.HasPrefix(path.path, owner+".") {
		return 0, 0, false
	}
	return path.start, path.start + len(owner), true
}

func formatDescriptorDefault(d paramdefs.Descriptor) string {
	if (d.Curve == "enum" || d.Curve == "toggle") && int(d.Default) >= 0 && int(d.Default) < len(d.Values) {
		if d.Curve == "enum" {
			return d.Values[int(d.Default)]
		}
	}
	if d.Curve == "toggle" {
		if d.Default != 0 {
			return "on"
		}
		return "off"
	}
	return formatDescriptorStep(d.Default, d.Unit)
}

func formatRange(minimum, maximum float64, unit string) string {
	return formatNumber(minimum) + ".." + formatNumber(maximum) + unit
}

func formatDescriptorStep(step float64, unit string) string {
	return formatNumber(step) + unit
}

func formatNumber(value float64) string { return strconv.FormatFloat(value, 'f', -1, 64) }
