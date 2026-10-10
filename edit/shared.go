package edit

import (
	"fmt"
	"strconv"
	"strings"

	"m31labs.dev/cicada/notation"
)

var patternTypes = []string{"acid_pattern", "note_pattern", "drum_pattern"}

// stepOrigin follows authored parts rather than the expanded tokens, whose
// source positions point back to a phrase shared by multiple uses.
func stepOrigin(score *notation.Score, pattern, lane string, index int) (phrase string, shared bool) {
	phrase, _, shared = stepUse(score, pattern, lane, index)
	return
}

func stepUse(score *notation.Score, pattern, lane string, index int) (phrase string, at notation.Position, shared bool) {
	if lane != "" || index < 0 {
		return
	}
	phrases := map[string]notation.Phrase{}
	for _, p := range score.Phrases {
		phrases[p.Name] = p
	}
	for _, p := range score.Patterns {
		if p.Name != pattern || p.Kind == "drums" {
			continue
		}
		offset := 0
		for _, part := range p.Parts {
			if part.Step != nil {
				if index == offset {
					return
				}
				offset++
				continue
			}
			if part.Use == nil {
				continue
			}
			count := part.Use.Repeat * len(phrases[part.Use.Name].Steps)
			if index >= offset && index < offset+count {
				return part.Use.Name, part.Use.Position, true
			}
			offset += count
		}
	}
	return
}

func resolveShared(ctx *Context, entity EntityID, policy string) ([]byte, error) {
	pattern, lane, index, err := stepTarget(entity)
	if err != nil {
		return nil, err
	}
	if policy == "pattern" {
		if err := patternPreflight(ctx); err != nil {
			return nil, err
		}
		return independentPatternSource(ctx, pattern)
	}
	score, ds := ctx.Parse()
	if score == nil || hasErrors(ds) {
		return ctx.Source, nil
	}
	phrase, at, shared := stepUse(score, pattern, lane, index)
	if !shared {
		return ctx.Source, nil
	}
	switch policy {
	case "definition":
		return ctx.Source, nil
	case "detach":
		return expandPatternUses(ctx, pattern, Offset(ctx.Source, at))
	default:
		return nil, &SharedPhraseError{Pattern: pattern, Phrase: phrase, Step: index}
	}
}

// Phrase-based patterns become locally editable without changing the shared
// phrase or another pattern. Only each `use` span is expanded; its surrounding
// comments and direct note spellings stay intact.
func independentPatternSource(ctx *Context, id string) ([]byte, error) {
	return expandPatternUses(ctx, id, -1)
}

// useStart is the byte offset of one use, or -1 to expand every use.
func expandPatternUses(ctx *Context, id string, useStart int) ([]byte, error) {
	source := ctx.Source
	score, diagnostics := ctx.Parse()
	if score == nil || hasErrors(diagnostics) {
		return nil, fmt.Errorf("score must validate before editing a pattern")
	}
	semantic, err := ctx.CurrentPlan()
	if semantic == nil || err != nil {
		return nil, fmt.Errorf("score must compile before editing a pattern")
	}
	node, walker, err := declaration(source, patternTypes, id)
	if err != nil {
		return nil, err
	}
	var pattern *Pattern
	for i := range semantic.Patterns {
		if semantic.Patterns[i].ID == id {
			pattern = &semantic.Patterns[i]
			break
		}
	}
	if pattern == nil {
		return nil, fmt.Errorf("unknown pattern %q", id)
	}
	phrases := map[string]notation.Phrase{}
	for _, phrase := range score.Phrases {
		phrases[phrase.Name] = phrase
	}
	var edits []Span
	index := 0
	previousUse := false
	for i := 0; i < node.NamedChildCount(); i++ {
		part := node.NamedChild(i)
		switch walker.Type(part) {
		case "acid_step":
			previousUse = false
			if walker.Text(part) != "|" {
				index++
			}
		case "phrase_use":
			phrase, ok := phrases[walker.Text(walker.Field(part, "name"))]
			if !ok {
				return nil, fmt.Errorf("unknown phrase")
			}
			repeat := 1
			if count := walker.Field(part, "repeat"); count != nil {
				repeat, err = strconv.Atoi(walker.Text(count))
				if err != nil || repeat < 1 || repeat > 64 {
					return nil, fmt.Errorf("invalid phrase repeat")
				}
			}
			if useStart >= 0 && int(part.StartByte()) != useStart {
				index += repeat * len(phrase.Steps)
				previousUse = true
				continue
			}
			if strings.Contains(walker.Text(part), "//") {
				return nil, fmt.Errorf("comments inside a phrase use need a source edit to preserve their attachment")
			}
			transpose := walker.Field(part, "transpose")
			transformed := transpose != nil && walker.Text(transpose) != "0"
			var tokens []string
			for range repeat {
				for _, token := range phrase.Steps {
					if index >= len(pattern.Data) {
						return nil, fmt.Errorf("phrase expansion no longer matches the pattern")
					}
					text := token.Text
					if step := pattern.Data[index]; transformed && step != nil && !step.Tie {
						text, err = transposedPhraseToken(token, step)
						if err != nil {
							return nil, err
						}
					}
					tokens = append(tokens, text)
					index++
				}
			}
			text := strings.Join(tokens, " ")
			// A rest beside a retained use can parse as a qualified name.
			// Put the separator inside the replaced use span so later edits
			// remain valid without changing neighboring source bytes.
			if useStart >= 0 && previousUse {
				text = "| " + text
			}
			previousUse = false
			edits = append(edits, Span{int(part.StartByte()), int(part.EndByte()), text})
		}
	}
	return PatchSpans(source, edits)
}

// Patch chord pitches in their authored spans so transposition retains every
// voice, internal comment, whitespace, and modifier. Scalar spelling stays
// identical to the legacy expansion.
func transposedPhraseToken(token notation.StepToken, step *Step) (string, error) {
	if len(step.Notes) == 0 {
		return sourcePitch(int(step.Note)) + noteSuffix(token.Text), nil
	}
	if len(token.ChordPitches) != len(step.Notes) {
		return "", fmt.Errorf("chord source no longer matches the projection")
	}
	patches := make([]Span, len(step.Notes))
	for i, pitch := range token.ChordPitches {
		patches[i] = Span{pitch.Start, pitch.End, sourcePitch(step.Notes[i])}
	}
	updated, err := PatchSpans([]byte(token.Text), patches)
	return string(updated), err
}

func noteSuffix(text string) string {
	if at := strings.IndexAny(text, "^~*?%"); at >= 0 {
		return text[at:]
	}
	return ""
}
