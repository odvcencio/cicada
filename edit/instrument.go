package edit

import (
	"bytes"
	"fmt"

	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/notation"
)

// instrumentParameterSource ports studio_instrument.go instrumentParameterSource.
func instrumentParameterSource(source []byte, score *notation.Score, trackID, parameterID, value string, reset bool) ([]byte, error) {
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
			return ReplaceSpan(source, start, end, nil)
		}
		node := walker.Field(parameter, "value")
		return ReplaceSpan(source, int(node.StartByte()), int(node.EndByte()), []byte(literal))
	}
	if reset {
		return bytes.Clone(source), nil
	}
	at := int(decl.StartByte()) + bytes.IndexByte(source[decl.StartByte():decl.EndByte()], '{') + 1
	return ReplaceSpan(source, at, at, []byte(" "+parameterID+" = "+literal+" "))
}
