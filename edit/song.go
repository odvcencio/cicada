package edit

import (
	"bytes"
	"fmt"
	"strconv"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/taproot/walk"
	"m31labs.dev/cicada/notation"
)

type MoveSongEntry struct {
	Entity EntityID `json:"entity"`
	Target int      `json:"target"`
}

func (MoveSongEntry) Kind() string { return "movesongentry" }

type SetSongBars struct {
	Entity EntityID `json:"entity"`
	Bars   int      `json:"bars"`
}

func (SetSongBars) Kind() string { return "setsongbars" }

type AppendSongEntry struct {
	Scene string `json:"scene"`
	Bars  int    `json:"bars"`
}

func (AppendSongEntry) Kind() string { return "appendsongentry" }

type DuplicateSongEntry struct {
	Entity EntityID `json:"entity"`
}

func (DuplicateSongEntry) Kind() string { return "duplicatesongentry" }

type DeleteSongEntry struct {
	Entity EntityID `json:"entity"`
}

func (DeleteSongEntry) Kind() string { return "deletesongentry" }

type SetSongScene struct {
	Entity EntityID `json:"entity"`
	Scene  string   `json:"scene"`
}

func (SetSongScene) Kind() string { return "setsongscene" }

func init() {
	Register("movesongentry", func() Intent { return &MoveSongEntry{} })
	Register("setsongbars", func() Intent { return &SetSongBars{} })
	Register("appendsongentry", func() Intent { return &AppendSongEntry{} })
	Register("duplicatesongentry", func() Intent { return &DuplicateSongEntry{} })
	Register("deletesongentry", func() Intent { return &DeleteSongEntry{} })
	Register("setsongscene", func() Intent { return &SetSongScene{} })
	for _, kind := range []string{"movesongentry", "setsongbars", "appendsongentry", "duplicatesongentry", "deletesongentry", "setsongscene"} {
		Handle(kind, editSong)
	}
}

func editSong(ctx *Context, intent Intent) error {
	var entity EntityID
	var action, scene, label string
	var index, target, bars int
	switch in := intent.(type) {
	case *MoveSongEntry:
		entity, action, target = in.Entity, "move", in.Target
	case *SetSongBars:
		entity, action, bars = in.Entity, "bars", in.Bars
	case *AppendSongEntry:
		action, scene, bars = "append", in.Scene, in.Bars
	case *DuplicateSongEntry:
		entity, action = in.Entity, "duplicate"
	case *DeleteSongEntry:
		entity, action = in.Entity, "delete"
	case *SetSongScene:
		entity, action, scene = in.Entity, "scene", in.Scene
	}
	if action != "append" {
		if _, err := ParseEntityID(string(entity)); err != nil {
			return err
		}
		if entity.Kind() != KindSong {
			return fmt.Errorf("song edit needs a song entity, got %q", entity)
		}
		index, _ = strconv.Atoi(entity.Name())
	}
	switch action {
	case "move":
		label = fmt.Sprintf("Song block %d moved to %d", index+1, target+1)
	case "bars":
		label = fmt.Sprintf("Song block %d set to %d bars", index+1, bars)
	case "append":
		label = fmt.Sprintf("Arrangement · %s added for %d bars", scene, bars)
	default:
		label = fmt.Sprintf("Arrangement · block %d · %s", index+1, action)
	}
	updated, err := songSource(ctx, action, index, target, bars, scene)
	if err != nil {
		return err
	}
	ctx.Source, ctx.plan = updated, nil
	ctx.SetLabel(label)
	return nil
}

func songSource(ctx *Context, action string, index, target, bars int, scene string) ([]byte, error) {
	source := ctx.Source
	score, diagnostics := ctx.Parse()
	if score == nil || hasErrors(diagnostics) {
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
			return ReplaceSpan(source, ctx.Options.Path, Span{int(value.StartByte()), int(value.EndByte()), scene, ctx.Options.Path})
		}
		if bars < 1 || bars > 999 {
			return nil, fmt.Errorf("song entry must last 1–999 bars")
		}
		if len(entries) == 0 {
			at := int(song.EndByte()) - 1
			return ReplaceSpan(source, ctx.Options.Path, Span{at, at, " " + scene + "*" + strconv.Itoa(bars) + " ", ctx.Options.Path})
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
		return ReplaceSpan(source, ctx.Options.Path, Span{at, at, separator + scene + "*" + strconv.Itoa(bars), ctx.Options.Path})
	case "duplicate", "delete":
		if err := songGapsAreWhitespace(source, song, entries); err != nil {
			return nil, err
		}
		entry := entries[index]
		if action == "delete" {
			if len(entries) < 2 {
				return nil, fmt.Errorf("keep at least one arrangement block")
			}
			return ReplaceSpan(source, ctx.Options.Path, Span{int(entry.StartByte()), int(entry.EndByte()), "", ctx.Options.Path})
		}
		separator := " "
		if bytes.Contains(source[song.StartByte():song.EndByte()], []byte("\n")) {
			separator = "\n  "
			if bytes.Contains(source, []byte("\r\n")) {
				separator = "\r\n  "
			}
		}
		at := int(entry.EndByte())
		return ReplaceSpan(source, ctx.Options.Path, Span{at, at, string(append([]byte(separator), source[entry.StartByte():entry.EndByte()]...)), ctx.Options.Path})
	case "bars":
		if bars < 1 || bars > 999 {
			return nil, fmt.Errorf("song entry must last 1–999 bars")
		}
		entry := entries[index]
		if count := walker.Field(entry, "bars"); count != nil {
			return ReplaceSpan(source, ctx.Options.Path, Span{int(count.StartByte()), int(count.EndByte()), strconv.Itoa(bars), ctx.Options.Path})
		}
		if bars == 1 {
			return bytes.Clone(source), nil
		}
		at := int(entry.EndByte())
		return ReplaceSpan(source, ctx.Options.Path, Span{at, at, "*" + strconv.Itoa(bars), ctx.Options.Path})
	case "move":
		if target < 0 || target >= len(entries) {
			return nil, fmt.Errorf("song destination is out of range")
		}
		if target == index {
			return bytes.Clone(source), nil
		}
		if err := songGapsAreWhitespace(source, song, entries); err != nil {
			return moveSongCommentUnits(source, song, entries, index, target)
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

// A unit includes the entry's indentation, immediately preceding own-line
// comments, and a same-line trailing comment. Blank lines remain separators.
func moveSongCommentUnits(source []byte, song *gts.Node, entries []*gts.Node, index, target int) ([]byte, error) {
	type unit struct{ start, end int }
	units := make([]unit, len(entries))
	open := int(song.StartByte()) + bytes.IndexByte(source[song.StartByte():song.EndByte()], '{') + 1
	close := int(song.EndByte()) - 1
	refusal := func() ([]byte, error) {
		return nil, fmt.Errorf("song comments need a source edit to preserve their attachment")
	}
	for i, entry := range entries {
		start, end := int(entry.StartByte()), int(entry.EndByte())
		lineStart := bytes.LastIndexByte(source[:start], '\n') + 1
		if lineStart >= open && len(bytes.TrimSpace(source[lineStart:start])) == 0 {
			start = lineStart
			for start > open {
				previousEnd := start - 1
				if previousEnd > open && source[previousEnd-1] == '\r' {
					previousEnd--
				}
				previousStart := bytes.LastIndexByte(source[:previousEnd], '\n') + 1
				if previousStart < open || !bytes.HasPrefix(bytes.TrimSpace(source[previousStart:previousEnd]), []byte("//")) {
					break
				}
				start = previousStart
			}
		}
		lineEnd := end
		for lineEnd < close && source[lineEnd] != '\n' && source[lineEnd] != '\r' {
			lineEnd++
		}
		if bytes.HasPrefix(bytes.TrimSpace(source[end:lineEnd]), []byte("//")) {
			end = lineEnd
		}
		units[i] = unit{start, end}
	}
	previous := open
	for _, u := range units {
		if u.start < previous || len(bytes.TrimSpace(source[previous:u.start])) != 0 {
			return refusal()
		}
		previous = u.end
	}
	if len(bytes.TrimSpace(source[previous:close])) != 0 {
		return refusal()
	}
	texts := make([][]byte, len(units))
	for i, u := range units {
		texts[i] = source[u.start:u.end]
	}
	moving := texts[index]
	if index < target {
		copy(texts[index:target], texts[index+1:target+1])
	} else {
		copy(texts[target+1:index+1], texts[target:index])
	}
	texts[target] = moving
	startsWithComment := func(text []byte) bool { return bytes.HasPrefix(bytes.TrimSpace(text), []byte("//")) }
	endsWithComment := func(text []byte) bool {
		lastLine := bytes.LastIndexByte(text, '\n') + 1
		return bytes.Contains(text[lastLine:], []byte("//"))
	}
	updated := append([]byte(nil), source[:units[0].start]...)
	if startsWithComment(texts[0]) && !bytes.Contains(source[open:units[0].start], []byte("\n")) {
		updated = append(updated, Newline(source)...)
	}
	for i, text := range texts {
		updated = append(updated, text...)
		if i+1 < len(units) {
			gap := source[units[i].end:units[i+1].start]
			// A line comment must not swallow the following entry or brace,
			// and an own-line comment must stay on its own line after a move.
			if (endsWithComment(text) || startsWithComment(texts[i+1])) && !bytes.Contains(gap, []byte("\n")) {
				updated = append(updated, Newline(source)...)
			}
			updated = append(updated, gap...)
		}
	}
	if endsWithComment(texts[len(texts)-1]) && !bytes.Contains(source[units[len(units)-1].end:close], []byte("\n")) {
		updated = append(updated, Newline(source)...)
	}
	updated = append(updated, source[units[len(units)-1].end:]...)
	return updated, nil
}
