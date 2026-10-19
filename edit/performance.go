package edit

import (
	"fmt"
	"strings"

	gts "github.com/odvcencio/gotreesitter"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
)

func recordPerformedTake(ctx *Context, recordings []Recording) error {
	if len(recordings) == 0 || len(recordings) > 32 {
		return fmt.Errorf("a performance needs 1 to 32 destinations")
	}
	origin, end, count := int64(1<<60), int64(0), 0
	seen := map[string]bool{}
	for _, recording := range recordings {
		if recording.Track == "" || recording.Pattern == "" || len(recording.Notes) == 0 || seen[recording.Pattern] {
			return fmt.Errorf("each performed pattern needs one track and at least one note")
		}
		seen[recording.Pattern] = true
		for _, note := range recording.Notes {
			if note.Tick < 0 || note.EndTick < note.Tick || note.EndTick > 1<<60 || note.Note < 0 || note.Note > 127 || note.Velocity < 1 || note.Velocity > 127 || note.Channel > 15 || note.NoteID == 65535 {
				return fmt.Errorf("performed note range or timing is invalid")
			}
			if len(note.Expressions) > 0 {
				return fmt.Errorf("played-timing patterns do not support per-note expression yet; the take is unchanged")
			}
			origin, end = min(origin, note.Tick), max(end, max(note.EndTick, note.Tick+1))
			count++
		}
	}
	if count > seq.MaxPerformanceNotes {
		return fmt.Errorf("a performance may contain at most 512 notes")
	}
	origin = origin / seq.TicksPerBar * seq.TicksPerBar
	length := ((end - origin + seq.TicksPerBar - 1) / seq.TicksPerBar) * seq.TicksPerBar
	if length > 1<<40 {
		return fmt.Errorf("performance span exceeds its tick range")
	}
	for _, recording := range recordings {
		performance := &seq.Performance{LengthTicks: length}
		for _, note := range recording.Notes {
			performance.Notes = append(performance.Notes, seq.PerformedNote{Tick: note.Tick - origin, EndTick: max(note.EndTick, note.Tick+1) - origin, Note: uint8(note.Note), Velocity: uint8(note.Velocity)})
		}
		if err := performance.Validate(); err != nil {
			return err
		}
		plan, err := ctx.CurrentPlan()
		if err != nil {
			return err
		}
		assigned := false
		for _, track := range plan.Tracks {
			if track.ID == recording.Track {
				for _, slot := range track.Slots {
					assigned = assigned || slot != nil && *slot == recording.Pattern
				}
			}
		}
		if !assigned {
			return fmt.Errorf("pattern %q is not on track %q", recording.Pattern, recording.Track)
		}
		if err := replacePerformedPattern(ctx, recording.Pattern, performance, false); err != nil {
			return err
		}
	}
	ctx.SetLabel(fmt.Sprintf("Performance kept · %d notes across %d patterns", count, len(recordings)))
	return nil
}

func replacePerformedPattern(ctx *Context, id string, performance *seq.Performance, preserveTranspose bool) error {
	score, diagnostics, err := ctx.ParseProject()
	if err != nil {
		return err
	}
	if score == nil || hasErrors(diagnostics) {
		return fmt.Errorf("score must validate before keeping a performance")
	}
	var authored *notation.Pattern
	for i := range score.Patterns {
		if score.Patterns[i].Name == id {
			authored = &score.Patterns[i]
			break
		}
	}
	if authored == nil {
		return fmt.Errorf("unknown performed pattern %q", id)
	}
	file := authored.Position.File
	if file == "" {
		file = ctx.Options.Path
	}
	for _, source := range ctx.Options.Sources {
		if source.Path == file && source.Library != "" {
			return fmt.Errorf("copy library pattern %q into the project before keeping a performance", id)
		}
	}
	// Pattern actions carry the entry score's revision. A different file needs
	// its own revision receipt; do not overwrite imported edits from a stale UI.
	if file != ctx.Options.Path {
		return fmt.Errorf("source position belongs to another source file; edit its owning file")
	}
	before, ok := ctx.FileSource(file)
	if !ok {
		return fmt.Errorf("authored source for pattern %q is unavailable", id)
	}
	at, err := Offset(before, file, authored.Position)
	if err != nil {
		return err
	}
	declarations, err := textDeclarations(before)
	if err != nil {
		return err
	}
	var target *textDeclaration
	for i := range declarations {
		if declarations[i].start == at && declarationMatches(declarations[i].kind, "pattern") {
			target = &declarations[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("performed pattern source no longer matches")
	}
	var replacement strings.Builder
	fmt.Fprintf(&replacement, "pattern %s performance length=%dticks", target.name, performance.LengthTicks)
	for _, attribute := range authored.Attrs {
		if attribute.Name == "slot" || attribute.Name == "seed" || preserveTranspose && attribute.Name == "transpose" {
			fmt.Fprintf(&replacement, " %s=%s", attribute.Name, attribute.Value)
		}
	}
	replacement.WriteString(" {\n")
	document, err := notation.ParseDocument([]byte(target.text))
	if err != nil {
		return err
	}
	// Retain every authored comment when replacing the grid representation.
	var comments func(*gts.Node)
	comments = func(node *gts.Node) {
		if document.Walker.Type(node) == "comment" {
			replacement.WriteString("  " + strings.TrimRight(document.Walker.Text(node), "\r\n") + "\n")
			return
		}
		for i := 0; i < node.NamedChildCount(); i++ {
			comments(node.NamedChild(i))
		}
	}
	comments(document.Root)
	for _, note := range performance.Notes {
		fmt.Fprintf(&replacement, "  note %d at=%dticks length=%dticks velocity=%d;\n", note.Note, note.Tick, note.EndTick-note.Tick, note.Velocity)
	}
	replacement.WriteString("}")
	text := strings.ReplaceAll(replacement.String(), "\n", Newline(before))
	after, err := ReplaceSpan(before, file, Span{target.start, target.end, text, file})
	if err != nil {
		return err
	}
	ctx.Source, ctx.plan = after, nil
	return nil
}
