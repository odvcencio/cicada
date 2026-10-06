package main

import (
	"bytes"
	"fmt"
	"strconv"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/taproot/walk"
	"m31labs.dev/cicada/notation"
)

// editedSongSource changes only the authored song entries. Reordering moves
// exact CST entry text; comments between entries are refused rather than
// silently reassigned to a different scene.
func editedSongSource(source []byte, action string, index, target, bars int) ([]byte, error) {
	return editedSongBlockSource(source, action, index, target, bars, "")
}

func editedSongBlockSource(source []byte, action string, index, target, bars int, scene string) ([]byte, error) {
	score, diagnostics := notation.Parse(source)
	if score == nil || hasDiagnosticErrors(diagnostics) {
		return nil, fmt.Errorf("score must validate before editing the song")
	}
	root, walker, err := notation.ParseTree(source)
	if err != nil {
		return nil, err
	}
	var song *gts.Node
	for i := 0; i < root.NamedChildCount(); i++ {
		candidate := root.NamedChild(i)
		if walker.Type(candidate) == "song_decl" {
			song = candidate
			break
		}
	}
	if song == nil {
		return nil, fmt.Errorf("score has no song")
	}
	entries := songEntries(song, walker)
	if len(entries) != len(score.Song) || action != "append" && (index < 0 || index >= len(entries)) {
		return nil, fmt.Errorf("song entry is out of range")
	}
	switch action {
	case "scene", "append":
		found := false
		for _, candidate := range score.Scenes {
			found = found || candidate.Name == scene
		}
		if !found {
			return nil, fmt.Errorf("choose an existing scene")
		}
		if action == "scene" {
			value := walker.Field(entries[index], "scene")
			return replaceSongSpan(source, int(value.StartByte()), int(value.EndByte()), []byte(scene))
		}
		if bars < 1 || bars > 999 {
			return nil, fmt.Errorf("song entry must last 1–999 bars")
		}
		if len(entries) == 0 {
			at := int(song.EndByte()) - 1
			return replaceSongSpan(source, at, at, []byte(" "+scene+"*"+strconv.Itoa(bars)+" "))
		}
		if err := songGapsAreWhitespace(source, song, entries); err != nil {
			return nil, err
		}
		gap := source[entries[len(entries)-1].EndByte() : song.EndByte()-1]
		separator := " "
		if bytes.Contains(gap, []byte("\n")) {
			separator = "\n  "
			if bytes.Contains(source, []byte("\r\n")) {
				separator = "\r\n  "
			}
		}
		at := int(entries[len(entries)-1].EndByte())
		return replaceSongSpan(source, at, at, []byte(separator+scene+"*"+strconv.Itoa(bars)))
	case "duplicate", "delete":
		if err := songGapsAreWhitespace(source, song, entries); err != nil {
			return nil, err
		}
		entry := entries[index]
		if action == "delete" {
			if len(entries) < 2 {
				return nil, fmt.Errorf("keep at least one arrangement block")
			}
			return replaceSongSpan(source, int(entry.StartByte()), int(entry.EndByte()), nil)
		}
		separator := " "
		if bytes.Contains(source[song.StartByte():song.EndByte()], []byte("\n")) {
			separator = "\n  "
			if bytes.Contains(source, []byte("\r\n")) {
				separator = "\r\n  "
			}
		}
		at := int(entry.EndByte())
		return replaceSongSpan(source, at, at, append([]byte(separator), source[entry.StartByte():entry.EndByte()]...))
	case "bars":
		if bars < 1 || bars > 999 {
			return nil, fmt.Errorf("song entry must last 1–999 bars")
		}
		entry := entries[index]
		if count := walker.Field(entry, "bars"); count != nil {
			return replaceSongSpan(source, int(count.StartByte()), int(count.EndByte()), []byte(strconv.Itoa(bars)))
		}
		if bars == 1 {
			return bytes.Clone(source), nil
		}
		at := int(entry.EndByte())
		return replaceSongSpan(source, at, at, []byte("*"+strconv.Itoa(bars)))
	case "move":
		if target < 0 || target >= len(entries) {
			return nil, fmt.Errorf("song destination is out of range")
		}
		if target == index {
			return bytes.Clone(source), nil
		}
		if err := songGapsAreWhitespace(source, song, entries); err != nil {
			return nil, err
		}
		texts := make([][]byte, len(entries))
		for i, entry := range entries {
			texts[i] = bytes.Clone(source[entry.StartByte():entry.EndByte()])
		}
		moving := texts[index]
		if index < target {
			copy(texts[index:target], texts[index+1:target+1])
		} else {
			copy(texts[target+1:index+1], texts[target:index])
		}
		texts[target] = moving
		start, end := int(entries[0].StartByte()), int(entries[len(entries)-1].EndByte())
		updated := make([]byte, 0, len(source))
		updated = append(updated, source[:start]...)
		for i, text := range texts {
			updated = append(updated, text...)
			if i+1 < len(entries) {
				updated = append(updated, source[entries[i].EndByte():entries[i+1].StartByte()]...)
			}
		}
		updated = append(updated, source[end:]...)
		return updated, nil
	default:
		return nil, fmt.Errorf("unknown arrangement action")
	}
}

func songEntries(song *gts.Node, walker *walk.Walker) []*gts.Node {
	var entries []*gts.Node
	for i := 0; i < song.NamedChildCount(); i++ {
		entry := song.NamedChild(i)
		if walker.Type(entry) == "song_entry" {
			entries = append(entries, entry)
		}
	}
	return entries
}

func songGapsAreWhitespace(source []byte, song *gts.Node, entries []*gts.Node) error {
	content := source[song.StartByte():song.EndByte()]
	open := bytes.IndexByte(content, '{')
	if open < 0 {
		return fmt.Errorf("song braces are missing")
	}
	first := int(song.StartByte()) + open + 1
	if len(bytes.TrimSpace(source[first:entries[0].StartByte()])) != 0 {
		return fmt.Errorf("song comments need a source edit to preserve their attachment")
	}
	for i := 0; i+1 < len(entries); i++ {
		if len(bytes.TrimSpace(source[entries[i].EndByte():entries[i+1].StartByte()])) != 0 {
			return fmt.Errorf("song comments need a source edit to preserve their attachment")
		}
	}
	last := int(entries[len(entries)-1].EndByte())
	close := int(song.EndByte()) - 1
	if close < last || source[close] != '}' || len(bytes.TrimSpace(source[last:close])) != 0 {
		return fmt.Errorf("song comments need a source edit to preserve their attachment")
	}
	return nil
}

func replaceSongSpan(source []byte, start, end int, text []byte) ([]byte, error) {
	if start < 0 || end < start || end > len(source) {
		return nil, fmt.Errorf("song source span is invalid")
	}
	updated := make([]byte, 0, len(source)-end+start+len(text))
	updated = append(updated, source[:start]...)
	updated = append(updated, text...)
	updated = append(updated, source[end:]...)
	return updated, nil
}
