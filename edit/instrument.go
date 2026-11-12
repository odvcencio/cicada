package edit

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"m31labs.dev/cicada/internal/controls"
	"m31labs.dev/cicada/internal/paramdefs"
	"m31labs.dev/cicada/kernel/limits"

	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/notation"
)

// instrumentParameterSource ports studio_instrument.go instrumentParameterSource.
func instrumentParameterSource(source []byte, targetFile string, score *notation.Score, trackID, parameterID, value string, reset bool) ([]byte, error) {
	var target *notation.Track
	var definition *notation.Instrument
	for i := range score.Tracks {
		if score.Tracks[i].Name == trackID {
			target = &score.Tracks[i]
			break
		}
	}
	if target != nil {
		for i := range score.Instruments {
			if score.Instruments[i].Name == target.Kind {
				definition = &score.Instruments[i]
				break
			}
		}
	}
	literal := ""
	if definition != nil {
		program, ds := instrument.Compile(*definition)
		declared := program != nil && !hasErrors(ds) && program.HasParameter(parameterID)
		// The settings every instrument graph takes (octave, glide, vibrato)
		// are catalog controls of the track, not graph parameters.
		control, found := trackControl(score, trackID, parameterID)
		if !declared && (!found || program == nil || hasErrors(ds)) {
			return nil, refuse("unknown_setting", "entity", "instrument %s does not declare parameter %q", definition.Name, parameterID)
		}
		if !reset {
			var err error
			if declared {
				literal, err = instrument.ParameterLiteral(program, parameterID, value)
				if err != nil {
					return nil, invalidValue("%s", err.Error())
				}
			} else if literal, err = controlLiteral(control, value); err != nil {
				return nil, err
			}
		}
	} else {
		if target == nil {
			return nil, fmt.Errorf("track %q has no authored instrument", trackID)
		}
		// Every control the catalog lists for the track's voice: built-in
		// voices, keyboard patches and the lanes of a declared kit.
		control, found := trackControl(score, trackID, parameterID)
		if !found {
			// Custom kits and sample bindings have their own source controls. A
			// registry drum address does not imply that those sources accept it.
			for _, kit := range score.Kits {
				if kit.Name == target.Kind {
					return nil, refuse("unknown_setting", "entity", "track %q uses a custom kit; edit its source settings in Score", trackID)
				}
			}
			return nil, refuse("unknown_setting", "entity", "track %s does not expose sound parameter %q", trackID, parameterID)
		}
		if !reset {
			var err error
			literal, err = controlLiteral(control, value)
			if err != nil {
				return nil, err
			}
		}
	}

	decl, walker, err := declaration(source, []string{"track_decl"}, trackID)
	if err != nil {
		return nil, err
	}
	for i := 0; i < decl.NamedChildCount(); i++ {
		parameter := decl.NamedChild(i)
		if walker.Type(parameter) == "mix_setting" && parameter.NamedChildCount() > 0 {
			parameter = parameter.NamedChild(0)
		}
		if walker.Type(parameter) != "param_decl" || walker.Text(walker.Field(parameter, "name")) != parameterID {
			continue
		}
		if reset {
			start, end := int(parameter.StartByte()), int(parameter.EndByte())
			text := source[start:end]
			if bytes.Contains(text, []byte("//")) || bytes.Contains(text, []byte("/*")) {
				return nil, fmt.Errorf("this override contains a comment; remove it in Score to preserve the annotation")
			}
			return ReplaceSpan(source, targetFile, Span{start, end, "", targetFile})
		}
		node := walker.Field(parameter, "value")
		return ReplaceSpan(source, targetFile, Span{int(node.StartByte()), int(node.EndByte()), literal, targetFile})
	}
	if reset {
		return bytes.Clone(source), nil
	}
	at := int(decl.StartByte()) + bytes.IndexByte(source[decl.StartByte():decl.EndByte()], '{') + 1
	return ReplaceSpan(source, targetFile, Span{at, at, " " + parameterID + " = " + literal + " ", targetFile})
}

// trackControl finds a control of the track's voice in the control catalog.
func trackControl(score *notation.Score, track, name string) (controls.TrackControl, bool) {
	control, _, found, err := controls.TrackControlNamed(score, track, name)
	return control, found && err == nil
}

// controlLiteral checks a value for a catalog control and returns the source
// literal to write. Plain numbers take the control's catalog unit. A value
// outside the range is refused with value_out_of_range and the range; a
// value of the wrong form with invalid_value.
func controlLiteral(control controls.TrackControl, value string) (string, error) {
	value = strings.TrimSpace(value)
	if number, err := strconv.ParseFloat(value, 64); err == nil && (control.Type == "number" || control.Type == "integer") {
		value = controls.Literal(control.Control, number)
	}
	if number, ok := controls.CatalogNumber(control.Control, value); ok && control.Min != nil && control.Max != nil {
		excluded := false
		for _, v := range control.Excluded {
			excluded = excluded || number == v
		}
		if (number < *control.Min || number > *control.Max || excluded) && !controls.AcceptsWord(control.Control, value) {
			message := paramdefs.RangeText(control.Descriptor(), control.Name, value, number < *control.Min)
			if excluded {
				message = fmt.Sprintf("%s %s is not accepted: %s", control.Name, value, control.Note)
			}
			return "", outOfRange(control.Name, value, *control.Min, *control.Max, control.Unit, message)
		}
	}
	if err := controls.Validate(control.Control, value); err != nil {
		return "", invalidValue("%s", err.Error())
	}
	return value, nil
}

// AddPreset inserts an authored instrument and its track in one edit.
//
// Example: {"kind":"addpreset","preset":"warm-pad","instrument":"pad","track":"pad"}
type AddPreset struct {
	Preset     string `json:"preset" cicada:"required"`     // a built-in patch, such as warm-pad
	Instrument string `json:"instrument" cicada:"required"` // the name of the new instrument declaration
	Track      string `json:"track" cicada:"required"`      // the name of the new track that plays it
}

func (AddPreset) Kind() string { return "addpreset" }
func init() {
	Register("addpreset", func() Intent { return &AddPreset{} })
	Handle("addpreset", func(ctx *Context, intent Intent) error {
		in := intent.(*AddPreset)
		score, ds, err := ctx.ParseProject()
		if err != nil {
			return err
		}
		if score == nil || hasErrors(ds) {
			return fmt.Errorf("score must validate before adding an instrument")
		}
		updated, err := addPresetSourceParsed(ctx.Source, ctx.Options.Path, in.Preset, in.Instrument, in.Track, score)
		if err != nil {
			return err
		}
		patch, _ := instrument.FindPatch(in.Preset)
		ctx.Source, ctx.plan = updated, nil
		ctx.SetLabel("Add " + patch.Name + " instrument and track")
		return nil
	})
}

func addPresetSourceParsed(source []byte, targetFile, presetID, instrumentName, trackName string, score *notation.Score) ([]byte, error) {
	patch, ok := instrument.FindPatch(presetID)
	if !ok {
		return nil, fmt.Errorf("choose a patch from the instrument library")
	}
	declaration, err := patch.Source(instrumentName)
	if err != nil {
		return nil, err
	}
	if !patternName.MatchString(trackName) {
		return nil, fmt.Errorf("choose a lowercase track name")
	}
	for _, existing := range score.Instruments {
		if existing.Name == instrumentName {
			return nil, fmt.Errorf("instrument %s already exists", instrumentName)
		}
	}
	for _, existing := range score.Samplers {
		if existing.Name == instrumentName {
			return nil, fmt.Errorf("sampler %s already exists", instrumentName)
		}
	}
	for _, existing := range score.Kits {
		if existing.Name == instrumentName {
			return nil, fmt.Errorf("kit %s already exists", instrumentName)
		}
	}
	for _, existing := range score.Tracks {
		if existing.Name == trackName {
			return nil, fmt.Errorf("track %s already exists", trackName)
		}
	}
	if len(score.Tracks) >= limits.MaxTracks {
		return nil, fmt.Errorf("a project supports at most %d tracks", limits.MaxTracks)
	}
	root, walker, err := notation.ParseTree(source)
	if err != nil {
		return nil, err
	}
	at := len(source)
	for i := 0; i < root.NamedChildCount(); i++ {
		node := root.NamedChild(i)
		if kind := walker.Type(node); kind == "scene_decl" || kind == "song_decl" {
			at = int(node.StartByte())
			break
		}
	}
	newline := "\n"
	if bytes.Contains(source, []byte("\r\n")) {
		newline = "\r\n"
	}
	text := strings.ReplaceAll(declaration+"\ntrack "+trackName+" "+instrumentName+" {}\n\n", "\n", newline)
	if at > 0 && source[at-1] != '\n' {
		text = newline + text
	}
	return ReplaceSpan(source, targetFile, Span{at, at, text, targetFile})
}
