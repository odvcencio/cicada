package edit

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

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
		if program == nil || hasErrors(ds) || !program.HasParameter(parameterID) {
			return nil, fmt.Errorf("instrument %s does not declare parameter %q", definition.Name, parameterID)
		}
		if !reset {
			var err error
			literal, err = instrument.ParameterLiteral(program, parameterID, value)
			if err != nil {
				return nil, err
			}
		}
	} else {
		if target == nil {
			return nil, fmt.Errorf("track %q has no authored instrument", trackID)
		}
		// Custom kits and sample bindings have their own source controls. A
		// registry drum address does not imply that those sources accept it.
		for _, kit := range score.Kits {
			if kit.Name == target.Kind {
				return nil, fmt.Errorf("track %q uses a custom kit; edit its source settings in Score", trackID)
			}
		}
		descriptor, ok := paramdefs.TrackParam(notation.ParameterVoiceKind(score, target.Kind), parameterID)
		if !ok {
			return nil, fmt.Errorf("track %s does not expose sound parameter %q", trackID, parameterID)
		}
		if !reset {
			var err error
			literal, err = builtinParameterLiteral(descriptor, value)
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

// Built-in controls use the source catalog's units and range. Plain numbers
// take the control's displayed unit, while explicit compatible literals keep
// their original precision and spelling.
func builtinParameterLiteral(descriptor paramdefs.Descriptor, value string) (string, error) {
	value = strings.TrimSpace(value)
	if number, err := strconv.ParseFloat(value, 64); err == nil && descriptor.Curve != "enum" && descriptor.Curve != "toggle" {
		value = paramdefs.FormatValue(descriptor, number)
	}
	validation := descriptor
	// Cents, like semitones and ratios, are plain source numbers.
	if validation.Unit == "cent" {
		validation.Unit = ""
	}
	if descriptor.Type == "integer" && descriptor.Curve != "enum" && descriptor.Curve != "toggle" {
		validation.Curve = "integer"
	}
	if _, err := paramdefs.ValidateLiteral(validation, value); err != nil {
		return "", err
	}
	return value, nil
}

// AddPreset inserts an authored instrument and its track in one edit.
type AddPreset struct {
	Preset     string `json:"preset"`
	Instrument string `json:"instrument"`
	Track      string `json:"track"`
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
