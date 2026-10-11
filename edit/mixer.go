package edit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/taproot/walk"
	"m31labs.dev/cicada/internal/paramdefs"
	"m31labs.dev/cicada/notation"
)

// LineRange is a 1-based inclusive line span in the score.
type LineRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

type mixerSendValue struct {
	Level  json.RawMessage `json:"level"`
	Pre    *bool           `json:"pre,omitempty"`
	Remove bool            `json:"remove,omitempty"`
}

type mixerFXValue struct {
	Kind     string `json:"kind"`
	InsertOn string `json:"insertOn,omitempty"`
}

// mixerSource writes one mixer gesture into the smallest CST span. It
// returns the prior and canonical new value for the session history entry.
func mixerSource(source []byte, path string, raw json.RawMessage) ([]byte, string, string, LineRange, error) {
	if path == "" || len(raw) == 0 {
		return nil, "", "", LineRange{}, fmt.Errorf("mixer path and value are required")
	}
	score, diagnostics := notation.Parse(source)
	if score == nil || hasErrors(diagnostics) {
		return nil, "", "", LineRange{}, fmt.Errorf("score must validate before a mixer edit")
	}
	root, walker, err := notation.ParseTree(source)
	if err != nil {
		return nil, "", "", LineRange{}, err
	}
	if strings.HasPrefix(path, "fx.") && len(strings.Split(path, ".")) == 2 {
		name := strings.TrimPrefix(path, "fx.")
		request, err := requestedFX(raw)
		if err != nil {
			return nil, "", "", LineRange{}, err
		}
		kind := request.Kind
		if !ValidMixerIdentifier(name) {
			return nil, "", "", LineRange{}, fmt.Errorf("invalid effect name %q", name)
		}
		for _, effect := range score.Effects {
			if effect.Name == name {
				return nil, "", "", LineRange{}, fmt.Errorf("effect %s already exists", name)
			}
		}
		if !knownMixerEffectKind(kind) {
			return nil, "", "", LineRange{}, fmt.Errorf("unknown effect kind %q", kind)
		}
		updated := insertTopLevelFX(source, root, walker, name, kind)
		line := lineNumberAt(updated, bytes.Index(updated, []byte("fx "+name+" ")))
		changed := LineRange{Start: line, End: line}
		if request.InsertOn != "" {
			if request.InsertOn != "music" || kind != "comp" {
				return nil, "", "", LineRange{}, unsupportedMixerRoute("a compressor can only be inserted on bus music")
			}
			placed, _, _, insertedRange, insertErr := mixerSource(updated, "music.insert", json.RawMessage(strconv.Quote(name)))
			if insertErr != nil {
				return nil, "", "", LineRange{}, insertErr
			}
			updated = placed
			changed.Start = min(changed.Start, insertedRange.Start)
			changed.End = max(changed.End, insertedRange.End)
		}
		return updated, "absent", kind, changed, nil
	}

	parts := strings.Split(path, ".")
	if len(parts) < 2 {
		return nil, "", "", LineRange{}, fmt.Errorf("mixer path %q must name an owner and field", path)
	}
	owner, field := parts[0], strings.Join(parts[1:], ".")
	var decl *gts.Node
	declKind := ""
	for i := 0; i < root.NamedChildCount(); i++ {
		node := root.NamedChild(i)
		kind := walker.Type(node)
		name := ""
		switch kind {
		case "track_decl", "bus_decl", "fx_decl":
			name = walker.Text(walker.Field(node, "name"))
		case "master_decl":
			name = "master"
		}
		if name == owner {
			decl, declKind = node, kind
			break
		}
	}
	if decl == nil && (owner == "master" || owner == "music" || owner == "sfx") {
		want := "master_decl"
		if owner != "master" {
			want = "bus_decl"
		}
		return addMixerBlock(source, root, walker, owner, want, field, raw, score)
	}
	if decl == nil {
		return nil, "", "", LineRange{}, fmt.Errorf("unknown mixer owner %q", owner)
	}
	if declKind == "fx_decl" {
		resolved, diagnostics := notation.ResolvePresets(score)
		if hasErrors(diagnostics) {
			return nil, "", "", LineRange{}, fmt.Errorf("score must validate before a mixer edit")
		}
		return editFXField(source, walker, decl, owner, field, raw, resolved)
	}
	if field == "send" || strings.HasPrefix(field, "send.") {
		if declKind != "track_decl" || len(parts) != 3 || parts[1] != "send" {
			return nil, "", "", LineRange{}, fmt.Errorf("send path must be <track>.send.<effect>")
		}
		return editMixerSend(source, walker, decl, owner, parts[2], raw, score)
	}
	if field != "level" && field != "pan" && field != "mute" && field != "solo" && field != "insert" && field != "out" {
		return nil, "", "", LineRange{}, fmt.Errorf("unsupported mixer field %q", field)
	}
	return editMixerSetting(source, walker, decl, declKind, owner, field, raw, score)
}

func editFXField(source []byte, walker *walk.Walker, decl *gts.Node, owner, field string, raw json.RawMessage, score *notation.Score) ([]byte, string, string, LineRange, error) {
	kind := walker.Text(walker.Field(decl, "kind"))
	if kind == "" {
		kind = owner
	}
	for _, effect := range score.Effects {
		if effect.Name == owner {
			kind = effect.Kind
			break
		}
	}
	descriptor, ok := mixerFXDescriptor(kind, field)
	if !ok {
		return nil, "", "", LineRange{}, fmt.Errorf("unknown %s setting %q", kind, field)
	}
	literal, err := CanonicalMixerLiteral(descriptor, raw)
	if err != nil {
		return nil, "", "", LineRange{}, err
	}
	var found *gts.Node
	for i := 0; i < decl.NamedChildCount(); i++ {
		child := decl.NamedChild(i)
		if walker.Type(child) == "param_decl" && walker.Text(walker.Field(child, "name")) == descriptor.Source {
			found = child
			break
		}
	}
	if found != nil {
		value := walker.Field(found, "value")
		old := walker.Text(value)
		start, end := int(value.StartByte()), int(value.EndByte())
		updated := replaceMixerSpan(source, start, end, []byte(literal))
		return updated, old, literal, SourceRangeAt(updated, start, start+len(literal)), nil
	}
	updated := insertMixerSetting(source, walker, decl, "fx_decl", descriptor.Source, literal, descriptorOrder(kind, descriptor.Source))
	line := lineNumberAt(updated, findInsertedValue(updated, descriptor.Source, literal, int(decl.StartByte())))
	return updated, "absent", literal, LineRange{Start: line, End: line}, nil
}

func editMixerSetting(source []byte, walker *walk.Walker, decl *gts.Node, declKind, owner, field string, raw json.RawMessage, score *notation.Score) ([]byte, string, string, LineRange, error) {
	if field == "level" {
		step := mixerDisplayStep(declKind, field, .01)
		literal, isOff, err := mixerLevelLiteral(raw, step)
		if err != nil {
			return nil, "", "", LineRange{}, err
		}
		if isOff {
			before := mixerOwnerLevel(score, declKind, owner, step)
			if declKind == "track_decl" {
				updated, _, _, changed, switchErr := setMixerSwitch(source, walker, decl, declKind, "mute", json.RawMessage(`true`), score)
				return updated, before, "off", changed, switchErr
			}
			withMute, _, _, muteRange, muteErr := mixerSource(source, owner+".mute", json.RawMessage(`true`))
			if muteErr != nil {
				return nil, "", "", LineRange{}, muteErr
			}
			updated, _, _, levelRange, editErr := editMixerValueUnchecked(withMute, owner, declKind, field, "off")
			if editErr != nil {
				return nil, "", "", LineRange{}, editErr
			}
			levelRange.Start = min(levelRange.Start, muteRange.Start)
			levelRange.End = max(levelRange.End, muteRange.End)
			return updated, before, "off", levelRange, nil
		}
		updated, before, after, changed, editErr := editOneMixerSetting(source, walker, decl, declKind, field, literal)
		params := mixerOwnerParams(score, declKind, owner)
		// Moving the fader up from Off wakes the strip: Off is saved as mute on a track, and as
		// level off plus mute on a bus or the master. Clear the saved mute with the level write.
		mutedTrack := declKind == "track_decl" && (TrackMixerSourceText(params, "mute") == "on" || TrackMixerSourceText(params, "mute") == "true")
		if editErr != nil || (TrackMixerSourceText(params, "level") != "off" && !mutedTrack) {
			return updated, before, after, changed, editErr
		}
		withMute, _, _, muteRange, muteErr := mixerSource(updated, owner+".mute", json.RawMessage(`false`))
		if muteErr != nil {
			return nil, "", "", LineRange{}, muteErr
		}
		changed.Start = min(changed.Start, muteRange.Start)
		changed.End = max(changed.End, muteRange.End)
		return withMute, before, after, changed, nil
	}
	if field == "pan" {
		literal, err := mixerNumericLiteral(raw, -1, 1, mixerDisplayStep(declKind, field, .01), "")
		if err != nil {
			return nil, "", "", LineRange{}, err
		}
		return editOneMixerSetting(source, walker, decl, declKind, field, literal)
	}
	if field == "mute" || field == "solo" {
		return setMixerSwitch(source, walker, decl, declKind, field, raw, score)
	}
	if field == "insert" {
		names, err := insertNames(raw)
		if err != nil {
			return nil, "", "", LineRange{}, err
		}
		literal := "none"
		if len(names) > 0 {
			literal = strings.Join(names, " -> ")
		}
		return editOneMixerSetting(source, walker, decl, declKind, field, literal)
	}
	if field == "out" {
		value, err := jsonStringOrNumber(raw)
		if err != nil {
			return nil, "", "", LineRange{}, fmt.Errorf("out must be a bus name")
		}
		return editOneMixerSetting(source, walker, decl, declKind, field, value)
	}
	return nil, "", "", LineRange{}, fmt.Errorf("unsupported mixer field %q", field)
}

func editMixerValueUnchecked(source []byte, owner, wantKind, field, literal string) ([]byte, string, string, LineRange, error) {
	root, walker, err := notation.ParseTree(source)
	if err != nil {
		return nil, "", "", LineRange{}, err
	}
	for i := 0; i < root.NamedChildCount(); i++ {
		decl := root.NamedChild(i)
		kind := walker.Type(decl)
		if kind != wantKind {
			continue
		}
		name := ""
		if kind == "master_decl" {
			name = "master"
		} else {
			name = walker.Text(walker.Field(decl, "name"))
		}
		if name == owner {
			return editOneMixerSetting(source, walker, decl, kind, field, literal)
		}
	}
	return nil, "", "", LineRange{}, fmt.Errorf("unknown mixer owner %q", owner)
}

func mixerOwnerLevel(score *notation.Score, declKind, owner string, step float64) string {
	params := mixerOwnerParams(score, declKind, owner)
	if value := TrackMixerSourceText(params, "level"); value != "" {
		return spacedMixerValue(value)
	}
	defaultValue := -6.0
	if declKind == "bus_decl" {
		if owner == "music" {
			defaultValue = -3
		} else {
			defaultValue = 0
		}
	} else if declKind == "master_decl" {
		defaultValue = 0
	}
	return formatMixerNumber(roundMixer(defaultValue, step)) + " dB"
}

func mixerOwnerParams(score *notation.Score, declKind, owner string) []notation.Param {
	switch declKind {
	case "track_decl":
		return TrackParams(score, owner)
	case "bus_decl":
		return BusParams(score, owner)
	case "master_decl":
		return score.Master
	default:
		return nil
	}
}

func setMixerSwitch(source []byte, walker *walk.Walker, decl *gts.Node, declKind, field string, raw json.RawMessage, _ *notation.Score) ([]byte, string, string, LineRange, error) {
	value, err := jsonBool(raw)
	if err != nil {
		return nil, "", "", LineRange{}, err
	}
	literal := "off"
	if value {
		literal = "on"
	}
	return editOneMixerSetting(source, walker, decl, declKind, field, literal)
}

func editOneMixerSetting(source []byte, walker *walk.Walker, decl *gts.Node, declKind, field, literal string) ([]byte, string, string, LineRange, error) {
	var found *gts.Node
	for i := 0; i < decl.NamedChildCount(); i++ {
		setting := decl.NamedChild(i)
		var param *gts.Node
		if declKind == "fx_decl" {
			if walker.Type(setting) == "param_decl" {
				param = setting
			}
		} else if walker.Type(setting) == "mix_setting" && setting.NamedChildCount() > 0 && walker.Type(setting.NamedChild(0)) == "param_decl" {
			param = setting.NamedChild(0)
		}
		if param != nil && walker.Text(walker.Field(param, "name")) == field {
			found = param
			break
		}
	}
	if found != nil {
		value := walker.Field(found, "value")
		old := walker.Text(value)
		start, end := int(value.StartByte()), int(value.EndByte())
		updated := replaceMixerSpan(source, start, end, []byte(literal))
		return updated, old, literal, SourceRangeAt(updated, start, start+len(literal)), nil
	}
	updated := insertMixerSetting(source, walker, decl, declKind, field, literal, mixerFieldOrder(field))
	line := lineNumberAt(updated, findInsertedValue(updated, field, literal, int(decl.StartByte())))
	return updated, "absent", literal, LineRange{Start: line, End: line}, nil
}

func editMixerSend(source []byte, walker *walk.Walker, decl *gts.Node, owner, target string, raw json.RawMessage, score *notation.Score) ([]byte, string, string, LineRange, error) {
	if !ValidMixerIdentifier(target) {
		return nil, "", "", LineRange{}, fmt.Errorf("invalid send destination %q", target)
	}
	effect := FindNotationEffect(score, target)
	if effect == nil && target != "music" && target != "sfx" {
		return nil, "", "", LineRange{}, fmt.Errorf("send references unknown effect %s", target)
	}
	var request mixerSendValue
	if len(raw) > 0 && raw[0] == '{' {
		if err := json.Unmarshal(raw, &request); err != nil {
			return nil, "", "", LineRange{}, fmt.Errorf("invalid send value: %w", err)
		}
		if request.Remove {
			return removeMixerSend(source, walker, decl, target)
		}
	} else {
		request.Level = bytes.Clone(raw)
	}
	if len(request.Level) == 0 {
		return nil, "", "", LineRange{}, fmt.Errorf("send value needs a level")
	}
	step := .01
	if effect != nil {
		parameter := "mix.send_a"
		if effect.Kind == "reverb" {
			parameter = "mix.send_b"
		}
		if descriptor, found := paramdefs.Lookup(parameter); found {
			step = descriptor.DisplayStep
		}
	}
	literal, err := canonicalSendLiteral(request.Level, step)
	if err != nil {
		return nil, "", "", LineRange{}, err
	}
	var found *gts.Node
	var priorLevel, priorPre string
	for i := 0; i < decl.NamedChildCount(); i++ {
		setting := decl.NamedChild(i)
		if walker.Type(setting) != "mix_setting" || setting.NamedChildCount() == 0 {
			continue
		}
		send := setting.NamedChild(0)
		if walker.Type(send) == "send_decl" && walker.Text(walker.Field(send, "to")) == target {
			found = send
			priorLevel = walker.Text(walker.Field(send, "level"))
			if walker.Field(send, "tap") != nil {
				priorPre = " pre"
			}
			break
		}
	}
	pre := request.Pre != nil && *request.Pre
	if request.Pre == nil && priorPre != "" {
		pre = true
	}
	newLiteral := "send " + target + " = " + literal
	if pre {
		newLiteral += " pre"
	}
	if found != nil {
		start, end := int(found.StartByte()), int(found.EndByte())
		old := "send " + target + " = " + priorLevel + priorPre
		updated := replaceMixerSpan(source, start, end, []byte(newLiteral))
		return updated, old, newLiteral, SourceRangeAt(updated, start, start+len(newLiteral)), nil
	}
	// A named send is ordered by destination after the ordinary mixer fields.
	updated := insertMixerSend(source, walker, decl, target, newLiteral)
	line := lineNumberAt(updated, bytes.Index(updated, []byte(newLiteral)))
	return updated, "absent", newLiteral, LineRange{Start: line, End: line}, nil
}

func removeMixerSend(source []byte, walker *walk.Walker, decl *gts.Node, target string) ([]byte, string, string, LineRange, error) {
	for i := 0; i < decl.NamedChildCount(); i++ {
		setting := decl.NamedChild(i)
		if walker.Type(setting) != "mix_setting" || setting.NamedChildCount() == 0 {
			continue
		}
		send := setting.NamedChild(0)
		if walker.Type(send) != "send_decl" || walker.Text(walker.Field(send, "to")) != target {
			continue
		}
		start, end := int(send.StartByte()), int(send.EndByte())
		old := string(bytes.TrimSpace(source[start:end]))
		lineStart := bytes.LastIndexByte(source[:start], '\n') + 1
		lineEnd := end
		for lineEnd < len(source) && source[lineEnd] != '\n' {
			lineEnd++
		}
		if len(bytes.TrimSpace(source[lineStart:start])) == 0 && len(bytes.TrimSpace(source[end:lineEnd])) == 0 {
			if lineEnd < len(source) {
				lineEnd++
			}
			start = lineStart
			end = lineEnd
		}
		updated := replaceMixerSpan(source, start, end, nil)
		line := max(1, lineNumberAt(updated, max(0, start-1)))
		return updated, old, "removed", LineRange{Start: line, End: line}, nil
	}
	return bytes.Clone(source), "absent", "absent", LineRange{}, nil
}

func insertMixerSetting(source []byte, walker *walk.Walker, decl *gts.Node, declKind, field, literal string, order int) []byte {
	var insertAt int
	for i := 0; i < decl.NamedChildCount(); i++ {
		child := decl.NamedChild(i)
		name := ""
		candidate := child
		if declKind != "fx_decl" {
			if walker.Type(child) != "mix_setting" || child.NamedChildCount() == 0 {
				continue
			}
			candidate = child.NamedChild(0)
			if walker.Type(candidate) == "send_decl" {
				if order < mixerFieldOrder("send") {
					insertAt = attachedCommentStart(source, int(child.StartByte()), int(decl.StartByte()))
					break
				}
				continue
			}
			if walker.Type(candidate) != "param_decl" {
				continue
			}
		}
		if declKind == "fx_decl" && walker.Type(child) != "param_decl" {
			continue
		}
		name = walker.Text(walker.Field(candidate, "name"))
		candidateOrder := mixerFieldOrder(name)
		if declKind == "fx_decl" {
			kind := walker.Text(walker.Field(decl, "kind"))
			if kind == "" {
				kind = walker.Text(walker.Field(decl, "name"))
			}
			candidateOrder = descriptorOrder(kind, name)
		}
		if candidateOrder > order {
			insertAt = attachedCommentStart(source, int(child.StartByte()), int(decl.StartByte()))
			break
		}
	}
	if insertAt == 0 {
		insertAt = closeBraceLineStart(source, int(decl.StartByte()), int(decl.EndByte()))
	}
	line := "  " + field + " = " + literal + "\n"
	if insertAt == int(decl.EndByte())-1 { // Empty or inline closing brace.
		if insertAt > 0 && source[insertAt-1] != '\n' {
			line = "\n" + line
		}
		line = strings.TrimSuffix(line, "\n") + "\n"
	}
	return replaceMixerSpan(source, insertAt, insertAt, []byte(line))
}

func insertMixerSend(source []byte, walker *walk.Walker, decl *gts.Node, target, literal string) []byte {
	var insertAt int
	for i := 0; i < decl.NamedChildCount(); i++ {
		child := decl.NamedChild(i)
		if walker.Type(child) != "mix_setting" || child.NamedChildCount() == 0 {
			continue
		}
		content := child.NamedChild(0)
		if walker.Type(content) == "param_decl" && walker.Text(walker.Field(content, "name")) == "out" ||
			walker.Type(content) == "send_decl" && walker.Text(walker.Field(content, "to")) > target {
			insertAt = attachedCommentStart(source, int(child.StartByte()), int(decl.StartByte()))
			break
		}
	}
	if insertAt == 0 {
		insertAt = closeBraceLineStart(source, int(decl.StartByte()), int(decl.EndByte()))
	}
	line := "  " + literal + "\n"
	if insertAt == int(decl.EndByte())-1 && insertAt > 0 && source[insertAt-1] != '\n' {
		line = "\n" + line
	}
	return replaceMixerSpan(source, insertAt, insertAt, []byte(line))
}

func closeBraceLineStart(source []byte, start, end int) int {
	close := end - 1
	if close < start || close >= len(source) {
		return close
	}
	lineStart := bytes.LastIndexByte(source[:close], '\n') + 1
	if len(bytes.TrimSpace(source[lineStart:close])) == 0 {
		return lineStart
	}
	return close
}

func attachedCommentStart(source []byte, at, lowerBound int) int {
	lineStart := bytes.LastIndexByte(source[:at], '\n') + 1
	// An inline setting shares its line with a declaration or another
	// setting. Its line start is outside the insertion span; keep the new
	// setting inside the owner's braces instead of moving before the owner.
	if len(bytes.TrimSpace(source[lineStart:at])) > 0 {
		return at
	}
	for lineStart > lowerBound {
		previousEnd := lineStart - 1
		previousStart := bytes.LastIndexByte(source[:previousEnd], '\n') + 1
		line := bytes.TrimSpace(source[previousStart:previousEnd])
		if !bytes.HasPrefix(line, []byte("//")) {
			break
		}
		lineStart = previousStart
	}
	return lineStart
}

func canonicalSendLiteral(raw json.RawMessage, displayStep ...float64) (string, error) {
	step := .01
	if len(displayStep) > 0 && displayStep[0] > 0 {
		step = displayStep[0]
	}
	value, err := jsonStringOrNumber(raw)
	if err != nil {
		return "", fmt.Errorf("send level must be a unitless gain or dB")
	}
	if strings.HasSuffix(strings.ToLower(value), "db") {
		n, parseErr := strconv.ParseFloat(value[:len(value)-2], 64)
		if parseErr != nil || !finiteMixer(n) || n < -60 || n > 0 {
			return "", fmt.Errorf("send level must be between -60 and 0 dB")
		}
		n = roundMixer(n, step)
		return formatMixerNumber(n) + "dB", nil
	}
	n, parseErr := strconv.ParseFloat(value, 64)
	if parseErr != nil || !finiteMixer(n) || n < 0 || n > 1 {
		return "", fmt.Errorf("send level must be between 0 and 1")
	}
	return formatMixerNumber(roundMixer(n, step)), nil
}

func mixerDisplayStep(declKind, field string, fallback float64) float64 {
	var id string
	switch declKind {
	case "track_decl":
		id = "mix." + field
		if field == "level" {
			id = "mix.gain"
		}
	case "bus_decl":
		id = "mix.bus." + field
	case "master_decl":
		id = "mix.master." + field
	}
	if descriptor, found := paramdefs.Lookup(id); found && descriptor.DisplayStep > 0 {
		return descriptor.DisplayStep
	}
	return fallback
}

func mixerLevelLiteral(raw json.RawMessage, step float64) (string, bool, error) {
	value, err := jsonStringOrNumber(raw)
	if err != nil {
		return "", false, fmt.Errorf("level must be dB or off")
	}
	if value == "off" {
		return "", true, nil
	}
	suffix := "dB"
	if strings.HasSuffix(value, "dB") {
		value = strings.TrimSuffix(value, "dB")
	} else if strings.HasSuffix(value, "db") {
		value = strings.TrimSuffix(value, "db")
	}
	n, parseErr := strconv.ParseFloat(value, 64)
	if parseErr != nil || !finiteMixer(n) || n < -60 || n > 6 {
		return "", false, fmt.Errorf("level must be -60 to +6 dB or off")
	}
	n = roundMixer(n, step)
	return formatMixerNumber(n) + suffix, false, nil
}

func mixerNumericLiteral(raw json.RawMessage, minValue, maxValue, step float64, unit string) (string, error) {
	value, err := jsonStringOrNumber(raw)
	if err != nil {
		return "", fmt.Errorf("value must be numeric")
	}
	value = strings.TrimSuffix(value, unit)
	n, parseErr := strconv.ParseFloat(value, 64)
	if parseErr != nil || !finiteMixer(n) || n < minValue || n > maxValue {
		return "", fmt.Errorf("value must be within %g..%g", minValue, maxValue)
	}
	return formatMixerNumber(roundMixer(n, step)) + unit, nil
}

func CanonicalMixerLiteral(descriptor paramdefs.Descriptor, raw json.RawMessage) (string, error) {
	if descriptor.Off {
		if value, err := jsonStringOrNumber(raw); err == nil && value == "off" {
			return "off", nil
		}
	}
	if value, err := jsonStringOrNumber(raw); err == nil && descriptor.Source != "sidechain" && containsMixerString(descriptor.Values, value) {
		return value, nil
	}
	if descriptor.Curve == "toggle" {
		value, err := jsonBool(raw)
		if err != nil {
			return "", err
		}
		if value {
			return "true", nil
		}
		return "false", nil
	}
	if descriptor.Curve == "enum" {
		value, err := jsonStringOrNumber(raw)
		if err != nil {
			return "", fmt.Errorf("%s must be one of %s", descriptor.Source, strings.Join(descriptor.Values, ", "))
		}
		if descriptor.Source != "sidechain" && !containsMixerString(descriptor.Values, value) {
			return "", fmt.Errorf("%s must be one of %s", descriptor.Source, strings.Join(descriptor.Values, ", "))
		}
		return value, nil
	}
	unit := ""
	switch strings.ToLower(descriptor.Unit) {
	case "db":
		unit = "dB"
	case "hz":
		unit = "Hz"
	case "ms":
		unit = "ms"
	}
	value, err := jsonStringOrNumber(raw)
	if err != nil {
		return "", fmt.Errorf("%s must be numeric", descriptor.Source)
	}
	number, suffix, err := parseMixerUnitValue(value, unit)
	if err != nil {
		return "", err
	}
	if unit != "" && suffix != "" && suffix != unit {
		return "", fmt.Errorf("%s requires %s", descriptor.Source, unit)
	}
	if number < descriptor.Min || number > descriptor.Max {
		return "", fmt.Errorf("value is outside %g..%g", descriptor.Min, descriptor.Max)
	}
	return formatMixerNumber(roundMixer(number, descriptor.DisplayStep)) + unit, nil
}

func parseMixerUnitValue(value, canonicalUnit string) (float64, string, error) {
	suffixes := []string{"kHz", "Hz", "ms", "dB", "s", "khz", "hz", "db"}
	suffix := ""
	for _, candidate := range suffixes {
		if strings.HasSuffix(value, candidate) {
			suffix = candidate
			value = strings.TrimSuffix(value, candidate)
			break
		}
	}
	number, err := strconv.ParseFloat(value, 64)
	if err != nil || !finiteMixer(number) {
		return 0, suffix, fmt.Errorf("value must be a finite number")
	}
	if strings.EqualFold(suffix, "khz") && canonicalUnit == "Hz" {
		number *= 1000
		suffix = "Hz"
	} else if suffix == "s" && canonicalUnit == "ms" {
		number *= 1000
		suffix = "ms"
	} else if strings.EqualFold(suffix, "db") && canonicalUnit == "dB" {
		suffix = "dB"
	} else if strings.EqualFold(suffix, "hz") && canonicalUnit == "Hz" {
		suffix = "Hz"
	} else if suffix == "ms" && canonicalUnit == "ms" {
		suffix = "ms"
	}
	return number, suffix, nil
}

func insertNames(raw json.RawMessage) ([]string, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var names []string
	if len(raw) > 0 && raw[0] == '[' {
		if err := json.Unmarshal(raw, &names); err != nil {
			return nil, fmt.Errorf("insert chain must be a list of effect names")
		}
	} else {
		value, err := jsonStringOrNumber(raw)
		if err != nil {
			return nil, fmt.Errorf("insert must be none or an effect chain")
		}
		if value == "" || value == "none" {
			return nil, nil
		}
		for _, part := range strings.Fields(value) {
			if part != "->" {
				names = append(names, part)
			}
		}
	}
	for _, name := range names {
		if !ValidMixerIdentifier(name) {
			return nil, fmt.Errorf("invalid insert name %q", name)
		}
	}
	return names, nil
}

func addMixerBlock(source []byte, root *gts.Node, walker *walk.Walker, owner, kind, field string, raw json.RawMessage, score *notation.Score) ([]byte, string, string, LineRange, error) {
	if kind == "bus_decl" && owner != "music" && owner != "sfx" {
		return nil, "", "", LineRange{}, unsupportedMixerRoute("user-declared bus " + owner + " is not implemented")
	}
	if field != "level" && field != "mute" && field != "solo" && field != "insert" && field != "pan" && field != "out" && !strings.HasPrefix(field, "send.") {
		return nil, "", "", LineRange{}, fmt.Errorf("unsupported mixer field %q", field)
	}
	base := ""
	if kind == "master_decl" {
		base = "master {}"
	} else {
		base = "bus " + owner + " {}"
	}
	updated := insertTopLevelMixerBlock(source, root, walker, kind, owner, base)
	return mixerSource(updated, owner+"."+field, raw)
}

func insertTopLevelMixerBlock(source []byte, root *gts.Node, walker *walk.Walker, kind, owner, declaration string) []byte {
	var at int
	for i := 0; i < root.NamedChildCount(); i++ {
		node := root.NamedChild(i)
		typ := walker.Type(node)
		if typ == kind {
			name := walker.Text(walker.Field(node, "name"))
			if name == owner || kind == "master_decl" {
				at = int(node.EndByte())
			}
		}
	}
	if at == 0 {
		at = topLevelAppendOffset(source, root, walker)
	}
	return replaceMixerSpan(source, at, at, []byte(topLevelSeparator(source, at, declaration)))
}

func insertTopLevelFX(source []byte, root *gts.Node, walker *walk.Walker, name, kind string) []byte {
	declaration := "fx " + name + " " + kind + " {}"
	at := topLevelAppendOffset(source, root, walker)
	return replaceMixerSpan(source, at, at, []byte(topLevelSeparator(source, at, declaration)))
}

func topLevelSeparator(source []byte, at int, declaration string) string {
	newlines := 0
	for i := at - 1; i >= 0 && source[i] == '\n'; i-- {
		newlines++
	}
	prefix := "\n\n"
	if newlines == 1 {
		prefix = "\n"
	} else if newlines >= 2 {
		prefix = ""
	}
	suffix := "\n"
	if at < len(source) {
		suffix = "\n\n"
	}
	return prefix + declaration + suffix
}

func topLevelAppendOffset(source []byte, root *gts.Node, walker *walk.Walker) int {
	lastDecl := 0
	firstTrailingComment := -1
	for i := 0; i < root.NamedChildCount(); i++ {
		node := root.NamedChild(i)
		if walker.Type(node) == "comment" {
			if lastDecl > 0 && int(node.StartByte()) > lastDecl && firstTrailingComment < 0 {
				firstTrailingComment = int(node.StartByte())
			}
			continue
		}
		lastDecl = int(node.EndByte())
		firstTrailingComment = -1
	}
	if firstTrailingComment >= 0 {
		return bytes.LastIndexByte(source[:firstTrailingComment], '\n') + 1
	}
	return len(source)
}

func mixerFXDescriptor(kind, field string) (paramdefs.Descriptor, bool) {
	for _, descriptor := range paramdefs.Registry {
		if descriptor.Scope == "global" && strings.HasPrefix(descriptor.ID, "fx."+kind+".") && strings.TrimPrefix(descriptor.Path, kind+".") == field {
			return descriptor, true
		}
	}
	return paramdefs.Descriptor{}, false
}

func descriptorOrder(kind, field string) int {
	for i, descriptor := range paramdefs.Registry {
		if descriptor.Scope == "global" && strings.HasPrefix(descriptor.ID, "fx."+kind+".") && descriptor.Source == field {
			return i
		}
	}
	return int(^uint(0) >> 1)
}

func mixerFieldOrder(field string) int {
	switch field {
	case "level":
		return 0
	case "pan":
		return 1
	case "mute":
		return 2
	case "solo":
		return 3
	case "insert":
		return 4
	case "send":
		return 5
	case "out":
		return 6
	default:
		return int(^uint(0) >> 1)
	}
}

func FindNotationEffect(score *notation.Score, name string) *notation.Effect {
	for i := range score.Effects {
		if score.Effects[i].Name == name {
			return &score.Effects[i]
		}
	}
	return nil
}

func requestedFX(raw json.RawMessage) (mixerFXValue, error) {
	if len(raw) > 0 && raw[0] == '{' {
		var value mixerFXValue
		if err := json.Unmarshal(raw, &value); err != nil {
			return mixerFXValue{}, err
		}
		if value.Kind == "" {
			return mixerFXValue{}, fmt.Errorf("new effect needs a kind")
		}
		return value, nil
	}
	kind, err := jsonStringOrNumber(raw)
	return mixerFXValue{Kind: kind}, err
}

func knownMixerEffectKind(kind string) bool {
	return kind == "drive" || kind == "delay" || kind == "reverb" || kind == "comp"
}

func ValidMixerIdentifier(name string) bool {
	if len(name) == 0 || len(name) > 64 || !(name[0] == '_' || name[0] >= 'a' && name[0] <= 'z') {
		return false
	}
	for i := 1; i < len(name); i++ {
		c := name[i]
		if c != '_' && c != '-' && !(c >= 'a' && c <= 'z') && !(c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func containsMixerString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func jsonStringOrNumber(raw json.RawMessage) (string, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}
	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&number); err == nil {
		return number.String(), nil
	}
	return "", fmt.Errorf("expected a string or number")
}

func jsonBool(raw json.RawMessage) (bool, error) {
	var value bool
	if err := json.Unmarshal(raw, &value); err == nil {
		return value, nil
	}
	text, err := jsonStringOrNumber(raw)
	if err == nil {
		switch text {
		case "on", "true":
			return true, nil
		case "off", "false":
			return false, nil
		}
	}
	return false, fmt.Errorf("switch must be on or off")
}

func finiteMixer(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }
func roundMixer(value, step float64) float64 {
	if step <= 0 {
		return value
	}
	rounded := math.Round(value/step) * step
	return math.Round(rounded*1e8) / 1e8
}

func formatMixerNumber(value float64) string {
	if value == 0 {
		return "0"
	}
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func replaceMixerSpan(source []byte, start, end int, replacement []byte) []byte {
	updated := make([]byte, 0, len(source)-(end-start)+len(replacement))
	updated = append(updated, source[:start]...)
	updated = append(updated, replacement...)
	updated = append(updated, source[end:]...)
	return updated
}

func SourceRangeAt(source []byte, start, end int) LineRange {
	return LineRange{Start: lineNumberAt(source, start), End: lineNumberAt(source, max(start, end-1))}
}

func lineNumberAt(source []byte, offset int) int {
	if offset < 0 {
		return 1
	}
	if offset > len(source) {
		offset = len(source)
	}
	return bytes.Count(source[:offset], []byte{'\n'}) + 1
}

func findInsertedValue(source []byte, name, literal string, from int) int {
	if from < 0 {
		from = 0
	}
	if from > len(source) {
		from = len(source)
	}
	needle := []byte(name + " = " + literal)
	if offset := bytes.Index(source[from:], needle); offset >= 0 {
		return from + offset
	}
	return from
}

func unsupportedMixerRoute(reason string) error {
	return fmt.Errorf("CICADA-UNSUPPORTED: %s", reason)
}

func TrackMixerSourceText(params []notation.Param, name string) string {
	for _, param := range params {
		if param.Name == name {
			return param.Value
		}
	}
	return ""
}

func TrackParams(score *notation.Score, owner string) []notation.Param {
	for _, track := range score.Tracks {
		if track.Name == owner {
			return track.Params
		}
	}
	return nil
}

func BusParams(score *notation.Score, owner string) []notation.Param {
	for _, bus := range score.Buses {
		if bus.Name == owner {
			return bus.Params
		}
	}
	return nil
}

// spacedMixerValue puts a space before a dB unit: -3.5dB becomes -3.5 dB.
func spacedMixerValue(value string) string {
	if strings.HasSuffix(value, "dB") {
		return strings.TrimSuffix(value, "dB") + " dB"
	}
	if strings.HasSuffix(value, "db") {
		return strings.TrimSuffix(value, "db") + " dB"
	}
	return value
}

// AddEffect declares a new effect; it is SetParam's fx.<name> path with a
// typed request. The effect kind is EffectKind because the envelope's "kind"
// key names the intent.
type AddEffect struct {
	Name           string `json:"name"`
	EffectKind     string `json:"effectkind"`
	InsertOn       string `json:"inserton,omitempty"`
	ConfirmUpgrade bool   `json:"confirmupgrade,omitempty"`
}

func (AddEffect) Kind() string { return "addeffect" }

func init() {
	Register("addeffect", func() Intent { return &AddEffect{} })
	Handle("addeffect", addEffect)
}

func addEffect(ctx *Context, intent Intent) error {
	in := intent.(*AddEffect)
	raw, err := json.Marshal(mixerFXValue{Kind: in.EffectKind, InsertOn: in.InsertOn})
	if err != nil {
		return err
	}
	return setMixerParam(ctx, "fx."+in.Name, raw, in.ConfirmUpgrade)
}

// paramWriterFor resolves owner.field for the generic intent surface. An
// instrument that declares the parameter keeps it. A mixer field name or a
// mixer owner (the fixed buses, fx, a declared effect or bus) goes to the mixer
// writer. A track that shares its name with a mixer owner is resolved by the
// field: a mixer field name there is ambiguous and refused, and any other
// field belongs to the instrument.
func paramWriterFor(score *notation.Score, owner, field string) (ParamWriter, error) {
	isTrack := false
	for _, track := range score.Tracks {
		if track.Name != owner {
			continue
		}
		isTrack = true
		for _, definition := range score.Instruments {
			if definition.Name != track.Kind {
				continue
			}
			for _, param := range definition.Params {
				if param.Name == field {
					return ParamWriterInstrument, nil
				}
			}
		}
	}
	fieldIsMixer := strings.HasPrefix(field, "send")
	switch field {
	case "level", "pan", "mute", "solo", "insert", "out":
		fieldIsMixer = true
	}
	ownerIsMixer := false
	switch owner {
	case "master", "music", "sfx", "fx":
		ownerIsMixer = true
	}
	for _, effect := range score.Effects {
		ownerIsMixer = ownerIsMixer || effect.Name == owner
	}
	for _, bus := range score.Buses {
		ownerIsMixer = ownerIsMixer || bus.Name == owner
	}
	switch {
	case isTrack && ownerIsMixer && fieldIsMixer:
		return "", fmt.Errorf("parameter path %q is ambiguous: %s names both a track and a mixer owner; use the instrument or mixer route", owner+"."+field, owner)
	case isTrack && ownerIsMixer:
		return ParamWriterInstrument, nil
	case fieldIsMixer || ownerIsMixer:
		return ParamWriterMixer, nil
	}
	return ParamWriterInstrument, nil
}

// setMixerParam is the /api/mixer gesture: an edition-1 score is upgraded
// first (with confirmation), then the writer edits the smallest CST span.
func setMixerParam(ctx *Context, path string, raw json.RawMessage, confirm bool) error {
	if path == "" || len(raw) == 0 {
		return errors.New("mixer path and value are required")
	}
	score, ds, err := ctx.ParseProject()
	if err != nil {
		return err
	}
	if score == nil || hasErrors(ds) {
		return errors.New("score must validate before mixer edits")
	}
	working := bytes.Clone(ctx.Source)
	if score.Version == 1 {
		if !confirm {
			return errors.New("This score uses edition 1. Upgrade to edition 2 to save mixer changes?")
		}
		if ctx.Options.UpgradeEdition == nil {
			return ErrNoUpgrade
		}
		upgraded, files, err := ctx.Options.UpgradeEdition(working, ctx.fileOverrides())
		if err != nil {
			return err
		}
		working = upgraded
		for _, file := range files {
			ctx.AddFile(file)
		}
		ctx.Options.Edition = 2
	}
	updated, before, after, changedRange, err := mixerSource(working, path, raw)
	if err != nil {
		return err
	}
	if strings.HasSuffix(path, ".level") {
		var requested string
		_ = json.Unmarshal(raw, &requested)
		if requested == "off" {
			after = "off"
		}
		before = spacedMixerValue(before)
		after = spacedMixerValue(after)
	}
	ctx.Source = updated
	ctx.SetLabel(fmt.Sprintf("Mix: %s %s to %s", strings.ReplaceAll(path, ".", " "), before, after))
	ctx.Respond("path", path)
	ctx.Respond("value", after)
	ctx.Respond("previous", before)
	ctx.Respond("changedRange", changedRange)
	ctx.Respond("source", string(updated))
	return nil
}
