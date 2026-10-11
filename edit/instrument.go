package edit

import (
	"bytes"
	"fmt"
	"strings"

	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/notation"
)

// instrumentParameterSource ports studio_instrument.go instrumentParameterSource.
func instrumentParameterSource(source []byte, targetFile string, score *notation.Score, trackID, parameterID, value string, reset bool) ([]byte, error) {
	var definition *notation.Instrument
	for _, track := range score.Tracks {
		if track.Name != trackID {
			continue
		}
		for i := range score.Instruments {
			if score.Instruments[i].Name == track.Kind {
				definition = &score.Instruments[i]
				break
			}
		}
		break
	}
	if definition == nil {
		return nil, fmt.Errorf("track %q has no authored instrument", trackID)
	}
	program, ds := instrument.Compile(*definition)
	if program == nil || hasErrors(ds) || !program.HasParameter(parameterID) {
		return nil, fmt.Errorf("instrument %s does not declare parameter %q", definition.Name, parameterID)
	}
	literal := ""
	if !reset {
		var err error
		literal, err = instrument.ParameterLiteral(program, parameterID, value)
		if err != nil {
			return nil, err
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
	if len(score.Tracks) >= 16 {
		return nil, fmt.Errorf("a project supports at most 16 tracks")
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
