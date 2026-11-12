package controls

import (
	"fmt"
	"strings"

	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/instrument/keyboardpresets"
	"m31labs.dev/cicada/internal/paramdefs"
	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/kernel/fx/ampcab"
	"m31labs.dev/cicada/kernel/fx/pro"
	"m31labs.dev/cicada/kernel/fx/tape"
	"m31labs.dev/cicada/kernel/voice/keyboard"
	"m31labs.dev/cicada/kernel/voice/modal"
	"m31labs.dev/cicada/notation"
)

// mixer adds the track, bus and master strips and the song-wide settings.
func (b *builder) mixer() {
	b.owner(Owner{Name: "mix", Kind: KindMix, Meaning: "the mixer strip of every track: level, pan, sends, output bus and inserts, written in the track block",
		ValidatedBy: "notation validateTrackMixer (notation/validate.go); project.CompileMixerParams (project/mixer.go)", ReadBy: "kernel/mix through the engine's track strips",
		Note: "amp_* and chorus_* paths set the controls of the track's single amp or chorus insert from scenes, automation and modulation"})
	b.owner(Owner{Name: "bus", Kind: KindBus, Meaning: "the music and sfx bus strips, written in a bus block",
		ValidatedBy: "notation Validate, bus blocks (notation/validate.go)", ReadBy: "kernel/mix",
		Note: "only bus music takes settings today: an insert of one compressor, level -3dB or off, mute and solo"})
	b.owner(Owner{Name: "master", Kind: KindMaster, Meaning: "the master strip, written in the master block",
		ValidatedBy: "notation Validate, master block (notation/validate.go); project master chain (project/master.go)", ReadBy: "kernel/mix and the master chain"})
	b.owner(Owner{Name: "global", Kind: KindGlobal, Meaning: "song-wide settings written at the top of the score",
		ValidatedBy: "notation parse and project.Validate", ReadBy: "kernel/seq"})
	for _, d := range paramdefs.Registry {
		var owner, name string
		var places []string
		switch {
		case strings.HasPrefix(d.ID, "mix.bus."):
			owner, name, places = "bus", d.Source, setIn(d, InBus)
		case strings.HasPrefix(d.ID, "mix.master."):
			owner, name, places = "master", d.Source, setIn(d, InMaster)
		case strings.HasPrefix(d.ID, "mix."):
			owner, name, places = "mix", d.Source, setIn(d, InTrack)
		case strings.HasPrefix(d.ID, "amp."), strings.HasPrefix(d.ID, "chorus."):
			owner, name, places = "mix", d.Source, setIn(d)
		case d.ID == "global.tempo":
			owner, name, places = "global", d.Source, []string{InScore, InScene}
		case d.ID == "global.transpose":
			owner, name, places = "global", d.Source, []string{InPattern}
		default:
			continue
		}
		if name == "send" {
			name = d.Path
		}
		c := fromDescriptor(owner, name, d)
		c.RegistryID, c.Live, c.SetIn, c.Meaning = d.ID, d.Live, places, paramdefs.Meaning(d.ID)
		switch d.ID {
		case "mix.gain":
			c.Values = nil
			c.Note = "edition 1 also takes off; edition 2 writes mute = on"
		case "mix.send_a":
			c.Aliases, c.Note = []string{"send_a"}, "written send <delay effect> = <level> in the track block, with pre after the level for a pre-fader send; send_a is the edition 1 spelling"
		case "mix.send_b":
			c.Aliases, c.Note = []string{"send_b"}, "written send <reverb effect> = <level> in the track block, with pre after the level for a pre-fader send; send_b is the edition 1 spelling"
		case "mix.send_pre":
			c.Edition, c.Note = 1, "edition 1 only; edition 2 writes pre after a send's level: send room = 0.25 pre"
		case "mix.bus":
			c.Name, c.Path, c.Aliases, c.Note = "out", "bus", []string{"bus"}, "bus is the edition 1 spelling"
		case "mix.insert", "mix.bus.insert", "mix.master.insert":
			c.Type, c.Default, c.Values, c.Min, c.Max = "name", "none", nil, nil, nil
			c.Note = "effect names in signal order, joined with ->, or none"
			if d.ID == "mix.bus.insert" {
				c.Note += "; bus music accepts one compressor and bus sfx none"
			}
		case "global.tempo":
			c.Note = "written tempo 120 at the top of the score; a scene's tempo setting changes it from the scene's start"
		case "global.transpose":
			c.Note = "a pattern attribute, written transpose=12 in the pattern header or transpose = 12 in its body; drum patterns refuse it"
		case "mix.bus.send.delay", "mix.bus.send.reverb", "mix.bus.out", "mix.bus.pan", "mix.master.send.delay", "mix.master.send.reverb", "mix.master.out", "mix.master.pan":
			c.Note, c.Refused = "the language refuses this setting today (not implemented); the registry keeps the row", true
		case "mix.bus.level":
			c.Min, c.Max, c.Default = number(-3), number(-3), -3.0
			c.Note = "bus music accepts only -3dB or off today; the registry row keeps -60dB to 6dB for later"
		}
		b.add(c)
	}
}

// voices adds the built-in voices: acid, the drum machine, the piano, the
// guitar and the modeled percussion profiles.
func (b *builder) voices() {
	b.owner(Owner{Name: "acid", Kind: KindVoice, Meaning: "the built-in acid bass voice: one oscillator, a resonant diode or ladder filter with an envelope, accent and slide",
		ValidatedBy: "notation validTrackParam (acidParams, notation/validate.go); project.CompileAcidParams (project/acid.go)", ReadBy: "kernel/voice/acid (voice.go)"})
	b.owner(Owner{Name: "drums", Kind: KindVoice, Meaning: "the built-in drum machine: eleven synthesized lanes (bd, sd, ch, oh, cp, rs, lt, mt, ht, cb, cy), each set as <lane>_<control>",
		ValidatedBy: "notation validTrackParam (drumParams, notation/validate.go); project.CompileDrumParams (project/drum.go)", ReadBy: "kernel/voice/drum (kit.go)"})
	b.owner(Owner{Name: "piano", Kind: KindVoice, Meaning: "the modeled grand piano",
		ValidatedBy: "notation validTrackParam; project.PianoSustainFromValues (project/piano.go)", ReadBy: "kernel/voice/piano"})
	b.owner(Owner{Name: "guitar", Kind: KindVoice, Meaning: "the experimental plucked-string guitar; needs edition 2 and experimental = on",
		ValidatedBy: "project.CompileGuitarParams (project/guitar.go)", ReadBy: "kernel/voice/guitar"})
	for _, d := range paramdefs.Registry {
		var owner string
		switch {
		case strings.HasPrefix(d.ID, "acid."):
			owner = "acid"
		case strings.HasPrefix(d.ID, "drum."):
			owner = "drums"
		case strings.HasPrefix(d.ID, "guitar."):
			owner = "guitar"
		case d.ID == "piano.sustain":
			owner = "piano"
		default:
			continue
		}
		c := fromDescriptor(owner, d.Source, d)
		c.RegistryID, c.Live, c.SetIn, c.Meaning = d.ID, d.Live, setIn(d, InTrack), paramdefs.Meaning(d.ID)
		if d.ID == "guitar.experimental" {
			c.Values, c.Default = []string{"on"}, "on"
		}
		b.add(c)
	}
	for _, setting := range vibratoSettings() {
		c := setting
		c.Owner = "acid"
		b.add(c)
	}
	b.add(octaveControl("piano", 2))
	for _, name := range modal.Names {
		kind := "model_" + name
		b.owner(Owner{Name: kind, Kind: KindVoice, Family: "modal", Meaning: "modeled " + name + ": struck modes of a " + name + " body",
			ValidatedBy: "notation validTrackParam and validModalSustain; project modal compile (project/modal.go)", ReadBy: "kernel/voice/modal"})
		sustain, _ := registryRow("piano.sustain")
		c := fromDescriptor(kind, "sustain", sustain)
		c.RegistryID, c.Live, c.SetIn = sustain.ID, sustain.Live, setIn(sustain, InTrack)
		c.Values = []string{"off", "on"}
		c.Meaning = "damper pedal, a switch at 0.5: from 0.5 up (or on) released strikes keep ringing; below 0.5 (or off) they are damped at release"
		b.add(c)
		b.add(octaveControl(kind, 4))
	}
}

// octaveControl is the octave a pitched track's unnumbered notes play in.
func octaveControl(owner string, def float64) Control {
	d := paramdefs.Descriptor{ID: owner + ".octave", Path: "octave", Source: "octave", Scope: "track", Unit: "", Min: 0, Max: 6, Default: def, Curve: "integer", Type: "integer"}
	c := fromDescriptor(owner, "octave", d)
	c.SetIn = []string{InTrack}
	c.Meaning = "octave that notes written without an octave number play in"
	return c
}

// vibratoSettings are the baseline vibrato of the acid voice and of every
// authored instrument (notation.VibratoSetting).
func vibratoSettings() []Control {
	rows := []struct {
		name, unit, meaning string
		max, def            float64
	}{
		{"vibrato", "ct", "depth of the baseline vibrato every note gets; 0 is off", 9600, 0},
		{"vibrato_rate", "hz", "speed of the baseline vibrato", 100, 5},
		{"vibrato_delay", "ms", "time after each note starts before the vibrato begins", 10000, 0},
	}
	var out []Control
	for _, row := range rows {
		d := paramdefs.Descriptor{ID: "vibrato." + row.name, Path: row.name, Source: row.name, Scope: "track", Unit: row.unit, Min: 0, Max: row.max, Default: row.def, Curve: "linear"}
		c := fromDescriptor("", row.name, d)
		c.SetIn, c.Meaning = []string{InTrack}, row.meaning
		out = append(out, c)
	}
	return out
}

// keyboardPatches adds the 21 modeled keyboard patches. Each patch's controls,
// ranges and units come from keyboard.Parameters, its defaults from the
// patch's own spec, and its validation descriptor from the preset table that
// notation checks presets with.
func (b *builder) keyboardPatches() {
	for _, patch := range keyboard.Names {
		family := keyboard.FamilyOf(keyboard.ID(patch)).String()
		b.owner(Owner{Name: patch, Kind: KindPatch, Family: family, Meaning: patchMeaning(patch, family),
			ValidatedBy: "project.KeysSpecFromValues (project/keys.go) and host/keyboard.Validate over keyboard.Parameters (kernel/voice/keyboard/catalog.go)",
			ReadBy:      "host/keyboard.New, which builds kernel/voice/" + familyPackage(family) + " from the control table"})
		descriptors := map[string]paramdefs.Descriptor{}
		for _, d := range notation.PresetDescriptors(&notation.Score{}, "builtin."+patch) {
			if strings.HasPrefix(d.ID, "keys."+patch+".") {
				descriptors[d.Source] = d
			}
		}
		spec, _ := keyboardpresets.DefaultSpec(patch)
		roles := fmRoles(patch, spec.Controls)
		for _, p := range keyboard.Parameters(patch) {
			d := descriptors[p.Name]
			c := fromDescriptor(patch, p.Name, d)
			c.Unit = keyboardUnit(p.Unit)
			c.secondsPlain = p.Unit == "s"
			if c.secondsPlain {
				c.Note = "a plain number is seconds (0.3); a number with ms or s is converted (300ms)"
			}
			c.SetIn, c.Meaning = []string{InTrack}, keyboard.Meaning(patch, p.Name)
			if words := d.Values; len(words) > 0 && p.Integer && int(p.Max-p.Min)+1 == len(words) && p.Min == 0 {
				c.Type, c.Values, c.Default, c.Min, c.Max = "enum", words, wordAt(words, d.Default), nil, nil
			}
			if row, ok := trackRowFor(family, p.Name); ok {
				c.RegistryID, c.Live, c.SetIn = row.ID, row.Live, setIn(row, InTrack)
			}
			if role, ok := roles[p.Name]; ok {
				c.Meaning += "; in " + patch + ", " + role
			}
			if p.Name == "oversample" {
				c.Excluded = []float64{3}
			}
			b.add(c)
		}
		b.add(octaveControl(patch, 2))
	}
}

func patchMeaning(patch, family string) string {
	words := map[string]string{
		"ep": "modeled electric piano", "clav": "modeled clavinet", "organ": "modeled tonewheel organ with a rotary speaker",
		"fm": "six-operator FM synthesizer", "analog": "polyphonic analog synthesizer", "strings": "string machine",
	}
	return words[family] + " patch " + patch
}

func familyPackage(family string) string {
	if family == "analog" {
		return "polykeys"
	}
	return family
}

// keyboardUnit is the catalog unit of a keyboard control. Times are seconds in
// the patch table and milliseconds in scores and in the catalog.
func keyboardUnit(unit string) string {
	switch unit {
	case "s":
		return "ms"
	case "cents":
		return "cent"
	}
	return displayUnit(unit)
}

// fmRoles describes each operator of an FM patch from the routing and output
// controls the engine reads: whether it is heard, and which operators it
// modulates.
func fmRoles(patch string, controls [128]float32) map[string]string {
	if keyboard.FamilyOf(keyboard.ID(patch)).String() != "fm" {
		return nil
	}
	const operators = 6
	modulates := make([][]int, operators)
	index := 75
	for dst := 0; dst < operators-1; dst++ {
		for src := dst + 1; src < operators; src++ {
			if controls[index] != 0 {
				modulates[src] = append(modulates[src], dst+1)
			}
			index++
		}
	}
	roles := map[string]string{}
	for op := 0; op < operators; op++ {
		output := controls[3+op*12+11]
		var parts []string
		if output > 0 {
			parts = append(parts, fmt.Sprintf("operator %d is a carrier (heard at output %g), so its level sets its loudness", op+1, output))
		}
		if len(modulates[op]) > 0 {
			var targets []string
			for _, dst := range modulates[op] {
				targets = append(targets, fmt.Sprintf("%d", dst))
			}
			parts = append(parts, fmt.Sprintf("operator %d modulates operator %s, so its level sets how many upper harmonics it adds there", op+1, strings.Join(targets, " and ")))
		}
		if len(parts) == 0 {
			parts = append(parts, fmt.Sprintf("operator %d is neither heard nor routed to another operator, so it is silent until its output or a route is raised", op+1))
		}
		roles[fmt.Sprintf("op%d_level", op+1)] = strings.Join(parts, "; ")
	}
	return roles
}

// libraryPatches adds the built-in instrument graphs. Their parameters are
// read from each patch's own source, as addpreset writes it into a score.
func (b *builder) libraryPatches() {
	for _, patch := range instrument.Patches() {
		b.owner(Owner{Name: patch.ID, Kind: KindLibraryPatch, Family: "graph", Meaning: patch.Description,
			ValidatedBy: "instrument.Compile and instrument.ParameterLiteral (instrument/patches.go)", ReadBy: "the patch's instrument graph (kernel/graph)",
			Note: "addpreset copies the patch into the score as an instrument declaration; it then also takes the authored settings (octave, glide, vibrato)"})
		for slot, param := range PatchParams(patch) {
			b.add(authoredParam(patch.ID, slot, param, instrument.ParameterMeaning(param.Name)))
		}
	}
}

// PatchParams parses the parameters a library patch declares.
func PatchParams(patch instrument.Patch) []notation.InstrumentParam {
	source, err := patch.Source("patch")
	if err != nil {
		return nil
	}
	score, _ := notation.Parse([]byte("cicada 1\n" + source))
	if score == nil || len(score.Instruments) != 1 {
		return nil
	}
	return score.Instruments[0].Params
}

// AuthoredParam describes a parameter an instrument graph declares: it takes
// any finite number in its unit, and the first eight can change while playing.
func AuthoredParam(owner string, slot int, param notation.InstrumentParam, meaning string) Control {
	return authoredParam(owner, slot, param, meaning)
}

func authoredParam(owner string, slot int, param notation.InstrumentParam, meaning string) Control {
	value, unit, _ := paramdefs.ParseBase(param.Default)
	if param.Unit != "" {
		unit = strings.ToLower(param.Unit)
	}
	d := paramdefs.Authored(param.Name, unit, param.Default)
	c := Control{Owner: owner, Name: param.Name, Type: "number", Unit: displayUnit(unit), Default: value, descriptor: d,
		SetIn: []string{InInstrument, InTrack}, Meaning: meaning,
		Note: "an instrument graph declares no range: any finite number in its unit is accepted, and the graph's own functions limit it"}
	if slot < paramdefs.InstrumentParamSlots {
		live := paramdefs.InstrumentParamDescriptor(slot, param.Name, unit, value)
		c.Live, c.RegistryID = true, live.ID
		c.SetIn = append(c.SetIn, InScene, InAutomation, InModulation)
		c.Note += fmt.Sprintf("; scenes, automation and modulation take %s to %s", paramdefs.FormatBound(live, live.Min, false), paramdefs.FormatBound(live, live.Max, true))
	} else {
		c.Note += fmt.Sprintf("; only an instrument's first %d parameters can change while it plays", paramdefs.InstrumentParamSlots)
	}
	return c
}

// authored adds the settings every authored instrument graph takes.
func (b *builder) authored() {
	b.owner(Owner{Name: "authored", Kind: KindAuthored, Family: "graph", Meaning: "settings every instrument graph takes, in its declaration or its track block, besides the parameters it declares itself",
		ValidatedBy: "notation validTrackParam and VibratoSetting; project.Validate (project/validate.go)", ReadBy: "kernel/graph and the sequencer's pitch and glide"})
	b.add(octaveControl("authored", 2))
	glide := paramdefs.Descriptor{ID: "authored.glide", Path: "glide", Source: "glide", Scope: "track", Unit: "ms", Min: 0, Max: 10000, Default: 60, Curve: "linear"}
	c := fromDescriptor("authored", "glide", glide)
	c.SetIn, c.Meaning = []string{InInstrument, InTrack}, "pitch glide time between legato notes of a mono instrument"
	b.add(c)
	for _, setting := range vibratoSettings() {
		c := setting
		c.Owner = "authored"
		c.SetIn = []string{InInstrument, InTrack}
		b.add(c)
	}
}

// sampler adds the sampler voice's settings.
func (b *builder) sampler() {
	b.owner(Owner{Name: "sampler", Kind: KindSampler, Meaning: "a sampler voice that plays a recorded asset or sample pack, written in a sampler declaration",
		ValidatedBy: "notation sampler validation (notation/audio.go)", ReadBy: "kernel/voice/sample",
		Bindings: []string{"asset", "pack", "sha256"},
		Note:     "asset, pack and sha256 bind the recording and are not sound controls; a sampler track takes only mixer settings"})
	for _, d := range paramdefs.SamplerSettings {
		c := fromDescriptor("sampler", d.Source, d)
		c.SetIn, c.Meaning = []string{InSampler}, paramdefs.Meaning(d.ID)
		if d.Source == "root" {
			c.Type, c.Default, c.Values = "name", nil, nil
			c.Note = "an absolute pitch from c0 to b6, such as c3 or f#2"
		}
		if d.Source == "mode" {
			c.Default = "oneshot"
			c.Note = "a sample pack plays oneshot only"
		}
		b.add(c)
	}
	for _, env := range []struct {
		name, unit, meaning string
		min, max            float64
	}{
		{"attack", "ms", "time each note of a sample pack takes to reach full level", 0, 5000},
		{"decay", "ms", "time each note of a sample pack takes to fall to the sustain level", 0, 10000},
		{"sustain", "", "level a sample pack's note holds while it is down, as a fraction of full level", 0, 1},
		{"release", "ms", "time a sample pack's note takes to fade after it ends", 1, 10000},
	} {
		d := paramdefs.Descriptor{ID: "sampler." + env.name, Path: env.name, Source: env.name, Scope: "sampler", Unit: env.unit, Min: env.min, Max: env.max, Curve: "linear"}
		c := fromDescriptor("sampler", env.name, d)
		c.Default = nil
		c.SetIn, c.Meaning, c.Note = []string{InSampler}, env.meaning, "only with a pack; unset keeps the pack's own envelope"
		b.add(c)
	}
}

// kitLanes adds the controls of a declared kit's lanes (project.CompileKitTrackWithSamplers).
func (b *builder) kitLanes() {
	b.owner(Owner{Name: "kit_lane", Kind: KindKitLane, Meaning: "one lane of a declared kit, set in the track block as <lane>_<control>, such as bd_tune",
		ValidatedBy: "notation validTrackParam; project.CompileKitTrackWithSamplers (project/kit.go); modeledkit.Params.Validate", ReadBy: "kernel/voice/modeledkit for model.* lanes, kernel/voice/sample for pack lanes",
		Note: "a modeled lane (model.*) takes every control; a sample pack lane takes only level and pan; a lane bound to an instrument graph takes none"})
	for _, row := range []struct {
		name, unit, meaning string
		min, max, def       float64
		modeledOnly         bool
	}{
		{"tune", "", "pitch of the lane's modeled body as a ratio; 1 is unchanged", .5, 2, 1, true},
		{"decay", "", "ring time of the lane's modeled body as a ratio; 1 is unchanged", .25, 2, 1, true},
		{"position", "", "strike position from the center (0) to the edge (1)", 0, 1, .35, true},
		{"humanize", "", "random variation of each strike's position, level and pitch", 0, .1, .015, true},
		{"level", "dB", "level of the lane in the kit", -60, 6, 0, false},
		{"pan", "", "stereo position of the lane, from -1 (left) to 1 (right)", -1, 1, 0, false},
	} {
		d := paramdefs.Descriptor{ID: "kit_lane." + row.name, Path: "<lane>_" + row.name, Source: "<lane>_" + row.name, Scope: "track", Unit: row.unit, Min: row.min, Max: row.max, Default: row.def, Curve: "linear"}
		c := fromDescriptor("kit_lane", row.name, d)
		c.Path = ""
		c.SetIn, c.Meaning = []string{InTrack}, row.meaning
		c.Note = "written <lane>_" + row.name + ", such as bd_" + row.name
		if row.modeledOnly {
			c.Note += "; modeled lanes only"
		}
		b.add(c)
	}
}

// effects adds every effect kind. Registry rows and host-only rows give the
// ranges; ampcab and convolution, which have neither, are described here and
// checked against their compilers by this package's tests.
func (b *builder) effects() {
	routes := map[string]string{
		"drive": "a track insert", "eq": "a track or master insert", "comp": "a track, bus music or master insert",
		"deesser": "a track insert", "ampcab": "a track insert", "chorus": "a track insert",
		"transient": "a master insert", "width": "a master insert", "limiter": "a master insert", "convolution": "a master insert",
		"delay": "the delay send return", "reverb": "the reverb send return", "tape": "the delay send return (a tape echo replaces the delay)",
	}
	meanings := map[string]string{
		"drive": "saturator with a tone filter", "eq": "one filter band: peak, shelf or pass", "comp": "compressor",
		"deesser": "de-esser that reduces sibilance", "ampcab": "guitar amp and cabinet model", "chorus": "chorus",
		"transient": "transient shaper", "width": "stereo width and bass mono", "limiter": "true-peak limiter",
		"convolution": "convolution reverb from an impulse-response asset", "delay": "stereo delay", "reverb": "algorithmic reverb",
		"tape": "tape echo with wow, flutter and saturation",
	}
	readBy := map[string]string{
		"drive": "kernel/fx/drive.go", "eq": "kernel/fx/pro/eq.go", "comp": "kernel/fx/comp.go", "deesser": "kernel/fx/deesser.go",
		"ampcab": "kernel/fx/ampcab", "chorus": "kernel/fx/chorus", "transient": "kernel/fx/pro/transient.go", "width": "kernel/fx/pro/width.go",
		"limiter": "kernel/fx/pro/limiter.go", "convolution": "kernel/fx/convolution", "delay": "kernel/fx/delay.go", "reverb": "kernel/fx/reverb.go", "tape": "kernel/fx/tape",
	}
	validatedBy := map[string]string{
		"drive": "project.DriveParamsFromValues (project/drive.go)", "eq": "project compileTrackEffect and masterEffectParams", "comp": "project.CompSpecFromValues (project/comp.go)",
		"deesser": "project.DeesserParamsFromValues (project/track_effects.go)", "ampcab": "project.AmpCabParamsFromValues (project/track_effects.go)",
		"chorus": "project.ChorusParamsFromValues (project/track_effects.go)", "transient": "project masterEffectParams (project/master.go)",
		"width": "project masterEffectParams (project/master.go)", "limiter": "project masterEffectParams (project/master.go)",
		"convolution": "project convolutionSpec (project/master.go)", "delay": "project.DelayParamsFromValues (project/delay.go)",
		"reverb": "project.ReverbParamsFromValues (project/reverb.go)", "tape": "project.TapeParamsFromValues (project/tape.go)",
	}
	kinds := []string{"drive", "delay", "reverb", "comp", "eq", "deesser", "chorus", "tape", "ampcab", "transient", "width", "limiter", "convolution"}
	liveNote := "scenes, automation and modulation reach these controls only on a send return, a track's only insert when it is a drive, or the bus music compressor; other inserted effects are fixed until the score is recompiled"
	for _, kind := range kinds {
		owner := Owner{Name: kind, Kind: KindEffect, Meaning: meanings[kind] + ", used as " + routes[kind], ValidatedBy: validatedBy[kind], ReadBy: readBy[kind]}
		if kind == "convolution" {
			owner.Bindings = []string{"asset"}
			owner.Note = "asset names the impulse-response recording and is not a sound control"
		}
		b.owner(owner)
		rows := 0
		for _, d := range append(append([]paramdefs.Descriptor(nil), paramdefs.Registry...), paramdefs.HostOnly...) {
			if !strings.HasPrefix(d.ID, "fx."+kind+".") {
				continue
			}
			if def, ok := hostOnlyDefaults()[d.ID]; ok {
				d.Default = def
			}
			c := fromDescriptor(kind, d.Source, d)
			c.Path = ""
			c.Live, c.SetIn, c.Meaning = d.Live, setIn(d, InEffect), paramdefs.Meaning(d.ID)
			if _, inRegistry := registryRow(d.ID); inRegistry {
				c.RegistryID = d.ID
			}
			if d.Live {
				c.Note = liveNote
			}
			if d.ID == "fx.eq.frequency" {
				master, _ := paramdefs.DeviceParam("eq", "frequency", true)
				c.RouteMax = map[string]float64{"master": master.Max}
				c.Note = "a master EQ reaches " + paramdefs.FormatBound(master, master.Max, true)
			}
			switch d.ID {
			case "fx.delay.time":
				c.Default = divisionWord(d.Values, fx.DefaultDelayParams().Division)
				c.Note = "the default is a note division, so the time follows the tempo; " + liveNote
			case "fx.tape.time":
				c.Default = divisionWord(d.Values, tape.DefaultParams().Division)
				c.Note = "the default is a note division, so the time follows the tempo; " + liveNote
			case "fx.comp.makeup":
				if fx.DefaultCompParams().MakeupAuto {
					c.Default = "auto"
				}
			}
			if d.ID == "fx.comp.sidechain" {
				c.Type, c.Default, c.Values = "name", nil, nil
				c.Note = "music, sfx or the name of a track; only the bus music compressor takes a sidechain"
			}
			b.add(c)
			rows++
		}
		switch kind {
		case "ampcab":
			amp := ampcab.DefaultParams()
			for _, row := range []struct {
				name, unit, meaning string
				min, max, def       float64
				typ                 string
			}{
				{"drive", "", "preamp drive; higher values add distortion", 0, 1, amp.Drive, "number"},
				{"tone", "", "tone stack, from dark (0) to bright (1)", 0, 1, amp.Tone, "number"},
				{"output", "dB", "output level after the cabinet", -24, 12, amp.Output, "number"},
				{"cabinet", "", "cabinet model: 0, 1 or 2", 0, 2, float64(amp.Cabinet), "integer"},
			} {
				d := paramdefs.Descriptor{ID: "fx.ampcab." + row.name, Path: "ampcab." + row.name, Source: row.name, Scope: "global", Unit: row.unit, Min: row.min, Max: row.max, Default: row.def, Curve: "linear", Type: row.typ}
				c := fromDescriptor(kind, row.name, d)
				c.Path, c.SetIn, c.Meaning = "", []string{InEffect}, row.meaning
				if row.name != "cabinet" {
					c.Note = "the track paths amp_" + row.name + " set it from scenes, automation and modulation"
				}
				b.add(c)
			}
			bypass := paramdefs.Descriptor{ID: "fx.ampcab.bypass", Source: "bypass", Scope: "global", Curve: "toggle", Values: []string{"off", "on"}}
			c := fromDescriptor(kind, "bypass", bypass)
			c.SetIn, c.Meaning = []string{InEffect}, "on passes the signal through unprocessed"
			b.add(c)
		case "convolution":
			mix := paramdefs.Descriptor{ID: "fx.convolution.mix", Source: "mix", Scope: "global", Min: 0, Max: 1, Default: .15, Curve: "linear"}
			c := fromDescriptor(kind, "mix", mix)
			c.SetIn, c.Meaning = []string{InEffect}, "balance between the convolved (1) and dry (0) signal"
			b.add(c)
			partition := paramdefs.Descriptor{ID: "fx.convolution.partition", Source: "partition", Scope: "global", Unit: "frames", Min: 64, Max: 2048, Default: 128, Curve: "integer", Type: "integer"}
			c = fromDescriptor(kind, "partition", partition)
			c.SetIn, c.Meaning = []string{InEffect}, "block size of the partitioned convolution, a power of two; larger blocks cost less CPU and add latency"
			c.Values = nil
			c.Note = "a power of two from 64 to 2048 frames"
			b.add(c)
		}
	}
}

// hostOnlyDefaults reads the master processors' defaults from kernel/fx/pro,
// which the host-only rows leave out.
func hostOnlyDefaults() map[string]float64 {
	p := pro.DefaultParams()
	return map[string]float64{
		"fx.transient.attack": p.Transient.AttackDB, "fx.transient.sustain": p.Transient.SustainDB,
		"fx.transient.fast": p.Transient.FastMs, "fx.transient.slow": p.Transient.SlowMs, "fx.transient.release": p.Transient.ReleaseMs,
		"fx.width.amount": p.Width.Amount, "fx.width.bass_mono": p.Width.BassMonoHz,
		"fx.limiter.ceiling": p.Limiter.CeilingDBTP, "fx.limiter.lookahead": p.Limiter.LookaheadMs, "fx.limiter.release": p.Limiter.ReleaseMs,
	}
}

// divisionWord names a delay division with the word a score writes for it.
func divisionWord(words []string, division fx.DelayDivision) any {
	for _, word := range words {
		if parsed, err := fx.ParseDelayDivision(word); err == nil && parsed == division {
			return word
		}
	}
	return nil
}
