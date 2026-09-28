package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/taproot/walk"
	"m31labs.dev/cicada/internal/paramdefs"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

type mixerLineRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

type mixerField struct {
	Path        string               `json:"path"`
	Address     string               `json:"address,omitempty"`
	Value       any                  `json:"value"`
	SourceValue string               `json:"sourceValue,omitempty"`
	Descriptor  paramdefs.Descriptor `json:"descriptor"`
	Choices     []string             `json:"choices,omitempty"`
	SourceRange mixerLineRange       `json:"sourceRange"`
	Supported   bool                 `json:"supported"`
	Reason      string               `json:"reason,omitempty"`
}

type mixerSendView struct {
	Path        string               `json:"path"`
	Address     string               `json:"address,omitempty"`
	To          string               `json:"to"`
	Kind        string               `json:"kind"`
	Level       string               `json:"level"`
	Value       any                  `json:"value"`
	Pre         bool                 `json:"pre"`
	Descriptor  paramdefs.Descriptor `json:"descriptor"`
	SourceRange mixerLineRange       `json:"sourceRange"`
	Supported   bool                 `json:"supported"`
	Reason      string               `json:"reason,omitempty"`
}

type mixerStripView struct {
	ID          string                `json:"id"`
	Name        string                `json:"name"`
	Kind        string                `json:"kind"`
	Meter       string                `json:"meter"`
	SourceRange mixerLineRange        `json:"sourceRange"`
	Fields      map[string]mixerField `json:"fields"`
	Sends       []mixerSendView       `json:"sends,omitempty"`
	Inserts     []string              `json:"inserts"`
	InsertPath  string                `json:"insertPath"`
	InsertAddr  string                `json:"insertAddress,omitempty"`
	InsertOK    bool                  `json:"insertSupported"`
	InsertWhy   string                `json:"insertReason,omitempty"`
	RouteReason string                `json:"routeReason,omitempty"`
}

type mixerReturnView struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Kind        string         `json:"kind"`
	Meter       string         `json:"meter"`
	SourceRange mixerLineRange `json:"sourceRange"`
	Fields      []mixerField   `json:"fields"`
}

type studioMixerView struct {
	Revision  string                 `json:"revision"`
	Edition   int                    `json:"edition"`
	Tempo     float64                `json:"tempo"`
	Tracks    []mixerStripView       `json:"tracks"`
	Buses     []mixerStripView       `json:"buses"`
	Returns   []mixerReturnView      `json:"returns"`
	Master    mixerStripView         `json:"master"`
	Effects   []mixerReturnView      `json:"effects"`
	Registry  json.RawMessage        `json:"registry"`
	Addresses []project.ParamAddress `json:"addresses"`
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

// studioMixerSource writes one mixer gesture into the smallest CST span. It
// returns the prior and canonical new value for the session history entry.
func studioMixerSource(source []byte, path string, raw json.RawMessage) ([]byte, string, string, mixerLineRange, error) {
	if path == "" || len(raw) == 0 {
		return nil, "", "", mixerLineRange{}, fmt.Errorf("mixer path and value are required")
	}
	score, diagnostics := notation.Parse(source)
	if score == nil || hasDiagnosticErrors(diagnostics) {
		return nil, "", "", mixerLineRange{}, fmt.Errorf("score must validate before a mixer edit")
	}
	root, walker, err := notation.ParseTree(source)
	if err != nil {
		return nil, "", "", mixerLineRange{}, err
	}
	if strings.HasPrefix(path, "fx.") && len(strings.Split(path, ".")) == 2 {
		name := strings.TrimPrefix(path, "fx.")
		request, err := requestedFX(raw)
		if err != nil {
			return nil, "", "", mixerLineRange{}, err
		}
		kind := request.Kind
		if !validMixerIdentifier(name) {
			return nil, "", "", mixerLineRange{}, fmt.Errorf("invalid effect name %q", name)
		}
		for _, effect := range score.Effects {
			if effect.Name == name {
				return nil, "", "", mixerLineRange{}, fmt.Errorf("effect %s already exists", name)
			}
		}
		if !knownMixerEffectKind(kind) {
			return nil, "", "", mixerLineRange{}, fmt.Errorf("unknown effect kind %q", kind)
		}
		updated := insertTopLevelFX(source, root, walker, name, kind)
		line := lineNumberAt(updated, bytes.Index(updated, []byte("fx "+name+" ")))
		changed := mixerLineRange{Start: line, End: line}
		if request.InsertOn != "" {
			if request.InsertOn != "music" || kind != "comp" {
				return nil, "", "", mixerLineRange{}, unsupportedMixerRoute("a compressor can only be inserted on bus music")
			}
			placed, _, _, insertedRange, insertErr := studioMixerSource(updated, "music.insert", json.RawMessage(strconv.Quote(name)))
			if insertErr != nil {
				return nil, "", "", mixerLineRange{}, insertErr
			}
			updated = placed
			changed.Start = min(changed.Start, insertedRange.Start)
			changed.End = max(changed.End, insertedRange.End)
		}
		return updated, "absent", kind, changed, nil
	}

	parts := strings.Split(path, ".")
	if len(parts) < 2 {
		return nil, "", "", mixerLineRange{}, fmt.Errorf("mixer path %q must name an owner and field", path)
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
		return nil, "", "", mixerLineRange{}, fmt.Errorf("unknown mixer owner %q", owner)
	}
	if declKind == "fx_decl" {
		return editFXField(source, walker, decl, owner, field, raw)
	}
	if field == "send" || strings.HasPrefix(field, "send.") {
		if declKind != "track_decl" || len(parts) != 3 || parts[1] != "send" {
			return nil, "", "", mixerLineRange{}, fmt.Errorf("send path must be <track>.send.<effect>")
		}
		return editMixerSend(source, walker, decl, owner, parts[2], raw, score)
	}
	if field != "level" && field != "pan" && field != "mute" && field != "solo" && field != "insert" && field != "out" {
		return nil, "", "", mixerLineRange{}, fmt.Errorf("unsupported mixer field %q", field)
	}
	return editMixerSetting(source, walker, decl, declKind, owner, field, raw, score)
}

func editFXField(source []byte, walker *walk.Walker, decl *gts.Node, owner, field string, raw json.RawMessage) ([]byte, string, string, mixerLineRange, error) {
	kind := walker.Text(walker.Field(decl, "kind"))
	if kind == "" {
		kind = owner
	}
	descriptor, ok := mixerFXDescriptor(kind, field)
	if !ok {
		return nil, "", "", mixerLineRange{}, fmt.Errorf("unknown %s setting %q", kind, field)
	}
	literal, err := canonicalMixerLiteral(descriptor, raw)
	if err != nil {
		return nil, "", "", mixerLineRange{}, err
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
		return updated, old, literal, sourceRangeAt(updated, start, start+len(literal)), nil
	}
	updated := insertMixerSetting(source, walker, decl, "fx_decl", descriptor.Source, literal, descriptorOrder(kind, descriptor.Source))
	line := lineNumberAt(updated, findInsertedValue(updated, descriptor.Source, literal, int(decl.StartByte())))
	return updated, "absent", literal, mixerLineRange{Start: line, End: line}, nil
}

func editMixerSetting(source []byte, walker *walk.Walker, decl *gts.Node, declKind, owner, field string, raw json.RawMessage, score *notation.Score) ([]byte, string, string, mixerLineRange, error) {
	if field == "level" {
		step := mixerDisplayStep(declKind, field, .01)
		literal, isOff, err := mixerLevelLiteral(raw, step)
		if err != nil {
			return nil, "", "", mixerLineRange{}, err
		}
		if isOff {
			before := mixerOwnerLevel(score, declKind, owner, step)
			if declKind == "track_decl" {
				updated, _, _, changed, switchErr := setMixerSwitch(source, walker, decl, declKind, "mute", json.RawMessage(`true`), score)
				return updated, before, "off", changed, switchErr
			}
			withMute, _, _, muteRange, muteErr := studioMixerSource(source, owner+".mute", json.RawMessage(`true`))
			if muteErr != nil {
				return nil, "", "", mixerLineRange{}, muteErr
			}
			updated, _, _, levelRange, editErr := editMixerValueUnchecked(withMute, owner, declKind, field, "off")
			if editErr != nil {
				return nil, "", "", mixerLineRange{}, editErr
			}
			levelRange.Start = min(levelRange.Start, muteRange.Start)
			levelRange.End = max(levelRange.End, muteRange.End)
			return updated, before, "off", levelRange, nil
		}
		updated, before, after, changed, editErr := editOneMixerSetting(source, walker, decl, declKind, field, literal)
		if editErr != nil || trackMixerSourceText(mixerOwnerParams(score, declKind, owner), "level") != "off" {
			return updated, before, after, changed, editErr
		}
		withMute, _, _, muteRange, muteErr := studioMixerSource(updated, owner+".mute", json.RawMessage(`false`))
		if muteErr != nil {
			return nil, "", "", mixerLineRange{}, muteErr
		}
		changed.Start = min(changed.Start, muteRange.Start)
		changed.End = max(changed.End, muteRange.End)
		return withMute, before, after, changed, nil
	}
	if field == "pan" {
		literal, err := mixerNumericLiteral(raw, -1, 1, mixerDisplayStep(declKind, field, .01), "")
		if err != nil {
			return nil, "", "", mixerLineRange{}, err
		}
		return editOneMixerSetting(source, walker, decl, declKind, field, literal)
	}
	if field == "mute" || field == "solo" {
		return setMixerSwitch(source, walker, decl, declKind, field, raw, score)
	}
	if field == "insert" {
		names, err := insertNames(raw)
		if err != nil {
			return nil, "", "", mixerLineRange{}, err
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
			return nil, "", "", mixerLineRange{}, fmt.Errorf("out must be a bus name")
		}
		return editOneMixerSetting(source, walker, decl, declKind, field, value)
	}
	return nil, "", "", mixerLineRange{}, fmt.Errorf("unsupported mixer field %q", field)
}

func editMixerValueUnchecked(source []byte, owner, wantKind, field, literal string) ([]byte, string, string, mixerLineRange, error) {
	root, walker, err := notation.ParseTree(source)
	if err != nil {
		return nil, "", "", mixerLineRange{}, err
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
	return nil, "", "", mixerLineRange{}, fmt.Errorf("unknown mixer owner %q", owner)
}

func mixerOwnerLevel(score *notation.Score, declKind, owner string, step float64) string {
	params := mixerOwnerParams(score, declKind, owner)
	if value := trackMixerSourceText(params, "level"); value != "" {
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
		return trackParams(score, owner)
	case "bus_decl":
		return busParams(score, owner)
	case "master_decl":
		return score.Master
	default:
		return nil
	}
}

func setMixerSwitch(source []byte, walker *walk.Walker, decl *gts.Node, declKind, field string, raw json.RawMessage, _ *notation.Score) ([]byte, string, string, mixerLineRange, error) {
	value, err := jsonBool(raw)
	if err != nil {
		return nil, "", "", mixerLineRange{}, err
	}
	literal := "off"
	if value {
		literal = "on"
	}
	return editOneMixerSetting(source, walker, decl, declKind, field, literal)
}

func editOneMixerSetting(source []byte, walker *walk.Walker, decl *gts.Node, declKind, field, literal string) ([]byte, string, string, mixerLineRange, error) {
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
		return updated, old, literal, sourceRangeAt(updated, start, start+len(literal)), nil
	}
	updated := insertMixerSetting(source, walker, decl, declKind, field, literal, mixerFieldOrder(field))
	line := lineNumberAt(updated, findInsertedValue(updated, field, literal, int(decl.StartByte())))
	return updated, "absent", literal, mixerLineRange{Start: line, End: line}, nil
}

func editMixerSend(source []byte, walker *walk.Walker, decl *gts.Node, owner, target string, raw json.RawMessage, score *notation.Score) ([]byte, string, string, mixerLineRange, error) {
	if !validMixerIdentifier(target) {
		return nil, "", "", mixerLineRange{}, fmt.Errorf("invalid send destination %q", target)
	}
	effect := findNotationEffect(score, target)
	if effect == nil && target != "music" && target != "sfx" {
		return nil, "", "", mixerLineRange{}, fmt.Errorf("send references unknown effect %s", target)
	}
	var request mixerSendValue
	if len(raw) > 0 && raw[0] == '{' {
		if err := json.Unmarshal(raw, &request); err != nil {
			return nil, "", "", mixerLineRange{}, fmt.Errorf("invalid send value: %w", err)
		}
		if request.Remove {
			return removeMixerSend(source, walker, decl, target)
		}
	} else {
		request.Level = bytes.Clone(raw)
	}
	if len(request.Level) == 0 {
		return nil, "", "", mixerLineRange{}, fmt.Errorf("send value needs a level")
	}
	step := .01
	if effect != nil {
		parameter := "mix.send_a"
		if effect.Kind == "reverb" {
			parameter = "mix.send_b"
		}
		if descriptor, found := project.LookupParamDescriptor(parameter); found {
			step = descriptor.DisplayStep
		}
	}
	literal, err := canonicalSendLiteral(request.Level, step)
	if err != nil {
		return nil, "", "", mixerLineRange{}, err
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
		return updated, old, newLiteral, sourceRangeAt(updated, start, start+len(newLiteral)), nil
	}
	// A named send is ordered by destination after the ordinary mixer fields.
	updated := insertMixerSend(source, walker, decl, target, newLiteral)
	line := lineNumberAt(updated, bytes.Index(updated, []byte(newLiteral)))
	return updated, "absent", newLiteral, mixerLineRange{Start: line, End: line}, nil
}

func removeMixerSend(source []byte, walker *walk.Walker, decl *gts.Node, target string) ([]byte, string, string, mixerLineRange, error) {
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
		return updated, old, "removed", mixerLineRange{Start: line, End: line}, nil
	}
	return bytes.Clone(source), "absent", "absent", mixerLineRange{}, nil
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
	if descriptor, found := project.LookupParamDescriptor(id); found && descriptor.DisplayStep > 0 {
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

func canonicalMixerLiteral(descriptor paramdefs.Descriptor, raw json.RawMessage) (string, error) {
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
		if !validMixerIdentifier(name) {
			return nil, fmt.Errorf("invalid insert name %q", name)
		}
	}
	return names, nil
}

func addMixerBlock(source []byte, root *gts.Node, walker *walk.Walker, owner, kind, field string, raw json.RawMessage, score *notation.Score) ([]byte, string, string, mixerLineRange, error) {
	if kind == "bus_decl" && owner != "music" && owner != "sfx" {
		return nil, "", "", mixerLineRange{}, unsupportedMixerRoute("user-declared bus " + owner + " is not implemented")
	}
	if field != "level" && field != "mute" && field != "solo" && field != "insert" && field != "pan" && field != "out" && !strings.HasPrefix(field, "send.") {
		return nil, "", "", mixerLineRange{}, fmt.Errorf("unsupported mixer field %q", field)
	}
	base := ""
	if kind == "master_decl" {
		base = "master {}"
	} else {
		base = "bus " + owner + " {}"
	}
	updated := insertTopLevelMixerBlock(source, root, walker, kind, owner, base)
	return studioMixerSource(updated, owner+"."+field, raw)
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

func findNotationEffect(score *notation.Score, name string) *notation.Effect {
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

func validMixerIdentifier(name string) bool {
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

func sourceRangeAt(source []byte, start, end int) mixerLineRange {
	return mixerLineRange{Start: lineNumberAt(source, start), End: lineNumberAt(source, max(start, end-1))}
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
