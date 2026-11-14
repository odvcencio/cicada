package main

import (
	"fmt"
	"net/http"
	"strconv"

	"m31labs.dev/cicada/project"
	"m31labs.dev/gosx"
	"m31labs.dev/gosx/action"
)

// Forms that act on an item by its position in a list (a song block, a
// contour point, a played note, a gesture point or a shared lane) also carry
// target_key: a name for the item, rendered from the project the form shows.
// An action refuses a key that no longer names the item at that position, so
// a form rendered before another change cannot act on a different item. The
// /studio shell compares the key too before it sends a held form.

func songBlockKey(song []project.SongEntry, index int) string {
	bar := 1
	for i, entry := range song {
		if i == index {
			return fmt.Sprintf("%s@%d+%d", entry.Scene, bar, entry.Bars)
		}
		bar += int(entry.Bars)
	}
	return ""
}

func placementTargetKey(p project.Placement) string {
	return fmt.Sprintf("%q/%q/%q/%d/%d", p.ID, p.Track, p.Content, p.AtTick, p.LengthTicks)
}

func contourPointKey(points []project.ContourPoint, index int) string {
	if index < 0 || index >= len(points) {
		return ""
	}
	return fmt.Sprintf("%g:%g:%s", points[index].At, points[index].Value, points[index].Shape)
}

func performedNoteKey(notes []project.PerformedNote, index int) string {
	if index < 0 || index >= len(notes) {
		return ""
	}
	return fmt.Sprintf("%d-%d-%d", notes[index].Tick, notes[index].EndTick, notes[index].Note)
}

func expressionKey(points []project.PerformedExpression, index int) string {
	if index < 0 || index >= len(points) {
		return ""
	}
	return strconv.FormatInt(points[index].Tick, 10)
}

func groupKey(groups []project.ControlGroup, index int) string {
	if index < 0 || index >= len(groups) {
		return ""
	}
	return groups[index].Name
}

func groupPointKey(groups []project.ControlGroup, group, index int) string {
	if group < 0 || group >= len(groups) || index < 0 || index >= len(groups[group].Points) {
		return ""
	}
	return groups[group].Name + "/" + strconv.FormatInt(groups[group].Points[index].Tick, 10)
}

// targetKey names the item a submitted form acts on in p, or "" when the form
// targets nothing by position.
func targetKey(p *project.Project, path string, f map[string]string) string {
	if p == nil {
		return ""
	}
	index := func(name string) int {
		i, err := strconv.Atoi(f[name])
		if err != nil {
			return -1
		}
		return i
	}
	switch path {
	case "/api/song":
		return songBlockKey(p.Song, index("index"))
	case "/api/automation":
		for _, source := range p.Modulators {
			if source.ID == f["source"] && f["action"] == "mod-source-edit" {
				return contourPointKey(source.Points, index("index"))
			}
		}
	case "/api/pattern":
		for _, pattern := range p.Patterns {
			if pattern.ID != f["pattern"] || pattern.Performance == nil {
				continue
			}
			played := pattern.Performance
			note := performedNoteKey(played.Notes, index("note_index"))
			switch f["action"] {
			case "performed-note":
				return performedNoteKey(played.Notes, index("index"))
			case "performance-binding":
				return note
			case "performed-gesture":
				if n := index("note_index"); n >= 0 && n < len(played.Notes) {
					return note + "/" + expressionKey(played.Notes[n].Expressions, index("index"))
				}
			case "performance-group":
				return groupKey(played.Groups, index("index"))
			case "shared-gesture":
				return groupPointKey(played.Groups, index("group_index"), index("index"))
			}
		}
	}
	return ""
}

// targetField renders a form's target_key; a form that creates its item has
// none.
func targetField(key string) gosx.Node {
	if key == "" {
		return gosx.Fragment()
	}
	return hidden("target_key", key)
}

// checkTarget refuses a form whose target_key no longer names the item at its
// position. It checks against the saved project only at the form's own
// revision: at any other revision the service refuses the write itself, and
// the revision check makes this check and the write see the same project.
func (s *studioApp) checkTarget(ctx *action.Context, path string) error {
	key := ctx.FormData["target_key"]
	if key == "" {
		return nil
	}
	var saved workspace
	if err := s.backend.call(ctx.Request.Context(), http.MethodGet, "/api/workspace", nil, &saved); err != nil || saved.Revision != ctx.FormData["revision"] {
		return nil
	}
	if targetKey(saved.Project, path, ctx.FormData) == key {
		return nil
	}
	result := action.Error(http.StatusConflict, "This item changed since it was shown. Check it and submit again.")
	result.Result.Values = ctx.FormData
	return result
}
