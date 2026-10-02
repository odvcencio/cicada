package lsp

import (
	"bytes"
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
	typeName := d.Type
	if typeName == "" {
		typeName = "number"
		if d.Curve == "enum" {
			typeName = "enum"
		}
		if d.Curve == "toggle" {
			typeName = "boolean"
		}
	}
	return fmt.Sprintf("**%s** — %s `%s`\n\nType: %s  \nUnit: %s  \nRange: %s  \nDefault: %s  \nSmoothing: %g ms  \nLive: %t  \nDisplay step: %s",
		match.path, resolved.OwnerKind, resolved.Owner, typeName, unit,
		formatRange(d.Min, d.Max, d.Unit), formatDescriptorDefault(d), d.SmoothingMS, d.Live, formatDescriptorStep(d.DisplayStep, d.Unit))
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
		case "bus_decl":
			if resolved.OwnerKind == "bus" && walker.Text(walker.Field(node, "name")) == resolved.Owner {
				ownerNode = walker.Field(node, "name")
			}
		case "master_decl":
			if resolved.OwnerKind == "master" {
				ownerNode = node
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
	if named := namedMixerCompletion(source, prefix, offset); named != nil {
		return named
	}
	if !strings.HasSuffix(prefix, ".") {
		return []map[string]any{}
	}
	if split := strings.LastIndexAny(prefix, " \t{}"); split >= 0 {
		prefix = prefix[split+1:]
	}
	token := strings.TrimSuffix(prefix, ".")
	owner, settingPrefix, hasNested := strings.Cut(token, ".")
	if hasNested {
		settingPrefix += "."
	}
	if owner == "" {
		return []map[string]any{}
	}
	trackKind, effectKind, scope := "", "", ""
	if match := regexp.MustCompile(`(?m)^\s*track\s+` + regexp.QuoteMeta(owner) + `\s+([a-z_][a-z0-9_-]*)\s*\{`).FindSubmatch(source); len(match) == 2 {
		trackKind, scope = string(match[1]), "track"
	}
	if match := regexp.MustCompile(`(?m)^\s*fx\s+` + regexp.QuoteMeta(owner) + `(?:\s+([a-z_][a-z0-9_-]*))?\s*\{`).FindSubmatch(source); len(match) > 0 {
		effectKind = owner
		if len(match) > 1 && len(match[1]) > 0 {
			effectKind = string(match[1])
		}
		scope = "global"
	}
	if regexp.MustCompile(`(?m)^\s*bus\s+` + regexp.QuoteMeta(owner) + `\s*\{`).Match(source) {
		scope = "bus"
	}
	if owner == "music" || owner == "sfx" {
		scope = "bus"
	}
	if owner == "master" && regexp.MustCompile(`(?m)^\s*master\s*\{`).Match(source) {
		scope = "master"
	}
	if scope == "" {
		return []map[string]any{}
	}
	var items []map[string]any
	for _, descriptor := range paramdefs.Registry {
		var setting string
		if scope == "track" && descriptor.Scope == "track" && descriptorHasVoice(descriptor, trackKind) {
			if hasNested {
				if !strings.HasPrefix(descriptor.Path, settingPrefix) {
					continue
				}
				setting = strings.TrimPrefix(descriptor.Path, settingPrefix)
			} else {
				setting = descriptor.Path
			}
		} else if scope == "global" && descriptor.Scope == "global" && strings.HasPrefix(descriptor.ID, "fx."+effectKind+".") {
			setting = strings.TrimPrefix(descriptor.Path, effectKind+".")
			if hasNested {
				if !strings.HasPrefix(setting, settingPrefix) {
					continue
				}
				setting = strings.TrimPrefix(setting, settingPrefix)
			}
		} else if descriptor.Scope == scope && (scope == "bus" || scope == "master") {
			setting = descriptor.Path
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

func namedMixerCompletion(source []byte, prefix string, offset int) []map[string]any {
	effects := make([]notation.Effect, 0)
	// Completion should keep working in syntactically complete declarations
	// before the rest of the score has its required patterns and song.
	declaration := regexp.MustCompile(`(?m)^\s*fx\s+([a-z_][a-z0-9_-]*)(?:\s+([a-z_][a-z0-9_-]*))?\s*\{`)
	for _, match := range declaration.FindAllSubmatch(source, -1) {
		name, kind := string(match[1]), string(match[1])
		if len(match) > 2 && len(match[2]) > 0 {
			name, kind = string(match[1]), string(match[2])
		}
		effects = append(effects, notation.Effect{Name: name, Kind: kind})
	}
	blockKind, blockName := mixerCompletionBlock(source, offset)
	if equal := strings.IndexByte(prefix, '='); equal >= 0 {
		left, right := strings.TrimSpace(prefix[:equal]), strings.TrimSpace(prefix[equal+1:])
		trimmed := right
		if left == "out" && blockKind == "track" {
			return matchingCompletion([]string{"music", "sfx"}, trimmed, "built-in bus")
		}
		if left == "insert" {
			names := []string{"none"}
			for _, effect := range effects {
				if blockKind == "track" && effect.Kind == "drive" || blockKind == "bus" && blockName == "music" && effect.Kind == "comp" {
					names = append(names, effect.Name)
				}
			}
			if blockKind != "track" && (blockKind != "bus" || blockName != "music") {
				return []map[string]any{}
			}
			return matchingCompletion(names, trimmed, "effect insert")
		}
		return nil
	}
	sendTarget := regexp.MustCompile(`(?:^|\s)send(?:\s+([^\s=]*))?$`).FindStringSubmatch(prefix)
	if len(sendTarget) == 0 || blockKind != "track" {
		return nil
	}
	targetPrefix := ""
	if len(sendTarget) > 1 {
		targetPrefix = sendTarget[1]
	}
	var names []string
	for _, effect := range effects {
		if effect.Kind == "delay" || effect.Kind == "reverb" {
			names = append(names, effect.Name)
		}
	}
	return matchingCompletion(names, targetPrefix, "send effect")
}

func mixerCompletionBlock(source []byte, offset int) (kind, name string) {
	if offset > len(source) {
		offset = len(source)
	}
	prefix := source[:offset]
	declaration := regexp.MustCompile(`(?m)(?:^|\n)\s*(track|bus)\s+([a-z_][a-z0-9_-]*)[^{}]*\{|(?:^|\n)\s*(master)\s*\{`)
	matches := declaration.FindAllSubmatchIndex(prefix, -1)
	if len(matches) == 0 {
		return "", ""
	}
	last := matches[len(matches)-1]
	open := bytes.LastIndexByte(prefix[last[0]:last[1]], '{') + last[0]
	depth := 0
	for _, char := range prefix[open:offset] {
		switch char {
		case '{':
			depth++
		case '}':
			depth--
		}
	}
	if depth <= 0 {
		return "", ""
	}
	if len(last) >= 6 && last[2] >= 0 {
		return string(prefix[last[2]:last[3]]), string(prefix[last[4]:last[5]])
	}
	return "master", "master"
}

func matchingCompletion(names []string, prefix, detail string) []map[string]any {
	items := make([]map[string]any, 0, len(names))
	for _, name := range names {
		if strings.HasPrefix(name, prefix) {
			items = append(items, map[string]any{"label": name, "insertText": name, "detail": detail})
		}
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
