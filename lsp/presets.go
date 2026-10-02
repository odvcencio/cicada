package lsp

import (
	"bytes"
	"regexp"
	"sort"
	"strings"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

var presetBlock = regexp.MustCompile(`(?m)\bpreset\s+[a-z_][a-z0-9_.-]*\s*\{`)
var presetTarget = regexp.MustCompile(`\binstrument\s*=\s*([a-z_][a-z0-9_.-]*)`)

func presetCompletion(files *project.Sources, source []byte, at position) ([]map[string]any, bool) {
	offset := byteOffset(source, at)
	matches := presetBlock.FindAllIndex(source[:offset], -1)
	if len(matches) == 0 {
		return nil, false
	}
	start := matches[len(matches)-1][1]
	block := source[start:offset]
	if bytes.ContainsRune(block, '}') {
		return nil, false
	}
	line := block
	if n := bytes.LastIndexByte(block, '\n'); n >= 0 {
		line = block[n+1:]
	}
	prefix := strings.TrimSpace(string(line))
	// Parse a repaired buffer so completion works before the score is valid.
	repaired := append([]byte(nil), source[:offset]...)
	if n := bytes.LastIndexByte(repaired, '\n'); n >= start {
		repaired = repaired[:n]
	}
	depth := bytes.Count(repaired, []byte("{")) - bytes.Count(repaired, []byte("}"))
	repaired = append(repaired, []byte(strings.Repeat("}", max(depth, 0)))...)
	var score *notation.Score
	if files != nil {
		copies := append([]notation.SourceFile(nil), files.Files...)
		replaced := false
		for i := range copies {
			if bytes.Equal(copies[i].Source, source) {
				copies[i].Source = repaired
				replaced = true
			}
		}
		if replaced {
			score, _ = notation.ParseFiles(copies, max(files.Manifest.Edition, 1))
		}
	}
	if score == nil {
		score, _ = notation.Parse(repaired)
	}
	if score == nil {
		score = &notation.Score{}
	}
	if equal := strings.IndexByte(prefix, '='); equal >= 0 && strings.TrimSpace(prefix[:equal]) == "instrument" {
		targetPrefix := strings.TrimSpace(prefix[equal+1:])
		names := []string{"acid", "drums", "audio", "builtin.bd", "builtin.sd", "builtin.ch", "builtin.oh", "builtin.cp", "builtin.rs", "builtin.lt", "builtin.mt", "builtin.ht", "builtin.cb", "builtin.cy", "builtin.delay", "builtin.reverb", "builtin.drive", "builtin.comp"}
		for _, i := range score.Instruments {
			names = append(names, i.Name)
		}
		for _, k := range score.Kits {
			names = append(names, k.Name)
		}
		for _, s := range score.Samplers {
			names = append(names, s.Name)
		}
		for _, e := range score.Effects {
			names = append(names, e.Name)
		}
		names = completionSourceNames(names, score)
		sort.Strings(names)
		return matchingCompletion(names, targetPrefix, "preset target"), true
	}
	if strings.Contains(prefix, "=") {
		return []map[string]any{}, true
	}
	target := presetTarget.FindSubmatch(block)
	if len(target) != 2 {
		return matchingCompletion([]string{"instrument"}, prefix, "preset target"), true
	}
	targetName := string(target[1])
	if first, rest, ok := strings.Cut(targetName, "."); ok {
		if namespace, found := score.LibraryAliases[first]; found {
			targetName = namespace + "." + rest
		}
	}
	var items []map[string]any
	for _, d := range notation.PresetDescriptors(score, targetName) {
		if strings.HasPrefix(d.Source, prefix) {
			items = append(items, map[string]any{"label": d.Source, "insertText": d.Source, "detail": d.Unit + "; default " + formatDescriptorDefault(d)})
		}
	}
	return items, true
}

func completionSourceNames(names []string, score *notation.Score) []string {
	var result []string
	for _, name := range names {
		if origin, ok := score.Origins[name]; ok {
			local := strings.TrimPrefix(name, strings.ReplaceAll(origin.Library, "/", ".")+".")
			if strings.HasPrefix(local, "_") {
				continue
			}
			for alias, namespace := range score.LibraryAliases {
				if namespace == strings.ReplaceAll(origin.Library, "/", ".") {
					result = append(result, alias+"."+local)
				}
			}
		} else {
			result = append(result, name)
		}
	}
	return result
}
