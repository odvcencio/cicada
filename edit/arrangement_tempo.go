package edit

import (
	"fmt"
	"math"
	"strconv"

	gts "github.com/odvcencio/gotreesitter"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
)

// SetArrangementTempo adds a bar-boundary tempo step. OldAt selects an
// existing step to change or move; moving onto another step is refused.
type SetArrangementTempo struct {
	OldAt string `json:"oldat,omitempty"`
	At    string `json:"at"`
	Value string `json:"value"`
}

func (SetArrangementTempo) Kind() string { return "setarrangementtempo" }

type RemoveArrangementTempo struct {
	At string `json:"at"`
}

func (RemoveArrangementTempo) Kind() string { return "removearrangementtempo" }

func init() {
	Register("setarrangementtempo", func() Intent { return &SetArrangementTempo{} })
	Register("removearrangementtempo", func() Intent { return &RemoveArrangementTempo{} })
	Handle("setarrangementtempo", editArrangementTempo)
	Handle("removearrangementtempo", editArrangementTempo)
}

func tempoBarTick(at string) (int64, error) {
	tick, err := notation.MusicalTicks(at, true)
	if err != nil || tick < 0 || tick%seq.TicksPerBar != 0 || tick/seq.TicksPerBar > 1<<20 {
		return 0, fmt.Errorf("choose a tempo step at the start of a bar")
	}
	return tick, nil
}

func editArrangementTempo(ctx *Context, intent Intent) error {
	if ctx.Options.CaptureGuard != nil {
		if err := ctx.Options.CaptureGuard(); err != nil {
			return err
		}
	}
	var oldAt, at, value string
	remove := false
	switch in := intent.(type) {
	case *SetArrangementTempo:
		oldAt, at, value = in.OldAt, in.At, in.Value
		bpm, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(bpm) || math.IsInf(bpm, 0) || bpm < 20 || bpm > 300 {
			return fmt.Errorf("choose a tempo from 20 to 300 BPM")
		}
		value = strconv.FormatFloat(math.Round(bpm*1000)/1000, 'f', -1, 64)
	case *RemoveArrangementTempo:
		oldAt, at, remove = in.At, in.At, true
	}
	tick, err := tempoBarTick(at)
	if err != nil {
		return err
	}
	at = notation.TickPosition(tick)
	oldTick := int64(-1)
	if oldAt != "" {
		oldTick, err = tempoBarTick(oldAt)
		if err != nil {
			return err
		}
	}
	source, file, err := arrangementFile(ctx)
	if err != nil {
		return err
	}
	root, walker, err := notation.ParseTree(source)
	if err != nil {
		return err
	}
	if root.HasError() {
		return fmt.Errorf("fix score syntax before editing tempo steps")
	}
	var arrangement, selected, next *gts.Node
	var before, after int64 = -1, 1<<63 - 1
	for i := 0; i < root.NamedChildCount(); i++ {
		a := root.NamedChild(i)
		if walker.Type(a) != "arrange_decl" {
			continue
		}
		arrangement = a
		for j := 0; j < a.NamedChildCount(); j++ {
			n := a.NamedChild(j)
			if walker.Type(n) != "arrangement_tempo" {
				continue
			}
			t, err := tempoBarTick(walker.Text(walker.Field(n, "at")))
			if err != nil {
				return err
			}
			if t == oldTick {
				selected = n
				continue
			}
			if !remove && t == tick {
				return fmt.Errorf("bar %d already has a tempo step; edit that step instead", tick/seq.TicksPerBar+1)
			}
			if t < oldTick {
				before = max(before, t)
			} else if t > oldTick {
				after = min(after, t)
			}
			if t > tick && next == nil {
				next = n
			}
		}
	}
	if arrangement == nil {
		return fmt.Errorf("tempo steps need an arrange block")
	}
	if oldAt != "" && selected == nil {
		return fmt.Errorf("the tempo step at %s no longer exists; refresh and try again", oldAt)
	}
	var spans []Span
	if remove {
		spans = append(spans, Span{int(selected.StartByte()), int(selected.EndByte()), "", file})
	} else if selected != nil && tick > before && tick < after {
		for key, text := range map[string]string{"at": at, "value": value} {
			n := walker.Field(selected, key)
			spans = append(spans, Span{int(n.StartByte()), int(n.EndByte()), text, file})
		}
	} else {
		if selected != nil {
			spans = append(spans, Span{int(selected.StartByte()), int(selected.EndByte()), "", file})
		}
		insert := closeBraceLineStart(source, int(arrangement.StartByte()), int(arrangement.EndByte()))
		if next != nil {
			insert = attachedCommentStart(source, int(next.StartByte()), int(arrangement.StartByte()))
		}
		nl := Newline(source)
		prefix := ""
		if insert > 0 && source[insert-1] != '\n' {
			prefix = nl
		}
		spans = append(spans, Span{insert, insert, prefix + "  tempo " + at + " " + value + nl, file})
	}
	updated, err := PatchSpans(source, file, spans)
	if err != nil {
		return err
	}
	ctx.Source, ctx.plan = stageProjectSource(ctx, file, source, updated), nil
	label := fmt.Sprintf("Tempo · bar %d · %s BPM", tick/seq.TicksPerBar+1, value)
	if remove {
		label = fmt.Sprintf("Tempo · bar %d step removed", tick/seq.TicksPerBar+1)
	}
	ctx.SetLabel(label)
	return nil
}
