package edit

import (
	"bytes"
	"fmt"
	"strings"
)

// CreatePattern creates a grid for a track's instrument. An optional
// scene assignment is part of the same edit, validation, and Undo entry.
type CreatePattern struct {
	Name    string `json:"name"`
	Track   string `json:"track"`
	Steps   int    `json:"steps"`
	Scene   string `json:"scene,omitempty"`
	Starter string `json:"starter,omitempty"`
}

func (CreatePattern) Kind() string { return "createpattern" }

func init() {
	Register("createpattern", func() Intent { return &CreatePattern{} })
	Handle("createpattern", createPattern)
}

func createPattern(ctx *Context, intent Intent) error {
	in := intent.(*CreatePattern)
	if !patternName.MatchString(in.Name) || in.Name == "off" || in.Name == "keep" || in.Name == "stop" {
		return fmt.Errorf("choose a lowercase pattern name of at most 64 letters, digits, underscores, or hyphens; off, keep, and stop are scene controls")
	}
	if in.Steps < 1 || in.Steps > 64 {
		return fmt.Errorf("pattern length must be 1–64 steps")
	}
	score, ds, err := ctx.ParseProject()
	if err != nil {
		return err
	}
	if score == nil || hasErrors(ds) {
		return fmt.Errorf("score must validate before creating a pattern")
	}
	for _, p := range score.Patterns {
		if p.Name == in.Name {
			return fmt.Errorf("pattern %q already exists", in.Name)
		}
	}
	for _, clip := range score.Clips {
		if clip.Name == in.Name {
			return fmt.Errorf("clip %q already exists; choose another pattern name", in.Name)
		}
	}
	// Scene bindings use local declarations, like BindScene. Never interpret
	// another file's source positions as offsets into this entry file.
	if _, _, err := declaration(ctx.Source, []string{"track_decl"}, in.Track); err != nil {
		return err
	}
	plan, err := ctx.CurrentPlan()
	if err != nil {
		return err
	}
	kind := ""
	instrumentKind := ""
	var lanes []string
	for _, track := range plan.Tracks {
		if track.ID != in.Track {
			continue
		}
		instrumentKind = track.Kind
		switch track.Kind {
		case "audio":
			return fmt.Errorf("audio tracks use clips; choose an instrument or drum track")
		case "acid":
			kind = "acid"
		case "drums":
			kind, lanes = "drums", []string{"bd", "sd", "ch"}
		default:
			kind = "notes"
			for _, kit := range score.Kits {
				if kit.Name == track.Kind {
					kind = "drums"
					for _, binding := range kit.Bindings {
						lanes = append(lanes, binding.Lane)
					}
				}
			}
		}
		break
	}
	if kind == "" {
		return fmt.Errorf("choose an instrument or drum track")
	}
	if in.Starter == "chords" {
		for _, instrument := range score.Instruments {
			if instrument.Name == instrumentKind && instrument.Mode == "mono" {
				return fmt.Errorf("held chords need a polyphonic instrument; choose a pulse or arpeggio for this sound")
			}
		}
	}
	rows, err := patternStarter(in.Starter, kind, lanes, in.Steps)
	if err != nil {
		return err
	}
	newline := "\n"
	if bytes.Contains(ctx.Source, []byte("\r\n")) {
		newline = "\r\n"
	}
	var body strings.Builder
	fmt.Fprintf(&body, "pattern %s %s steps=%d {%s", in.Name, kind, in.Steps, newline)
	if kind == "drums" {
		for _, lane := range lanes {
			fmt.Fprintf(&body, "  %s: %s%s", lane, rows[lane], newline)
		}
	} else {
		fmt.Fprintf(&body, "  %s%s", rows[""], newline)
	}
	body.WriteString("}" + newline)
	// Appending leaves all existing declarations and their comments byte-for-
	// byte intact. Cicada declarations may appear after the song or arrangement.
	updated := append(bytes.Clone(ctx.Source), []byte(newline+newline+body.String())...)
	if in.Scene != "" {
		updated, err = bindPatternSource(sourceContext(ctx, updated), in.Scene, in.Track, in.Name)
		if err != nil {
			return err
		}
	}
	ctx.Source, ctx.plan = updated, nil
	ctx.SetLabel(fmt.Sprintf("Pattern · %s created for %s", in.Name, in.Track))
	return nil
}
