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
	Kind     string                     `json:"kind"`
	InsertOn string                     `json:"insertOn,omitempty"`
	Before   string                     `json:"before,omitempty"`
	After    string                     `json:"after,omitempty"`
	Settings map[string]json.RawMessage `json:"settings,omitempty"`
}

// mixerSource writes one mixer gesture into the smallest CST span. It
// returns the prior and canonical new value for the session history entry.
func mixerSource(source []byte, path string, raw json.RawMessage) ([]byte, string, string, LineRange, error) {
	return mixerSourceWithParser(source, path, raw, func(candidate []byte) (*notation.Score, []notation.Diagnostic, error) {
		score, ds := notation.Parse(candidate)
		return score, ds, nil
	})
}

// The host's parser keeps references to other project sources available while
// the CST patch remains confined to this source.
func mixerSourceWithParser(source []byte, path string, raw json.RawMessage, parse func([]byte) (*notation.Score, []notation.Diagnostic, error)) ([]byte, string, string, LineRange, error) {
	if path == "" || len(raw) == 0 {
		return nil, "", "", LineRange{}, fmt.Errorf("mixer path and value are required")
	}
	score, diagnostics, parseErr := parse(source)
	if parseErr != nil {
		return nil, "", "", LineRange{}, parseErr
	}
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
			return nil, "", "", LineRange{}, refuse("invalid_name", "name", "invalid effect name %q: use lowercase letters, digits, _ and -, at most 64 bytes", name)
		}
		if mixerNameTaken(score, name) {
			return nil, "", "", LineRange{}, refuse("name_taken", "name", "%s already names an effect, a track or a bus", name)
		}
		if !knownMixerEffectKind(kind) {
			return nil, "", "", LineRange{}, refuse("unknown_effect_kind", "effectkind", "unknown effect kind %q; the kinds are %s", kind, strings.Join(mixerEffectKinds, ", "))
		}
		if request.InsertOn == "" && (request.Before != "" || request.After != "") {
			owner, err := anchorOwner(score, request)
			if err != nil {
				return nil, "", "", LineRange{}, err
			}
			request.InsertOn = owner
		}
		route, chain, err := checkNewEffect(score, name, kind, request.InsertOn)
		if err != nil {
			return nil, "", "", LineRange{}, err
		}
		if err := checkEntryOwnsTarget(score, root, walker, route, request.InsertOn); err != nil {
			return nil, "", "", LineRange{}, err
		}
		chain, err = placeInChain(chain, name, request)
		if err != nil {
			return nil, "", "", LineRange{}, err
		}
		if kind == "convolution" {
			if err := checkConvolutionAsset(score, request.Settings["asset"]); err != nil {
				return nil, "", "", LineRange{}, err
			}
		}
		if raw, ok := request.Settings["sidechain"]; ok && kind == "comp" {
			literal, err := effectSettingLiteral(kind, "sidechain", raw, route == "master")
			if err != nil {
				return nil, "", "", LineRange{}, &FieldError{Field: "settings.sidechain", Err: err}
			}
			if err := checkSidechain(score, route, literal, "settings.sidechain"); err != nil {
				return nil, "", "", LineRange{}, err
			}
		}
		declaration, err := effectDeclaration(name, kind, request.Settings, route == "master")
		if err != nil {
			return nil, "", "", LineRange{}, err
		}
		declaration = strings.ReplaceAll(declaration, "\n", Newline(source))
		at := topLevelAppendOffset(source, root, walker)
		updated := replaceMixerSpan(source, at, at, []byte(topLevelSeparator(source, at, declaration)))
		line := lineNumberAt(updated, bytes.Index(updated, []byte("fx "+name+" ")))
		changed := LineRange{Start: line, End: line}
		// Settings that pass their own checks can still break a rule of the
		// language; say so before the route is written.
		if _, declared, err := parse(updated); err != nil {
			return nil, "", "", LineRange{}, err
		} else {
			for _, d := range declared {
				if d.Severity == "error" {
					return nil, "", "", LineRange{}, &ReasonError{Reason: "settings_invalid", Field: "settings", Message: fmt.Sprintf("effect %s does not validate: %s", name, d.Text())}
				}
			}
		}
		if route == "master" && !score.HasMaster {
			// A new master declaration is written whole, in the layout cicada
			// fmt gives it.
			newRoot, newWalker, err := notation.ParseTree(updated)
			if err != nil {
				return nil, "", "", LineRange{}, err
			}
			newline := Newline(updated)
			block := "master {" + newline + "  insert = " + name + newline + "}"
			updated = insertTopLevelMixerBlock(updated, newRoot, newWalker, "master_decl", "master", block)
			changed.End = lineNumberAt(updated, bytes.Index(updated, []byte(block))+len(block)-1)
			route = ""
		}
		if route != "" {
			owner := request.InsertOn
			value := json.RawMessage(strconv.Quote(strings.Join(chain, " -> ")))
			placed, _, _, insertedRange, insertErr := mixerSourceWithParser(updated, owner+".insert", value, parse)
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
	if field == "insert" && declKind != "fx_decl" {
		// A compressor put in a chain must be one whose sidechain that chain
		// takes, as addeffect checks.
		value, _ := jsonStringOrNumber(raw)
		route := "track"
		switch declKind {
		case "bus_decl":
			route = owner
		case "master_decl":
			route = "master"
		}
		for _, name := range insertChain(value) {
			if effect := FindNotationEffect(score, name); effect != nil && effect.Kind == "comp" {
				if sidechain := paramValue(effect.Params, "sidechain"); sidechain != "" {
					if err := checkSidechain(score, route, sidechain, "value"); err != nil {
						return nil, "", "", LineRange{}, err
					}
				}
			}
		}
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
	literal, err := effectSettingLiteral(kind, field, raw, onMaster(score, owner))
	if err != nil {
		return nil, "", "", LineRange{}, err
	}
	if kind == "comp" && field == "sidechain" {
		if err := checkSidechain(score, effectInsertRoute(score, owner), literal, "value"); err != nil {
			return nil, "", "", LineRange{}, err
		}
	}
	var found *gts.Node
	for i := 0; i < decl.NamedChildCount(); i++ {
		child := decl.NamedChild(i)
		if walker.Type(child) == "param_decl" && walker.Text(walker.Field(child, "name")) == field {
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
	updated := insertMixerSetting(source, walker, decl, "fx_decl", field, literal, descriptorOrder(kind, field))
	line := lineNumberAt(updated, findInsertedValue(updated, field, literal, int(decl.StartByte())))
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
		literal, err := mixerNumericLiteral(field, raw, -1, 1, mixerDisplayStep(declKind, field, .01), "")
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
		} else if start > 0 && source[start-1] == ' ' {
			// An inline send takes the space before it, so no double space is left.
			start--
		}
		updated := replaceMixerSpan(source, start, end, nil)
		line := max(1, lineNumberAt(updated, max(0, start-1)))
		return updated, old, "removed", LineRange{Start: line, End: line}, nil
	}
	return bytes.Clone(source), "absent", "absent", LineRange{}, nil
}

func insertMixerSetting(source []byte, walker *walk.Walker, decl *gts.Node, declKind, field, literal string, order int) []byte {
	var anchor *gts.Node
	for i := 0; i < decl.NamedChildCount() && anchor == nil; i++ {
		child := decl.NamedChild(i)
		candidate := child
		if declKind != "fx_decl" {
			if walker.Type(child) != "mix_setting" || child.NamedChildCount() == 0 {
				continue
			}
			candidate = child.NamedChild(0)
			if walker.Type(candidate) == "send_decl" {
				if order < mixerFieldOrder("send") {
					anchor = child
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
		name := walker.Text(walker.Field(candidate, "name"))
		candidateOrder := mixerFieldOrder(name)
		if declKind == "fx_decl" {
			kind := walker.Text(walker.Field(decl, "kind"))
			if kind == "" {
				kind = walker.Text(walker.Field(decl, "name"))
			}
			candidateOrder = descriptorOrder(kind, name)
		}
		if candidateOrder > order {
			anchor = child
		}
	}
	return insertBlockEntry(source, walker, decl, anchor, field+" = "+literal)
}

func insertMixerSend(source []byte, walker *walk.Walker, decl *gts.Node, target, literal string) []byte {
	var anchor *gts.Node
	for i := 0; i < decl.NamedChildCount(); i++ {
		child := decl.NamedChild(i)
		if walker.Type(child) != "mix_setting" || child.NamedChildCount() == 0 {
			continue
		}
		content := child.NamedChild(0)
		if walker.Type(content) == "param_decl" && walker.Text(walker.Field(content, "name")) == "out" ||
			walker.Type(content) == "send_decl" && walker.Text(walker.Field(content, "to")) > target {
			anchor = child
			break
		}
	}
	return insertBlockEntry(source, walker, decl, anchor, literal)
}

// insertBlockEntry writes one setting into a declaration's block before
// anchor or, with no anchor, last, in the layout cicada fmt gives a block
// with settings. A block whose braces share a line ("{}", "{ a = 1 }") is
// expanded: each setting on its own line, indented two spaces past the
// declaration, and the closing brace on its own line. In a block that is
// already expanded the setting gets its own line with the indentation of the
// block's settings, and comment lines attached above the anchor stay with
// it. Text after the closing brace, such as a comment, stays where it is.
// New lines use the file's newline.
func insertBlockEntry(source []byte, walker *walk.Walker, decl *gts.Node, anchor *gts.Node, entry string) []byte {
	newline := Newline(source)
	start, end := int(decl.StartByte()), int(decl.EndByte())
	open := start + bytes.IndexByte(source[start:end], '{')
	close := end - 1
	if !bytes.Contains(source[open:close], []byte("\n")) {
		declLine := bytes.LastIndexByte(source[:start], '\n') + 1
		outer := source[declLine:start]
		outer = outer[:len(outer)-len(bytes.TrimLeft(outer, " \t"))]
		indent := string(outer) + "  "
		var text strings.Builder
		text.WriteString("{" + newline)
		for i := 0; i < decl.NamedChildCount(); i++ {
			child := decl.NamedChild(i)
			if int(child.StartByte()) <= open || walker.Type(child) == "comment" {
				continue
			}
			if anchor != nil && child.StartByte() == anchor.StartByte() {
				text.WriteString(indent + entry + newline)
			}
			text.WriteString(indent + walker.Text(child) + newline)
		}
		if anchor == nil {
			text.WriteString(indent + entry + newline)
		}
		text.WriteString(string(outer) + "}")
		return replaceMixerSpan(source, open, close+1, []byte(text.String()))
	}
	if anchor != nil {
		at := int(anchor.StartByte())
		lineStart := bytes.LastIndexByte(source[:at], '\n') + 1
		if len(bytes.TrimSpace(source[lineStart:at])) > 0 {
			return replaceMixerSpan(source, at, at, []byte(entry+" "))
		}
		indent := string(source[lineStart:at])
		at = attachedCommentStart(source, at, start)
		return replaceMixerSpan(source, at, at, []byte(indent+entry+newline))
	}
	closeLine := bytes.LastIndexByte(source[:close], '\n') + 1
	if len(bytes.TrimSpace(source[closeLine:close])) == 0 {
		return replaceMixerSpan(source, closeLine, closeLine, []byte(blockIndent(source, walker, decl, closeLine, close)+entry+newline))
	}
	before := close
	for before > open+1 && (source[before-1] == ' ' || source[before-1] == '\t') {
		before--
	}
	return replaceMixerSpan(source, before, close, []byte(" "+entry+" "))
}

// blockIndent is the indentation of a block's settings: that of the last
// setting that starts its own line, else the closing brace's plus two spaces.
func blockIndent(source []byte, walker *walk.Walker, decl *gts.Node, closeLine, close int) string {
	indent := string(source[closeLine:close]) + "  "
	for i := 0; i < decl.NamedChildCount(); i++ {
		child := decl.NamedChild(i)
		if walker.Type(child) == "comment" {
			continue
		}
		at := int(child.StartByte())
		lineStart := bytes.LastIndexByte(source[:at], '\n') + 1
		if lineStart > int(decl.StartByte()) && len(bytes.TrimSpace(source[lineStart:at])) == 0 {
			indent = string(source[lineStart:at])
		}
	}
	return indent
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
		if parseErr != nil || !finiteMixer(n) {
			return "", fmt.Errorf("send level must be a unitless gain or dB")
		}
		if n < -60 || n > 0 {
			return "", outOfRange("send level", value, -60, 0, "dB", "send level must be between -60 and 0 dB")
		}
		n = roundMixer(n, step)
		return formatMixerNumber(n) + "dB", nil
	}
	n, parseErr := strconv.ParseFloat(value, 64)
	if parseErr != nil || !finiteMixer(n) {
		return "", fmt.Errorf("send level must be a unitless gain or dB")
	}
	if n < 0 || n > 1 {
		return "", outOfRange("send level", value, 0, 1, "", "send level must be between 0 and 1")
	}
	return formatMixerNumber(roundMixer(n, step)), nil
}

// invalidValue refuses a value that is not of the parameter's form, with
// reason invalid_value. Its field is the intent's value; a caller that knows
// a better one wraps it.
func invalidValue(format string, args ...any) error {
	return &ReasonError{Reason: "invalid_value", Field: "value", Message: fmt.Sprintf(format, args...)}
}

// outOfRange refuses a value outside a parameter's range with reason
// value_out_of_range, the parameter, the value given, the range and the unit.
// Its field is the intent's value; a caller that knows a better one wraps it.
func outOfRange(parameter, value string, min, max float64, unit, message string) error {
	return &ReasonError{Reason: "value_out_of_range", Field: "value", Message: message, Range: &ValueRange{Parameter: parameter, Value: value, Min: min, Max: max, Unit: unit}}
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
	if parseErr == nil && finiteMixer(n) && (n < -60 || n > 6) {
		return "", false, outOfRange("level", value+suffix, -60, 6, "dB", "level must be -60 to +6 dB or off")
	}
	if parseErr != nil || !finiteMixer(n) {
		return "", false, fmt.Errorf("level must be -60 to +6 dB or off")
	}
	n = roundMixer(n, step)
	return formatMixerNumber(n) + suffix, false, nil
}

func mixerNumericLiteral(parameter string, raw json.RawMessage, minValue, maxValue, step float64, unit string) (string, error) {
	value, err := jsonStringOrNumber(raw)
	if err != nil {
		return "", fmt.Errorf("%s must be numeric", parameter)
	}
	value = strings.TrimSuffix(value, unit)
	n, parseErr := strconv.ParseFloat(value, 64)
	if parseErr != nil || !finiteMixer(n) {
		return "", fmt.Errorf("%s must be numeric", parameter)
	}
	if n < minValue || n > maxValue {
		return "", outOfRange(parameter, value+unit, minValue, maxValue, unit, fmt.Sprintf("%s %s is outside %g to %g", parameter, value+unit, minValue, maxValue))
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
	case "dbtp":
		unit = "dBTP"
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
		if unit != "" {
			return "", invalidValue("%s %q is not a number in %s, such as %s", descriptor.Source, value, unit, formatMixerNumber(descriptor.Max)+unit)
		}
		return "", invalidValue("%s %q is not a number", descriptor.Source, value)
	}
	if strings.EqualFold(descriptor.Unit, "bpm") && suffix != "" {
		return "", invalidValue("%s is a plain number of beats per minute", descriptor.Source)
	}
	if unit != "" && suffix != "" && suffix != unit {
		return "", invalidValue("%s requires %s, not %s", descriptor.Source, unit, suffix)
	}
	if (number < descriptor.Min || number > descriptor.Max) && !containsFloat(descriptor.Isolated, number) {
		return "", outOfRange(descriptor.Source, value, descriptor.Min, descriptor.Max, unit, paramdefs.RangeText(descriptor, descriptor.Source, value, number < descriptor.Min))
	}
	if descriptor.ID == "fx.chorus.voices" && math.Trunc(number) != number {
		return "", fmt.Errorf("chorus voices must be an integer")
	}
	return formatMixerNumber(roundMixer(number, descriptor.DisplayStep)) + unit, nil
}

func parseMixerUnitValue(value, canonicalUnit string) (float64, string, error) {
	suffixes := []string{"dBTP", "kHz", "Hz", "ms", "dB", "s", "khz", "hz", "dbtp", "db"}
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
	} else if strings.EqualFold(suffix, "dbtp") && canonicalUnit == "dBTP" {
		suffix = "dBTP"
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

func topLevelSeparator(source []byte, at int, declaration string) string {
	newline := Newline(source)
	newlines := 0
	for i := at - 1; i >= 0 && (source[i] == '\n' || source[i] == '\r'); i-- {
		if source[i] == '\n' {
			newlines++
		}
	}
	prefix := newline + newline
	if newlines == 1 {
		prefix = newline
	} else if newlines >= 2 {
		prefix = ""
	}
	suffix := newline
	if at < len(source) {
		suffix = newline + newline
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
	if kind == "ampcab" {
		d := paramdefs.Descriptor{Source: field, DisplayStep: 0.01}
		switch field {
		case "drive", "tone":
			d.Min = 0
			d.Max = 1
		case "output":
			d.Min = -24
			d.Max = 12
			d.Unit = "dB"
			d.DisplayStep = 0.1
		case "cabinet":
			d.Min = 0
			d.Max = 2
			d.DisplayStep = 1
		case "bypass":
			d.Curve = "toggle"
		default:
			return paramdefs.Descriptor{}, false
		}
		return d, true
	}

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

// mixerEffectKinds are the effect kinds AddEffect and the fx.<name> mixer
// path declare: every kind the language has.
var mixerEffectKinds = valueList("effect-kind", "drive", "delay", "reverb", "comp", "eq", "deesser", "ampcab", "chorus", "tape", "transient", "width", "limiter", "convolution")

func knownMixerEffectKind(kind string) bool {
	return containsMixerString(mixerEffectKinds, kind)
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

func containsFloat(values []float64, want float64) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
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

// AddEffect declares a new effect and, with InsertOn, inserts it in one
// edit. The effect kind is EffectKind because the envelope's "kind" key names
// the intent. It is SetParam's fx.<name> path with a typed request. Where a
// kind may go follows the language: drive, deesser, ampcab and chorus are
// track inserts; eq a track or master insert; comp a track, bus music or
// master insert; transient, width, limiter and convolution master inserts;
// delay, tape and reverb send returns (one delay or tape, one reverb), which
// a track reaches with setparam param:<track>.send.<name>. Bus sfx takes no
// inserts. Comp, transient, width, limiter and convolution work only in an
// insert chain: give inserton, or set the chain with a later setparam
// param:<owner>.insert in the same envelope; if no intent of the envelope
// does, this intent is refused with effect_needs_insert. A track takes eight
// inserts and master sixteen; the new effect goes at the end of the chain,
// or in front of Before or behind After, inserts already in the chain; with
// Before or After and no InsertOn, the chain that holds that insert. Settings are set as setparam
// param:<name>.<field> sets them; only the compressor on bus music takes a
// sidechain, a track of the score, music or sfx. The edit writes only the
// entry file: an insert target declared in another file is refused with
// declared_in_other_file. An edition-1 score needs ConfirmUpgrade, since a
// mixer write upgrades it to edition 2. The catalog's reasons list names
// every refusal code.
//
// Example: {"kind":"addeffect","name":"echo","effectkind":"delay"}
// Example: {"kind":"addeffect","name":"fuzz","effectkind":"drive","inserton":"bass"}
// Example: {"kind":"addeffect","name":"fuzz","effectkind":"drive","inserton":"bass","before":"grit"}
// Example: {"kind":"addeffect","name":"fuzz","effectkind":"drive","after":"grit"}
// Example: {"kind":"addeffect","name":"hall","effectkind":"reverb"}
// Example: {"kind":"addeffect","name":"glue","effectkind":"comp","inserton":"music"}
// Example: {"kind":"addeffect","name":"tilt","effectkind":"eq","inserton":"keys","settings":{"type":"high_shelf","frequency":"6kHz","gain":"2dB"}}
// Example: {"kind":"addeffect","name":"soft","effectkind":"deesser","inserton":"lead"}
// Example: {"kind":"addeffect","name":"cab","effectkind":"ampcab","inserton":"bass"}
// Example: {"kind":"addeffect","name":"wide","effectkind":"chorus","inserton":"keys"}
// Example: {"kind":"addeffect","name":"warble","effectkind":"tape"}
// Example: {"kind":"addeffect","name":"punch","effectkind":"transient","inserton":"master","settings":{"attack":"3dB"}}
// Example: {"kind":"addeffect","name":"stereo","effectkind":"width","inserton":"master","settings":{"amount":1.2,"bass_mono":"120Hz"}}
// Example: {"kind":"addeffect","name":"ceiling","effectkind":"limiter","inserton":"master","settings":{"ceiling":"-2dBTP"}}
// Example: {"kind":"addeffect","name":"space","effectkind":"convolution","inserton":"master","settings":{"asset":"vocal","mix":0.2}}
type AddEffect struct {
	Name           string                     `json:"name" cicada:"required"`                                                        // the new effect's name: lowercase letters, digits, _ and -, at most 64 bytes
	EffectKind     string                     `json:"effectkind" values:"effect-kind" cicada:"required,selects"`                     // the effect's kind
	InsertOn       string                     `json:"inserton,omitempty" when:"effectkind=comp|transient|width|limiter|convolution"` // a track's name, music or master: where to insert the effect; these kinds need it unless a later intent sets the chain
	Before         string                     `json:"before,omitempty"`                                                              // an insert of that chain to put the new effect in front of; default the end of the chain; without inserton, the chain that holds it
	After          string                     `json:"after,omitempty"`                                                               // an insert of that chain to put the new effect behind
	Settings       map[string]json.RawMessage `json:"settings,omitempty" when:"effectkind=convolution"`                              // initial settings, field to value, such as {"ceiling":"-2dBTP"}; a convolution needs its asset
	ConfirmUpgrade bool                       `json:"confirmupgrade,omitempty"`                                                      // edition-1 scores need confirmation for mixer writes, which upgrade them to edition 2
}

func (AddEffect) Kind() string { return "addeffect" }

func init() {
	Register("addeffect", func() Intent { return &AddEffect{} })
	Handle("addeffect", addEffect)
}

func addEffect(ctx *Context, intent Intent) error {
	in := intent.(*AddEffect)
	raw, err := json.Marshal(mixerFXValue{Kind: in.EffectKind, InsertOn: in.InsertOn, Before: in.Before, After: in.After, Settings: in.Settings})
	if err != nil {
		return err
	}
	if err := setMixerParam(ctx, "fx."+in.Name, raw, in.ConfirmUpgrade); err != nil {
		return err
	}
	if in.InsertOn == "" && effectNeedsInsert[in.EffectKind] {
		// A later intent of the envelope may put it in a chain; if none does,
		// the refusal names this intent rather than the compile.
		name, kind := in.Name, in.EffectKind
		ctx.afterIntents(func(ctx *Context) error {
			score, _, err := ctx.ParseProject()
			if err != nil || score == nil || effectInsertRoute(score, name) != "" {
				return err
			}
			return refuse("effect_needs_insert", "inserton", "a %s works only in an insert chain, and no intent of this envelope put %s in one: give inserton %s, or set the chain with setparam param:<owner>.insert", kind, name, strings.ReplaceAll(effectRouteNames(kind), "a track", "a track name"))
		})
	}
	return nil
}

// mixerNameTaken reports whether a name is already an effect, a track or a
// bus, the namespace the mixer shares.
func mixerNameTaken(score *notation.Score, name string) bool {
	if name == "music" || name == "sfx" || name == "master" || FindNotationEffect(score, name) != nil {
		return true
	}
	for _, track := range score.Tracks {
		if track.Name == name {
			return true
		}
	}
	for _, bus := range score.Buses {
		if bus.Name == name {
			return true
		}
	}
	return false
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
	if ctx.Options.MixerGuard != nil {
		if err := ctx.Options.MixerGuard(); err != nil {
			return err
		}
	}
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
			return refuse("edition_upgrade_needed", "confirmupgrade", "This score uses edition 1. Upgrade to edition 2 to save mixer changes?")
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
	updated, before, after, changedRange, err := mixerSourceWithParser(working, path, raw, func(candidate []byte) (*notation.Score, []notation.Diagnostic, error) {
		workingContext := *ctx
		workingContext.Source = candidate
		return workingContext.ParseProject()
	})
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
