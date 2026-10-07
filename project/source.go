package project

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/notation"
)

var pitchNames = [12]string{"c", "c#", "d", "d#", "e", "f", "f#", "g", "g#", "a", "a#", "b"}

// ToSource writes concise notation in the project's source edition. It
// reparses and recompiles the result before returning so a JSON project
// cannot silently change meaning.
func ToSource(p *Project) ([]byte, error) {
	if err := ValidateProject(p); err != nil {
		return nil, err
	}
	var sections []string
	if p.Title != "" {
		sections = append(sections, "title "+strconv.Quote(p.Title))
	}
	if p.TempoMilli != 130_000 {
		sections = append(sections, "tempo "+decimal(float64(p.TempoMilli)/1000))
	}
	if p.Key.Root != 9 || p.Key.Scale != "minor" {
		sections = append(sections, "key "+pitchNames[p.Key.Root]+" "+p.Key.Scale)
	}
	if p.Seed != 0 {
		sections = append(sections, "seed "+strconv.FormatUint(uint64(p.Seed), 10))
	}
	sections = append(sections, audioSource(p)...)
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
		out.WriteString("fx " + effect.ID)
		if effect.Kind != "" {
			out.WriteString(" " + effect.Kind)
		}
		out.WriteString(" {")
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
	for _, bus := range p.Buses {
		var out strings.Builder
		out.WriteString("bus " + bus.ID + " {")
		writeMixerSettings(&out, bus.Mixer, bus.ID)
		out.WriteByte('}')
		sections = append(sections, out.String())
	}
	if p.Master != nil {
		var out strings.Builder
		out.WriteString("master {")
		writeMixerSettings(&out, p.Master.Mixer, "master")
		out.WriteByte('}')
		sections = append(sections, out.String())
	}
	for _, export := range p.Exports {
		var out strings.Builder
		out.WriteString("export " + export.ID + " {")
		if export.Rate != nil {
			out.WriteString("\n  rate = " + strconv.Itoa(*export.Rate) + "Hz")
		}
		if export.Bits != nil {
			out.WriteString("\n  bits = " + strconv.Itoa(*export.Bits))
		}
		for _, item := range []struct {
			name  string
			value *Value
		}{{"tail", export.Tail}, {"loudness", export.Loudness}, {"true_peak", export.TruePeak}} {
			if item.value == nil {
				continue
			}
			value, err := valueSource(*item.value)
			if err != nil {
				return nil, err
			}
			out.WriteString("\n  " + item.name + " = " + value)
		}
		if export.Normalize != nil {
			value := "off"
			if *export.Normalize {
				value = "on"
			}
			out.WriteString("\n  normalize = " + value)
		}
		if export.Rate != nil || export.Bits != nil || export.Tail != nil || export.Loudness != nil || export.TruePeak != nil || export.Normalize != nil {
			out.WriteByte('\n')
		}
		out.WriteByte('}')
		sections = append(sections, out.String())
	}
	for _, track := range p.Tracks {
		var out strings.Builder
		track = normalizeGuitarOptIn(track)
		out.WriteString("track " + track.ID + " " + track.Kind)
		if p.p2Syntax {
			source, err := trackMixerSource(track)
			if err != nil {
				return nil, err
			}
			out.WriteString(source)
			sections = append(sections, out.String())
			continue
		}
		hasMixer := track.Mixer.Mute || track.Mixer.GainDB != defaultMixer().GainDB || track.Mixer.Pan != 0 || track.Mixer.Insert != "none" || track.Mixer.SendA != 0 || track.Mixer.SendB != 0 || track.Mixer.SendPre || track.Mixer.Bus != "music"
		if len(track.Params) == 0 && !hasMixer && len(track.Chain) == 0 {
			out.WriteString(" {}")
		} else {
			out.WriteString(" {\n")
			if len(track.Chain) > 0 {
				out.WriteString("  chain = " + strings.Join(track.Chain, " ") + "\n")
			}
			if track.Mixer.Mute {
				out.WriteString("  level = off\n")
			} else if track.Mixer.GainDB != defaultMixer().GainDB {
				out.WriteString("  level = " + decimal(track.Mixer.GainDB) + "dB\n")
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
	if p.Live != nil {
		sections = append(sections, liveSource(p.Live))
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
		source, err := patternSource(pattern, slot, assigned && !automatic[pattern.ID], projectPatternUsedOnlyByAcid(p, pattern.ID), p.Seed)
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
			if pattern == "keep" {
				continue
			}
			if pattern == "off" && !projectHasPattern(p, "stop") {
				pattern = "stop"
			}
			out.WriteString("\n  " + track + " = " + pattern)
		}
		for _, setting := range scene.Settings {
			value, err := sceneSettingSource(p, setting)
			if err != nil {
				return nil, fmt.Errorf("scene %s path %s: %w", scene.ID, setting.Path, err)
			}
			out.WriteString("\n  " + setting.Path + " = " + value)
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
	if p.Arrange == nil {
		sections = append(sections, song.String())
	} else {
		sections = append(sections, arrangementSource(p.Arrange))
	}
	source := strings.Join(sections, "\n\n") + "\n"
	if p.Edition == 2 {
		source = "cicada 2\n" + source
	}
	result := []byte(source)
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
	before, beforeErr := canonicalProjectBytes(p)
	after, afterErr := canonicalProjectBytes(recompiled)
	if beforeErr != nil || afterErr != nil || !bytes.Equal(before, after) {
		return nil, fmt.Errorf("project cannot be represented by Cicada source edition %d without changing its meaning", p.Edition)
	}
	return result, nil
}

func sceneSettingSource(p *Project, setting SceneSetting) (string, error) {
	value := setting.Value.projectValue()
	if value.Unit == "enum" {
		return value.Text, nil
	}
	resolved, err := ResolveParameterPath(p, setting.Path)
	if err != nil {
		return "", err
	}
	if resolved.Descriptor.Curve == "toggle" && value.Number != nil {
		if *value.Number == 1 {
			return "on", nil
		}
		if *value.Number == 0 {
			return "off", nil
		}
		return "", fmt.Errorf("invalid toggle value")
	}
	return valueSource(value)
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
	if inst.Octave != nil {
		out.WriteString("  octave = " + strconv.Itoa(*inst.Octave) + "\n")
	}
	for _, param := range inst.Params {
		value, err := typedNumber(param.Default, instrument.Type(param.Unit))
		if err != nil {
			return "", err
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
	if expr.Op == "period" && len(expr.Args) == 2 {
		left, err := exprSource(expr.Args[0], instrument.Unit, symbols, bindings)
		if err != nil {
			return "", err
		}
		right, err := exprSource(expr.Args[1], instrument.Hz, symbols, bindings)
		if err != nil {
			return "", err
		}
		return "(" + left + " / " + right + ")", nil
	}
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
		if want == instrument.MS && right == instrument.Hz {
			return instrument.Unit, instrument.Hz
		}
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
	if expr.Op == "/" && left == instrument.Unit && right == instrument.Hz {
		return instrument.MS
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
	case "pm":
		return []instrument.Type{instrument.Hz, instrument.Audio, instrument.Unit}, true
	case "noise":
		return []instrument.Type{}, true
	case "ddsp":
		return []instrument.Type{instrument.Hz, instrument.Unit}, true
	case "env":
		return []instrument.Type{instrument.Gate, instrument.MS}, true
	case "adsr":
		return []instrument.Type{instrument.Gate, instrument.MS, instrument.MS, instrument.Unit, instrument.MS}, true
	case "pulse":
		return []instrument.Type{instrument.Hz, instrument.Unit}, true
	case "ladder", "diode", "svf":
		return []instrument.Type{instrument.Audio, instrument.Hz, instrument.Unit}, true
	case "lowpass", "highpass":
		return []instrument.Type{instrument.Audio, instrument.Hz}, true
	case "delay":
		return []instrument.Type{instrument.Audio, instrument.MS}, true
	case "period":
		return []instrument.Type{instrument.Unit, instrument.Hz}, true
	case "comb":
		return []instrument.Type{instrument.Audio, instrument.MS, instrument.Unit, instrument.Unit}, true
	case "neural_amp":
		return []instrument.Type{instrument.Audio, instrument.Unit}, true
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
	case "period":
		return instrument.MS
	case "saw", "square", "sine", "pm", "pulse", "svf", "noise", "ladder", "diode", "lowpass", "highpass", "mix", "tanh", "delay", "comb", "neural_amp", "ddsp":
		return instrument.Audio
	case "env", "exp2", "clamp", "adsr":
		return instrument.Unit
	}
	return ""
}

func patternSource(pattern Pattern, slot int, assigned, acidTrackOnly bool, projectSeed uint32) (string, error) {
	var out strings.Builder
	out.WriteString("pattern " + pattern.ID)
	if pattern.Kind == "drums" || pattern.Kind == "acid" && !acidTrackOnly {
		out.WriteString(" " + pattern.Kind)
	}
	out.WriteString(" {\n")
	if pattern.StepTicks != 0 {
		out.WriteString("  step = " + strconv.Itoa(int(pattern.StepTicks)) + "/3840\n")
	}
	if pattern.SwingPercent100 != 5000 {
		out.WriteString("  swing = " + percent100Text(pattern.SwingPercent100) + "%\n")
	}
	if pattern.GatePercent != 55 {
		out.WriteString("  gate = " + strconv.Itoa(int(pattern.GatePercent)) + "%\n")
	}
	if pattern.Transpose != 0 {
		out.WriteString("  transpose = " + strconv.Itoa(int(pattern.Transpose)) + "\n")
	}
	if pattern.Seed != projectSeed {
		out.WriteString("  seed = " + strconv.FormatUint(uint64(pattern.Seed), 10) + "\n")
	}
	if assigned {
		out.WriteString("  slot = " + strconv.Itoa(slot) + "\n")
	}
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
			out.WriteString("  " + lane + ": " + notation.FormatDrumHits(hits) + "\n")
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
		needsVelocity := false
		for _, step := range pattern.Data {
			if step != nil && step.Velocity != 100 {
				needsVelocity = true
			}
		}
		if needsVelocity {
			var values []string
			for _, step := range pattern.Data {
				velocity := 100
				if step != nil {
					velocity = int(step.Velocity)
				}
				if velocity < 1 {
					return "", fmt.Errorf("velocity must be 1..127 for source")
				}
				values = append(values, strconv.Itoa(velocity))
			}
			out.WriteString("  velocity: " + strings.Join(values, " ") + "\n")
		}
		writeExpressionSource(&out, pattern.Expression)
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
	if len(step.Notes) > 0 {
		pitches := make([]string, len(step.Notes))
		for i, note := range step.Notes {
			pitches[i] = absolutePitch(uint8(note))
		}
		text = "[" + strings.Join(pitches, " ") + "]"
	}
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
		text += "?" + strconv.Itoa(int(step.Probability))
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
		text += "?" + strconv.Itoa(int(step.Probability))
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
	if value.Number != nil && value.Unit == "ratio" {
		return decimal(*value.Number), nil
	}
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
		return decimal(value) + "Hz", nil
	case instrument.MS:
		return decimal(value) + "ms", nil
	case instrument.DB:
		return decimal(value) + "dB", nil
	}
	switch string(unit) {
	case "lu":
		return decimal(value) + "LU", nil
	case "lufs":
		return decimal(value) + "LUFS", nil
	case "frames":
		return decimal(value) + "frames", nil
	case "dbtp":
		return decimal(value) + "dBTP", nil
	case "ratio":
		return decimal(value), nil
	}
	return "", fmt.Errorf("cannot spell literal with unit %s", unit)
}

func trackMixerSource(track Track) (string, error) {
	var out strings.Builder
	out.WriteString(" {")
	settings := 0
	write := func(name, value string) {
		out.WriteString("\n  " + name + " = " + value)
		settings++
	}
	if len(track.Chain) > 0 {
		write("chain", strings.Join(track.Chain, " "))
	}
	if track.Mixer.Level != nil {
		value, err := valueSource(*track.Mixer.Level)
		if err != nil {
			return "", err
		}
		write("level", value)
	}
	if track.Mixer.panSet {
		write("pan", decimal(track.Mixer.Pan))
	}
	if track.Mixer.muteSet {
		value := "off"
		if track.Mixer.Mute {
			value = "on"
		}
		write("mute", value)
	}
	if track.Mixer.soloSet {
		value := "off"
		if track.Mixer.Solo {
			value = "on"
		}
		write("solo", value)
	}
	if track.Mixer.Inserts != nil {
		value := "none"
		if len(track.Mixer.Inserts) > 0 {
			value = strings.Join(track.Mixer.Inserts, " -> ")
		}
		write("insert", value)
	}
	for _, send := range track.Mixer.Sends {
		value, err := valueSource(send.Level)
		if err != nil {
			return "", err
		}
		line := "send " + send.To + " = " + value
		if send.Tap == "pre" {
			line += " pre"
		}
		out.WriteString("\n  " + line)
		settings++
	}
	if track.Mixer.Out != nil {
		write("out", *track.Mixer.Out)
	}
	for _, key := range sortedKeys(track.Params) {
		value, err := valueSource(track.Params[key])
		if err != nil {
			return "", err
		}
		write(key, value)
	}
	if settings > 0 {
		out.WriteByte('\n')
	}
	out.WriteByte('}')
	return out.String(), nil
}

func writeMixerSettings(out *strings.Builder, mixer Mixer, owner string) {
	if mixer.Level != nil {
		if value, err := valueSource(*mixer.Level); err == nil {
			out.WriteString("\n  level = " + value)
		}
	} else if owner == "music" && mixer.GainDB != -3 || owner == "sfx" && mixer.GainDB != 0 || owner == "master" && mixer.GainDB != 0 {
		out.WriteString("\n  level = " + decimal(mixer.GainDB) + "dB")
	}
	if mixer.panSet {
		out.WriteString("\n  pan = " + decimal(mixer.Pan))
	}
	if mixer.muteSet {
		value := "off"
		if mixer.Mute {
			value = "on"
		}
		out.WriteString("\n  mute = " + value)
	}
	if mixer.soloSet {
		value := "off"
		if mixer.Solo {
			value = "on"
		}
		out.WriteString("\n  solo = " + value)
	}
	if mixer.Inserts != nil {
		value := "none"
		if len(mixer.Inserts) > 0 {
			value = strings.Join(mixer.Inserts, " -> ")
		}
		out.WriteString("\n  insert = " + value)
	}
	if mixer.Out != nil {
		out.WriteString("\n  out = " + *mixer.Out)
	}
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
	left, leftErr := canonicalProjectBytes(ptrProject(normalizeProjectMeaning(a)))
	right, rightErr := canonicalProjectBytes(ptrProject(normalizeProjectMeaning(b)))
	return leftErr == nil && rightErr == nil && bytes.Equal(left, right)
}

func ptrProject(project Project) *Project { return &project }

func normalizeProjectMeaning(source *Project) Project {
	p := *source
	p.Edition = 1 // Source edition is metadata; migrations may change it without changing meaning.
	p.Format, p.Version, p.p2Syntax = FormatID2, 2, true
	p.Effects = append([]Effect(nil), source.Effects...)
	p.Buses = append([]Bus(nil), source.Buses...)
	p.Tracks = append([]Track(nil), source.Tracks...)
	effectIDs := make(map[string]string, len(p.Effects))
	for i := range p.Effects {
		kind := semanticEffectKind(p.Effects[i])
		p.Effects[i].Kind = kind
		effectIDs[kind] = p.Effects[i].ID
	}
	for i := range p.Tracks {
		m := &p.Tracks[i].Mixer
		m.Sends = append([]MixerSend{}, m.Sends...)
		if m.Level != nil && m.Level.Unit == "enum" && m.Level.Text == "off" {
			m.Level = nil
			m.Mute = true
		}
		if m.Inserts == nil {
			m.Inserts = []string{}
			if m.Insert != "none" {
				m.Inserts = append(m.Inserts, m.Insert)
			}
		}
		m.Insert = "none"
		if len(m.Inserts) > 0 {
			m.Insert = m.Inserts[0]
		}
		m.Out = stringPointer(m.Bus)
		if m.Sends == nil {
			m.Sends = []MixerSend{}
		}
		kept := m.Sends[:0]
		for j := range m.Sends {
			if m.Sends[j].Level.Number != nil && *m.Sends[j].Level.Number == 0 {
				continue
			}
			if m.Sends[j].Tap == "" {
				m.Sends[j].Tap = "post"
			}
			kept = append(kept, m.Sends[j])
		}
		m.Sends = kept
		for _, item := range []struct {
			kind  string
			value float64
		}{{"delay", m.SendA}, {"reverb", m.SendB}} {
			kind, value := item.kind, item.value
			if value <= 0 {
				continue
			}
			id := effectIDs[kind]
			found := false
			for _, send := range m.Sends {
				found = found || send.To == id
			}
			if found || id == "" {
				continue
			}
			amount := value
			tap := "post"
			if m.SendPre {
				tap = "pre"
			}
			m.Sends = append(m.Sends, MixerSend{To: id, Level: Value{Unit: "ratio", Number: &amount}, Tap: tap})
		}
		m.SendA, m.SendB, m.SendPre = 0, 0, false
		m.panSet, m.muteSet, m.soloSet, m.wireV2 = false, false, false, true
	}
	p.Patterns = append([]Pattern(nil), source.Patterns...)
	for i := range p.Patterns {
		if p.Patterns[i].Kind == "notes" && projectPatternUsedOnlyByAcid(source, p.Patterns[i].ID) {
			p.Patterns[i].Kind = "acid"
		}
	}
	p.Scenes = append([]Scene(nil), source.Scenes...)
	for i := range p.Scenes {
		bindings := make(map[string]string, len(source.Scenes[i].Bindings))
		for track, pattern := range source.Scenes[i].Bindings {
			if pattern != "keep" {
				bindings[track] = pattern
			}
		}
		p.Scenes[i].Bindings = bindings
	}
	if p.Buses == nil {
		p.Buses = []Bus{}
	}
	legacyComp := false
	for _, effect := range source.Effects {
		legacyComp = legacyComp || effect.Kind == "" && effect.ID == "comp"
	}
	hasMusicBus := false
	for i := range p.Buses {
		p.Buses[i].Mixer.wireV2 = true
		hasMusicBus = hasMusicBus || p.Buses[i].ID == "music"
	}
	if legacyComp && !hasMusicBus {
		p.Buses = append(p.Buses, Bus{ID: "music", Mixer: Mixer{GainDB: -6, Insert: "comp", Bus: "music", Inserts: []string{"comp"}, wireV2: true}})
	}
	return p
}
