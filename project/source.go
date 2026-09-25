package project

import (
	"bytes"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/notation"
)

var pitchNames = [12]string{"c", "c#", "d", "d#", "e", "f", "f#", "g", "g#", "a", "a#", "b"}

// ToSource writes explicit normalized notation. It reparses and recompiles the
// result before returning so a JSON project cannot silently change meaning.
func ToSource(p *Project) ([]byte, error) {
	if err := ValidateProject(p); err != nil {
		return nil, err
	}
	var sections []string
	sections = append(sections, "cicada 1")
	if p.Title != "" {
		sections = append(sections, "title "+strconv.Quote(p.Title))
	}
	sections = append(sections, "tempo "+decimal(float64(p.TempoMilli)/1000))
	sections = append(sections, "key "+pitchNames[p.Key.Root]+" "+p.Key.Scale)
	sections = append(sections, "seed "+strconv.FormatUint(uint64(p.Seed), 10))
	for _, inst := range p.Instruments {
		source, err := instrumentSource(inst)
		if err != nil {
			return nil, err
		}
		sections = append(sections, source)
	}
	for _, kit := range p.Kits {
		var out strings.Builder
		out.WriteString("kit " + kit.ID + " {")
		for _, lane := range laneOrder {
			if target, ok := kit.Lanes[lane]; ok {
				out.WriteString("\n  " + lane + " = " + target)
			}
		}
		if len(kit.Lanes) > 0 {
			out.WriteByte('\n')
		}
		out.WriteByte('}')
		sections = append(sections, out.String())
	}
	for _, effect := range p.Effects {
		var out strings.Builder
		out.WriteString("fx " + effect.ID + " {")
		for _, key := range sortedKeys(effect.Params) {
			value, err := valueSource(effect.Params[key])
			if err != nil {
				return nil, err
			}
			out.WriteString("\n  " + key + " = " + value)
		}
		if len(effect.Params) > 0 {
			out.WriteByte('\n')
		}
		out.WriteByte('}')
		sections = append(sections, out.String())
	}
	for _, track := range p.Tracks {
		var out strings.Builder
		out.WriteString("track " + track.ID + " " + track.Kind)
		hasMixer := track.Mixer.Mute || track.Mixer.GainDB != defaultMixer().GainDB || track.Mixer.Pan != 0 || track.Mixer.Insert != "none" || track.Mixer.SendA != 0 || track.Mixer.SendB != 0 || track.Mixer.SendPre || track.Mixer.Bus != "music"
		if len(track.Params) == 0 && !hasMixer {
			out.WriteString(" {}")
		} else {
			out.WriteString(" {\n")
			if track.Mixer.Mute {
				out.WriteString("  level = off\n")
			} else if track.Mixer.GainDB != defaultMixer().GainDB {
				out.WriteString("  level = " + decimal(track.Mixer.GainDB) + "db\n")
			}
			if track.Mixer.Pan != 0 {
				out.WriteString("  pan = " + decimal(track.Mixer.Pan) + "\n")
			}
			if track.Mixer.Insert != "none" {
				out.WriteString("  insert = " + track.Mixer.Insert + "\n")
			}
			if track.Mixer.SendA != 0 {
				out.WriteString("  send_a = " + decimal(track.Mixer.SendA) + "\n")
			}
			if track.Mixer.SendB != 0 {
				out.WriteString("  send_b = " + decimal(track.Mixer.SendB) + "\n")
			}
			if track.Mixer.SendPre {
				out.WriteString("  send_pre = true\n")
			}
			if track.Mixer.Bus != "music" {
				out.WriteString("  bus = " + track.Mixer.Bus + "\n")
			}
			for _, key := range sortedKeys(track.Params) {
				value, err := valueSource(track.Params[key])
				if err != nil {
					return nil, err
				}
				out.WriteString("  " + key + " = " + value + "\n")
			}
			out.WriteByte('}')
		}
		sections = append(sections, out.String())
	}
	slots := make(map[string]int)
	automatic := make(map[string]bool)
	for _, track := range p.Tracks {
		for index, id := range track.Slots {
			if id == nil {
				continue
			}
			if previous, exists := slots[*id]; exists && previous != index {
				automatic[*id] = true
			}
			slots[*id] = index
		}
	}
	for _, pattern := range p.Patterns {
		slot, assigned := slots[pattern.ID]
		source, err := patternSource(pattern, slot, assigned && !automatic[pattern.ID])
		if err != nil {
			return nil, err
		}
		sections = append(sections, source)
	}
	for _, scene := range p.Scenes {
		var out strings.Builder
		out.WriteString("scene " + scene.ID + " {")
		for _, track := range sortedKeys(scene.Bindings) {
			pattern := scene.Bindings[track]
			if pattern == "off" && !projectHasPattern(p, "stop") {
				pattern = "stop"
			}
			out.WriteString("\n  " + track + " = " + pattern)
		}
		out.WriteString("\n}")
		sections = append(sections, out.String())
	}
	var song strings.Builder
	song.WriteString("song {")
	for _, entry := range p.Song {
		song.WriteString("\n  " + entry.Scene)
		if entry.Bars != 1 {
			song.WriteString("*" + strconv.Itoa(int(entry.Bars)))
		}
	}
	song.WriteString("\n}")
	sections = append(sections, song.String())
	result := []byte(strings.Join(sections, "\n\n") + "\n")
	score, diagnostics := notation.Parse(result)
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			return nil, fmt.Errorf("generated source is invalid: %s", diagnostic.Message)
		}
	}
	recompiled, diagnostics := FromScore(score)
	if recompiled == nil {
		return nil, fmt.Errorf("generated source cannot compile: %+v", diagnostics)
	}
	if !reflect.DeepEqual(p, recompiled) {
		return nil, fmt.Errorf("project cannot be represented by Cicada source v1 without changing its meaning")
	}
	return result, nil
}

func projectHasPattern(p *Project, name string) bool {
	for _, pattern := range p.Patterns {
		if pattern.ID == name {
			return true
		}
	}
	return false
}

func instrumentSource(inst Instrument) (string, error) {
	bindings := map[string]Expr{}
	symbols := map[string]instrument.Type{
		"pitch": instrument.Hz, "gate": instrument.Gate,
		"velocity": instrument.Unit, "sample_rate": instrument.Hz,
	}
	for _, param := range inst.Params {
		symbols[param.ID] = instrument.Type(param.Unit)
	}
	for _, binding := range inst.Lets {
		bindings[binding.ID] = binding.Value
	}
	required := map[string]instrument.Type{}
	propagateType(inst.Out, instrument.Audio, symbols, bindings, required, map[string]bool{})
	var out strings.Builder
	out.WriteString("instrument " + inst.ID + " {\n")
	for _, param := range inst.Params {
		value, err := typedNumber(param.Default, instrument.Type(param.Unit))
		if err != nil {
			return "", err
		}
		switch param.Unit {
		case "hz":
			value = strings.TrimSuffix(value, "hz") + "Hz"
		case "db":
			value = strings.TrimSuffix(value, "db") + "dB"
		}
		out.WriteString("  param " + param.ID + " = " + value + "\n")
	}
	out.WriteString("  voice " + inst.Mode + " {\n")
	for _, binding := range inst.Lets {
		typ := required[binding.ID]
		if typ == "" {
			typ = inferExprType(binding.Value, symbols, bindings, map[string]bool{})
		}
		if typ == "" {
			typ = instrument.Unit
		}
		value, err := exprSource(binding.Value, typ, symbols, bindings)
		if err != nil {
			return "", err
		}
		out.WriteString("    let " + binding.ID + " = " + value + "\n")
		symbols[binding.ID] = typ
	}
	value, err := exprSource(inst.Out, instrument.Audio, symbols, bindings)
	if err != nil {
		return "", err
	}
	out.WriteString("    out = " + value + "\n  }\n}")
	return out.String(), nil
}

func propagateType(expr Expr, want instrument.Type, symbols map[string]instrument.Type, bindings map[string]Expr, required map[string]instrument.Type, visiting map[string]bool) {
	if expr.Name != "" {
		if binding, ok := bindings[expr.Name]; ok && !visiting[expr.Name] {
			required[expr.Name] = want
			visiting[expr.Name] = true
			propagateType(binding, want, symbols, bindings, required, visiting)
			delete(visiting, expr.Name)
		}
		return
	}
	if expr.Op == "" {
		return
	}
	if args, ok := callArgumentTypes(expr.Op); ok {
		for i, arg := range expr.Args {
			propagateType(arg, args[i], symbols, bindings, required, visiting)
		}
		return
	}
	if len(expr.Args) == 2 {
		left, right := binaryOperandTypes(expr, want, symbols, bindings)
		propagateType(expr.Args[0], left, symbols, bindings, required, visiting)
		propagateType(expr.Args[1], right, symbols, bindings, required, visiting)
	}
}

func exprSource(expr Expr, want instrument.Type, symbols map[string]instrument.Type, bindings map[string]Expr) (string, error) {
	if expr.Literal != nil {
		return typedNumber(*expr.Literal, want)
	}
	if expr.Name != "" {
		return expr.Name, nil
	}
	if args, ok := callArgumentTypes(expr.Op); ok {
		parts := make([]string, len(expr.Args))
		for i, arg := range expr.Args {
			part, err := exprSource(arg, args[i], symbols, bindings)
			if err != nil {
				return "", err
			}
			parts[i] = part
		}
		return expr.Op + "(" + strings.Join(parts, ", ") + ")", nil
	}
	if len(expr.Args) != 2 {
		return "", fmt.Errorf("unsupported expression %s", expr.Op)
	}
	leftType, rightType := binaryOperandTypes(expr, want, symbols, bindings)
	left, err := exprSource(expr.Args[0], leftType, symbols, bindings)
	if err != nil {
		return "", err
	}
	right, err := exprSource(expr.Args[1], rightType, symbols, bindings)
	if err != nil {
		return "", err
	}
	return "(" + left + " " + expr.Op + " " + right + ")", nil
}

func binaryOperandTypes(expr Expr, want instrument.Type, symbols map[string]instrument.Type, bindings map[string]Expr) (instrument.Type, instrument.Type) {
	if expr.Op == "+" || expr.Op == "-" {
		return want, want
	}
	left := inferExprType(expr.Args[0], symbols, bindings, map[string]bool{})
	right := inferExprType(expr.Args[1], symbols, bindings, map[string]bool{})
	if expr.Op == "/" {
		if want == instrument.Unit && left != "" && left == right {
			return left, right
		}
		return want, instrument.Unit
	}
	if want == instrument.Unit {
		return instrument.Unit, instrument.Unit
	}
	if left == want || right == instrument.Unit {
		return want, instrument.Unit
	}
	if right == want || left == instrument.Unit {
		return instrument.Unit, want
	}
	return want, instrument.Unit
}

func inferExprType(expr Expr, symbols map[string]instrument.Type, bindings map[string]Expr, visiting map[string]bool) instrument.Type {
	if expr.Literal != nil {
		return ""
	}
	if expr.Name != "" {
		if typ := symbols[expr.Name]; typ != "" {
			return typ
		}
		if binding, ok := bindings[expr.Name]; ok && !visiting[expr.Name] {
			visiting[expr.Name] = true
			typ := inferExprType(binding, symbols, bindings, visiting)
			delete(visiting, expr.Name)
			return typ
		}
		return ""
	}
	if typ := callOutputType(expr.Op); typ != "" {
		return typ
	}
	if len(expr.Args) != 2 {
		return ""
	}
	left := inferExprType(expr.Args[0], symbols, bindings, visiting)
	right := inferExprType(expr.Args[1], symbols, bindings, visiting)
	if expr.Op == "+" || expr.Op == "-" {
		if left != "" {
			return left
		}
		return right
	}
	if expr.Op == "/" && left == right && left != "" {
		return instrument.Unit
	}
	if left != "" && left != instrument.Unit {
		return left
	}
	if right != "" && right != instrument.Unit {
		return right
	}
	return instrument.Unit
}

func callArgumentTypes(op string) ([]instrument.Type, bool) {
	switch op {
	case "saw", "square", "sine":
		return []instrument.Type{instrument.Hz}, true
	case "noise":
		return []instrument.Type{}, true
	case "env":
		return []instrument.Type{instrument.Gate, instrument.MS}, true
	case "ladder", "diode":
		return []instrument.Type{instrument.Audio, instrument.Hz, instrument.Unit}, true
	case "lowpass", "highpass":
		return []instrument.Type{instrument.Audio, instrument.Hz}, true
	case "mix":
		return []instrument.Type{instrument.Audio, instrument.Audio, instrument.Unit}, true
	case "tanh":
		return []instrument.Type{instrument.Audio}, true
	case "exp2":
		return []instrument.Type{instrument.Unit}, true
	case "clamp":
		return []instrument.Type{instrument.Unit, instrument.Unit, instrument.Unit}, true
	}
	return nil, false
}

func callOutputType(op string) instrument.Type {
	switch op {
	case "saw", "square", "sine", "noise", "ladder", "diode", "lowpass", "highpass", "mix", "tanh":
		return instrument.Audio
	case "env", "exp2", "clamp":
		return instrument.Unit
	}
	return ""
}

func patternSource(pattern Pattern, slot int, assigned bool) (string, error) {
	var out strings.Builder
	out.WriteString("pattern " + pattern.ID + " " + pattern.Kind)
	out.WriteString(" steps = " + strconv.Itoa(int(pattern.Steps)))
	out.WriteString(" swing = " + percent100Text(pattern.SwingPercent100))
	out.WriteString(" gate = " + strconv.Itoa(int(pattern.GatePercent)))
	out.WriteString(" transpose = " + strconv.Itoa(int(pattern.Transpose)))
	out.WriteString(" seed = " + strconv.FormatUint(uint64(pattern.Seed), 10))
	if assigned {
		out.WriteString(" slot = " + strconv.Itoa(slot))
	}
	out.WriteString(" {\n")
	if pattern.Kind == "drums" {
		for _, lane := range laneOrder {
			var hits []string
			for _, step := range pattern.Lanes[lane] {
				hit, err := drumStepSource(step, lane)
				if err != nil {
					return "", err
				}
				hits = append(hits, hit)
			}
			out.WriteString("  " + lane + ": " + strings.Join(hits, " ") + "\n")
		}
	} else {
		var notes []string
		for _, step := range pattern.Data {
			note, err := noteStepSource(step)
			if err != nil {
				return "", err
			}
			notes = append(notes, note)
		}
		out.WriteString("  " + strings.Join(notes, " ") + "\n")
	}
	out.WriteByte('}')
	return out.String(), nil
}

func noteStepSource(step *Step) (string, error) {
	if step == nil {
		return ".", nil
	}
	if step.Tie {
		return "-", nil
	}
	text := absolutePitch(step.Note)
	if step.Accent {
		text += "^"
	}
	if step.Slide {
		text += "~"
	}
	if step.Ratchet > 1 {
		text += "*" + strconv.Itoa(int(step.Ratchet))
	}
	if step.Probability != 100 {
		if step.Probability == 0 {
			return "", fmt.Errorf("zero probability cannot be spelled in source v1")
		}
		text += "%" + strconv.Itoa(int(step.Probability))
	}
	return text, nil
}

func drumStepSource(step *Step, lane string) (string, error) {
	if step == nil {
		return ".", nil
	}
	if step.Tie || step.Slide || step.Note != drumNotes[lane] {
		return "", fmt.Errorf("drum step in lane %s cannot be spelled in source v1", lane)
	}
	text := "x"
	if step.Accent && step.Velocity == 127 {
		text = "X"
	} else if step.Velocity != 100 {
		if step.Accent || step.Velocity%14 != 0 || step.Velocity < 14 || step.Velocity > 126 {
			return "", fmt.Errorf("drum velocity %d cannot be spelled in source v1", step.Velocity)
		}
		text += strconv.Itoa(int(step.Velocity / 14))
	}
	if step.Ratchet > 1 {
		text += "*" + strconv.Itoa(int(step.Ratchet))
	}
	if step.Probability != 100 {
		if step.Probability == 0 {
			return "", fmt.Errorf("zero probability cannot be spelled in source v1")
		}
		text += "%" + strconv.Itoa(int(step.Probability))
	}
	return text, nil
}

func absolutePitch(note uint8) string {
	pc := pitchNames[note%12]
	octave := int(note)/12 - 1
	if octave < 0 {
		return pc + "0" + strings.Repeat(",", -octave)
	}
	if octave > 6 {
		return pc + "6" + strings.Repeat("'", octave-6)
	}
	return pc + strconv.Itoa(octave)
}

func valueSource(value Value) (string, error) {
	if value.Number != nil {
		return typedNumber(*value.Number, instrument.Type(value.Unit))
	}
	if value.Unit == "enum" {
		if division, err := fx.ParseDelayDivision(value.Text); err == nil && division != fx.FreeDelay {
			return value.Text, nil
		}
	}
	if validID(value.Text) {
		return value.Text, nil
	}
	return strconv.Quote(value.Text), nil
}

func typedNumber(value float64, unit instrument.Type) (string, error) {
	switch unit {
	case instrument.Unit:
		return decimal(value), nil
	case instrument.Hz:
		return decimal(value) + "hz", nil
	case instrument.MS:
		return decimal(value) + "ms", nil
	case instrument.DB:
		return decimal(value) + "db", nil
	}
	return "", fmt.Errorf("cannot spell literal with unit %s", unit)
}

func decimal(value float64) string {
	if value == 0 {
		return "0"
	}
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func percent100Text(value uint16) string {
	whole, fraction := value/100, value%100
	if fraction == 0 {
		return strconv.Itoa(int(whole))
	}
	if fraction%10 == 0 {
		return fmt.Sprintf("%d.%d", whole, fraction/10)
	}
	return fmt.Sprintf("%d.%02d", whole, fraction)
}

func sortedKeys[V any](items map[string]V) []string {
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func SemanticEqual(a, b *Project) bool {
	if a == nil || b == nil {
		return a == b
	}
	left, leftError := CanonicalJSON(a)
	right, rightError := CanonicalJSON(b)
	return leftError == nil && rightError == nil && bytes.Equal(left, right)
}
