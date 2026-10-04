package notation

import (
	"strconv"
	"strings"

	gts "github.com/odvcencio/gotreesitter"
	"m31labs.dev/cicada/internal/paramdefs"
)

func parsePreset(w *loweringWalker, n *gts.Node) Preset {
	p := Preset{Name: w.declaration(w.Field(n, "name")), Position: w.position(n), TargetPosition: w.position(n)}
	for i := 0; i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		if w.Type(c) != "param_decl" {
			continue
		}
		param := parseParam(w, c)
		if param.Name == "instrument" {
			if p.Target != "" {
				*w.diagnostics = append(*w.diagnostics, Diagnostic{Code: "CICADA-DUPLICATE", Severity: "error", Message: "preset declares instrument twice", Position: param.Position})
			}
			p.Target = w.referenceText(param.Value, param.ValuePosition)
			p.TargetPosition = param.ValuePosition
		} else {
			p.Params = append(p.Params, param)
		}
	}
	return p
}

// PresetTarget describes an existing voice or effect without changing its graph.
func PresetTarget(s *Score, target string) (kind string, found bool) {
	switch target {
	case "builtin.delay", "builtin.reverb", "builtin.drive", "builtin.comp":
		return "effect", true
	case "acid", "drums", "audio":
		return target, true
	}
	if strings.HasPrefix(target, "builtin.") && drumParams[strings.TrimPrefix(target, "builtin.")] != nil {
		return "lane", true
	}
	for _, i := range s.Instruments {
		if i.Name == target {
			return "instrument", true
		}
	}
	for _, k := range s.Kits {
		if k.Name == target {
			return "kit", true
		}
	}
	for _, v := range s.Samplers {
		if v.Name == target {
			return "sampler", true
		}
	}
	for _, e := range s.Effects {
		if e.Name == target {
			return "effect", true
		}
	}
	return "", false
}

// PresetDescriptors uses the shared registry for built-ins and mixer values,
// with host-only descriptors for authored and sampler parameters.
func PresetDescriptors(s *Score, target string) []paramdefs.Descriptor {
	kind, _ := PresetTarget(s, target)
	voice := kind
	if kind == "kit" || kind == "audio" || kind == "sampler" {
		voice = "instrument"
	}
	var descriptors []paramdefs.Descriptor
	for _, d := range paramdefs.Registry {
		if kind == "effect" {
			if d.ID == "fx.comp.sidechain" {
				continue
			}
			if strings.HasPrefix(target, "builtin.") && strings.HasPrefix(d.ID, "fx."+strings.TrimPrefix(target, "builtin.")+".") {
				descriptors = append(descriptors, d)
			}
			for _, effect := range s.Effects {
				if effect.Name == target && strings.HasPrefix(d.ID, "fx."+effect.Kind+".") {
					descriptors = append(descriptors, d)
				}
			}
			continue
		}
		if kind == "lane" {
			lane := strings.TrimPrefix(target, "builtin.")
			if strings.HasPrefix(d.ID, "drum."+lane+".") {
				d.Source = strings.TrimPrefix(d.Source, lane+"_")
				d.Path = d.Source
				descriptors = append(descriptors, d)
			}
			continue
		}
		if d.ID == "mix.insert" || d.ID == "mix.bus" {
			continue
		}
		if d.Scope == "track" {
			for _, v := range d.Voices {
				if v == voice {
					descriptors = append(descriptors, d)
					break
				}
			}
		}
	}
	if kind == "instrument" {
		for _, inst := range s.Instruments {
			if inst.Name == target {
				for _, p := range inst.Params {
					descriptors = append(descriptors, paramdefs.Authored(p.Name, p.Unit, p.Default))
				}
			}
		}
	}
	hasOctaveParameter := false
	for _, inst := range s.Instruments {
		if inst.Name == target {
			for _, p := range inst.Params {
				hasOctaveParameter = hasOctaveParameter || p.Name == "octave"
			}
		}
	}
	if kind == "acid" || kind == "instrument" && !hasOctaveParameter {
		descriptors = append(descriptors, paramdefs.Descriptor{ID: "source.octave", Path: "octave", Source: "octave", Unit: "unit", Min: 0, Max: 6, Default: 3, Curve: "integer"})
	}
	if kind == "sampler" {
		descriptors = append(descriptors, paramdefs.SamplerSettings...)
	}
	return descriptors
}

func validatePresetParams(s *Score, target string, params []Param) []Diagnostic {
	descriptors := PresetDescriptors(s, target)
	var ds []Diagnostic
	seen := map[string]bool{}
	for _, p := range params {
		if seen[p.Name] {
			ds = append(ds, Diagnostic{Code: "CICADA-DUPLICATE", Severity: "error", Message: "duplicate preset parameter " + p.Name, Position: p.Position})
		}
		seen[p.Name] = true
		found := false
		// Authored declarations take precedence over generic mixer descriptors.
		for i := len(descriptors) - 1; i >= 0; i-- {
			d := descriptors[i]
			if d.Source != p.Name {
				continue
			}
			found = true
			if code, err := paramdefs.ValidateLiteral(d, p.Value); err != nil {
				ds = append(ds, Diagnostic{Code: code, Severity: "error", Message: err.Error(), Position: p.ValuePosition})
			}
			break
		}
		if !found {
			ds = append(ds, Diagnostic{Code: "CICADA-PRESET-PARAM", Severity: "error", Message: "unknown parameter " + p.Name + " for " + target, Position: p.Position})
		}
	}
	return ds
}

func mergePresetParams(defaults, overrides []Param) []Param {
	result := append([]Param(nil), defaults...)
	for _, p := range overrides {
		key := func(q Param) bool { return q.Name == p.Name && q.Target == p.Target }
		replaced := false
		for i, q := range result {
			if key(q) {
				result[i] = p
				replaced = true
				break
			}
		}
		if !replaced {
			result = append(result, p)
		}
	}
	return result
}

// ResolvePresets returns a copy with bindings lowered to ordinary values.
// The source score remains intact for formatting, explain, and editor writes.
func ResolvePresets(source *Score) (*Score, []Diagnostic) {
	if source == nil || len(source.Presets) == 0 && len(source.Origins) == 0 {
		return source, nil
	}
	s := *source
	s.Presets = nil
	s.Tracks = append([]Track(nil), source.Tracks...)
	s.Effects = append([]Effect(nil), source.Effects...)
	s.Instruments = append([]Instrument(nil), source.Instruments...)
	s.Kits = append([]Kit(nil), source.Kits...)
	s.Samplers = append([]Sampler(nil), source.Samplers...)
	presets := map[string]Preset{}
	var ds []Diagnostic
	fail := func(message string, p Position) {
		ds = append(ds, Diagnostic{Code: "CICADA-PRESET-TARGET", Severity: "error", Message: message, Position: p})
	}
	for _, p := range source.Presets {
		if old, ok := presets[p.Name]; ok {
			ds = append(ds, Diagnostic{Code: "CICADA-DUPLICATE", Severity: "error", Message: "duplicate preset " + p.Name, Position: p.Position, Related: old.Position})
		}
		presets[p.Name] = p
		if _, ok := PresetTarget(source, p.Name); ok {
			ds = append(ds, Diagnostic{Code: "CICADA-DUPLICATE", Severity: "error", Message: "preset name conflicts with target " + p.Name, Position: p.Position})
		}
		if _, ok := PresetTarget(source, p.Target); !ok {
			fail("preset requires an existing instrument, kit, sampler, built-in voice, or effect: "+p.Target, p.TargetPosition)
			continue
		}
		ds = append(ds, validatePresetParams(source, p.Target, p.Params)...)
	}
	for i, t := range s.Tracks {
		p, ok := presets[t.Kind]
		if !ok {
			continue
		}
		kind, valid := PresetTarget(source, p.Target)
		if !valid || kind == "effect" {
			fail("track requires a voice preset", t.Position)
			continue
		}
		var valueOverrides []Param
		for _, q := range t.Params {
			if q.Name != "send" && q.Name != "insert" && q.Name != "out" && q.Name != "bus" && q.Name != "send_a" && q.Name != "send_b" && q.Name != "send_pre" {
				valueOverrides = append(valueOverrides, q)
			}
		}
		ds = append(ds, validatePresetParams(source, p.Target, valueOverrides)...)
		t.Kind = p.Target
		t.Params = mergePresetParams(p.Params, t.Params)
		if kind == "lane" {
			lane := strings.TrimPrefix(p.Target, "builtin.")
			t.Kind = "drums"
			for j := range t.Params {
				if t.Params[j].Name != "send" && t.Params[j].Name != "insert" && t.Params[j].Name != "out" && t.Params[j].Name != "bus" {
					t.Params[j].Name = lane + "_" + t.Params[j].Name
				}
			}
		}
		if kind == "sampler" {
			for _, sampler := range source.Samplers {
				if sampler.Name != p.Target {
					continue
				}
				sampler.Name = t.Name + ".__sampler"
				var settings, mixer []Param
				for _, q := range t.Params {
					if q.Name == "mode" || q.Name == "voices" || q.Name == "root" {
						settings = append(settings, q)
					} else {
						mixer = append(mixer, q)
					}
				}
				sampler.Params = mergePresetParams(sampler.Params, settings)
				s.Samplers = append(s.Samplers, sampler)
				t.Kind = sampler.Name
				t.Params = mixer
			}
		}
		s.Tracks[i] = t
	}
	for i, e := range s.Effects {
		p, ok := presets[e.Kind]
		if !ok {
			continue
		}
		kind, valid := PresetTarget(source, p.Target)
		if !valid || kind != "effect" {
			fail("fx requires an effect preset", e.Position)
			continue
		}
		ds = append(ds, validatePresetParams(source, p.Target, e.Params)...)
		if strings.HasPrefix(p.Target, "builtin.") {
			e.Kind = strings.TrimPrefix(p.Target, "builtin.")
			e.Params = mergePresetParams(p.Params, e.Params)
		}
		for _, target := range source.Effects {
			if target.Name == p.Target {
				e.Kind = target.Kind
				e.Params = mergePresetParams(mergePresetParams(target.Params, p.Params), e.Params)
			}
		}
		for j := range e.Params {
			if e.Params[j].Name == "pingpong" {
				if e.Params[j].Value == "on" {
					e.Params[j].Value = "true"
				}
				if e.Params[j].Value == "off" {
					e.Params[j].Value = "false"
				}
			}
		}
		s.Effects[i] = e
	}
	// An unreferenced effect used as a preset target supplies defaults. Keep
	// every routed or scene-addressed instance, including the original target.
	prototypes := map[string]bool{}
	for _, e := range source.Effects {
		if p, ok := presets[e.Kind]; ok {
			prototypes[p.Target] = true
		}
	}
	routed := map[string]bool{}
	collect := func(params []Param) {
		for _, q := range params {
			value, _ := strconv.ParseFloat(q.Value, 64)
			if (q.Name == "send_a" || q.Name == "send_b") && value > 0 {
				kind := "delay"
				if q.Name == "send_b" {
					kind = "reverb"
				}
				for _, effect := range s.Effects {
					if effect.Kind == kind {
						routed[effect.Name] = true
					}
				}
			}
			if q.Target != "" {
				routed[q.Target] = true
			}
			if q.Name == "insert" {
				for _, name := range strings.Split(q.Value, "->") {
					routed[strings.TrimSpace(name)] = true
				}
			}
		}
	}
	for _, t := range s.Tracks {
		collect(t.Params)
	}
	for _, b := range s.Buses {
		collect(b.Params)
	}
	collect(s.Master)
	for _, scene := range s.Scenes {
		for _, q := range scene.Settings {
			for _, effect := range s.Effects {
				if strings.HasPrefix(q.Path, effect.Name+".") {
					routed[effect.Name] = true
				}
			}
		}
	}
	effects := make([]Effect, 0, len(s.Effects))
	for _, e := range s.Effects {
		// Imported effect declarations are available prototypes. Only routed
		// or scene-addressed instances belong to this score's audio graph.
		if (!prototypes[e.Name] && source.Origins[e.Name].Library == "") || routed[e.Name] {
			effects = append(effects, e)
		}
	}
	s.Effects = effects
	// Kit lanes may use authored voice presets. A specialized declaration keeps
	// the same expressions and changes only parameter defaults.
	for i, k := range s.Kits {
		k.Bindings = append([]KitBinding(nil), k.Bindings...)
		for j, b := range k.Bindings {
			p, ok := presets[b.Target]
			if !ok {
				continue
			}
			kind, _ := PresetTarget(source, p.Target)
			if kind != "instrument" {
				fail("kit lane requires an authored instrument preset; use a drums track for built-in lane values", b.Position)
				continue
			}
			for _, inst := range source.Instruments {
				if inst.Name != p.Target {
					continue
				}
				for _, q := range p.Params {
					declared := false
					for _, param := range inst.Params {
						declared = declared || param.Name == q.Name
					}
					if !declared {
						fail("kit lane preset can only change declared instrument parameters: "+q.Name, q.Position)
					}
				}
				inst.Name = p.Name
				inst.Params = append([]InstrumentParam(nil), inst.Params...)
				for n := range inst.Params {
					for _, q := range p.Params {
						if q.Name == inst.Params[n].Name {
							inst.Params[n].Default = q.Value
						}
					}
				}
				exists := false
				for _, v := range s.Instruments {
					exists = exists || v.Name == inst.Name
				}
				if !exists {
					s.Instruments = append(s.Instruments, inst)
				}
				k.Bindings[j].Target = inst.Name
			}
		}
		s.Kits[i] = k
	}
	s.Assets, s.Clips, s.Samplers, _ = resolveAudio(&s)
	LocateDiagnostics(ds, source.Position)
	return &s, ds
}

// FindPreset returns the source declaration used by tooling.
func FindPreset(s *Score, name string) (Preset, bool) {
	for _, p := range s.Presets {
		if p.Name == name {
			return p, true
		}
	}
	return Preset{}, false
}
