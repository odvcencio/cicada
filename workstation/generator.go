package main

import (
	"fmt"
	"math"
	"strconv"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/phrase"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/workstation/ui"
	"m31labs.dev/gosx"
	"m31labs.dev/gosx/action"
	"m31labs.dev/gosx/server"
	"m31labs.dev/gosx/session"
)

var phraseDefaults = map[string]string{"seed": "4242", "key": "a", "scale": "minor", "structure": "aaba", "rootOctave": "2", "steps": "16", "density": "0.6", "accentDensity": "0.5", "slideDensity": "0.4", "octaveJump": "0.3", "swing": "54", "gate": "55"}
var phraseKeys = []string{"c", "c#", "d", "d#", "e", "f", "f#", "g", "g#", "a", "a#", "b"}
var phraseScales = []string{"minor", "phrygian", "dorian", "harmonic", "pent", "major", "mixo", "blues"}
var phraseStructures = []string{"a", "aaba", "abab", "abac", "aaab"}

func choice(value string, choices []string) (int, error) {
	for i, item := range choices {
		if item == value {
			return i, nil
		}
	}
	return 0, fmt.Errorf("choose a supported option")
}

func phraseParams(form map[string]string) (phrase.Params, error) {
	p := phrase.DefaultParams()
	seed, err := strconv.ParseUint(form["seed"], 10, 64)
	if err != nil {
		return p, fmt.Errorf("seed must be an unsigned 64-bit integer")
	}
	p.Seed = seed
	key, err := choice(form["key"], phraseKeys)
	if err != nil {
		return p, err
	}
	p.Key = uint8(key)
	scale, err := choice(form["scale"], phraseScales)
	if err != nil {
		return p, err
	}
	p.Scale = phrase.Scale(scale)
	structure, err := choice(form["structure"], phraseStructures)
	if err != nil {
		return p, err
	}
	p.Structure = phrase.Structure(structure + 1)
	for _, item := range []struct {
		name     string
		min, max int
		dest     *uint8
	}{{"rootOctave", 1, 4, &p.RootOctave}, {"steps", 8, 64, &p.Steps}, {"gate", 10, 100, &p.GatePercent}} {
		n, err := integer(form, item.name)
		if err != nil || n < item.min || n > item.max {
			return p, fmt.Errorf("%s must be %d to %d", item.name, item.min, item.max)
		}
		*item.dest = uint8(n)
	}
	for _, item := range []struct {
		name string
		dest *float32
	}{{"density", &p.Density}, {"accentDensity", &p.AccentDensity}, {"slideDensity", &p.SlideDensity}, {"octaveJump", &p.OctaveJump}} {
		n, err := strconv.ParseFloat(form[item.name], 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 1 {
			return p, fmt.Errorf("%s must be between 0 and 1", item.name)
		}
		*item.dest = float32(n)
	}
	swing, err := strconv.ParseFloat(form["swing"], 64)
	if err != nil || math.IsNaN(swing) || math.IsInf(swing, 0) || swing < 50 || swing > 75 {
		return p, fmt.Errorf("swing must be 50 to 75 percent")
	}
	p.SwingPercent100 = uint16(math.Round(swing * 100))
	p.RestDownbeat = form["restDownbeat"] == "on"
	return p, nil
}

func (s *studioApp) generatePhrase(ctx *action.Context) error {
	p, err := phraseParams(ctx.FormData)
	if err != nil {
		return action.Validation(err.Error(), nil, ctx.FormData)
	}
	result, err := phrase.Generate(p)
	if err != nil {
		return action.Validation(err.Error(), nil, ctx.FormData)
	}
	receipt, err := s.storeDraft(result.Notation, ctx.FormData["revision"], session.Token(ctx.Request), "phrase")
	if err != nil {
		return err
	}
	store := session.Current(ctx.Request)
	store.Set("phrase-preview", receipt)
	store.Delete("phrase-locks")
	values := make(map[string]string, len(phraseDefaults)+1)
	values["seed"], values["key"], values["scale"], values["structure"] = strconv.FormatUint(p.Seed, 10), phraseKeys[p.Key], phraseScales[p.Scale], phraseStructures[p.Structure-1]
	values["rootOctave"], values["steps"], values["gate"] = strconv.Itoa(int(p.RootOctave)), strconv.Itoa(int(p.Steps)), strconv.Itoa(int(p.GatePercent))
	for name, n := range map[string]float64{"density": float64(p.Density), "accentDensity": float64(p.AccentDensity), "slideDensity": float64(p.SlideDensity), "octaveJump": float64(p.OctaveJump), "swing": float64(p.SwingPercent100) / 100} {
		values[name] = strconv.FormatFloat(n, 'f', 4, 64)
	}
	values["restDownbeat"] = ctx.FormData["restDownbeat"]
	store.Set("phrase-params", values)
	ctx.FormData = nil
	ctx.RedirectBackWithMessage("/?panel=generator", "Phrase preview is ready. The open score has not changed.")
	return nil
}

func (s *studioApp) generator(ctx *server.Context, v workspace, csrf string) gosx.Node {
	values := make(map[string]string, len(phraseDefaults))
	for name, value := range phraseDefaults {
		values[name] = value
	}
	store := session.Current(ctx.Request)
	_ = store.Decode("phrase-params", &values)
	controls := []gosx.Node{field("Seed", textInput("seed", values["seed"])), field("Key", selectInput("key", values["key"], phraseKeys)), field("Scale", selectInput("scale", values["scale"], phraseScales)), field("Structure", selectInput("structure", values["structure"], phraseStructures)), field("Root octave", numberInput("rootOctave", values["rootOctave"], "1", "4")), field("Steps", selectInput("steps", values["steps"], []string{"8", "16", "32", "64"}))}
	for _, item := range []struct{ name, label, min, max string }{{"density", "Note density", "0", "1"}, {"accentDensity", "Accent density", "0", "1"}, {"slideDensity", "Slide density", "0", "1"}, {"octaveJump", "Octave jumps", "0", "1"}, {"swing", "Swing (%)", "50", "75"}, {"gate", "Gate (%)", "10", "100"}} {
		controls = append(controls, field(item.label, numberInput(item.name, values[item.name], item.min, item.max)))
	}
	attrs := gosx.Attrs(gosx.Attr("type", "checkbox"), gosx.Attr("name", "restDownbeat"))
	if values["restDownbeat"] == "on" {
		attrs = append(attrs, gosx.BoolAttr("checked"))
	}
	controls = append(controls, field("Allow downbeat rest", gosx.El("input", attrs)), submit("", "", "Preview phrase"))
	form := ui.Form(ui.FormProps{Action: "/__actions/generate", CSRF: csrf, Revision: v.Revision, ReturnTo: "/?panel=generator", Class: "action-form generator-form"}, controls...)
	preview := gosx.Fragment()
	if generated, ok := s.draft(store.String("phrase-preview"), session.Token(ctx.Request)); ok {
		preview = gosx.Fragment(gosx.El("h3", gosx.Text("Generated score preview")), gosx.El("pre", gosx.Attrs(gosx.Attr("class", "history-diff")), gosx.Text(generated.Source)), s.mutationControls(v, csrf, values, store.String("phrase-locks"), generated.Source), s.form(workspace{Revision: generated.Revision}, csrf, "generator", "source", hidden("content", generated.Source), submit("", "", "Replace open score with this phrase")), gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Applying replaces the complete score. Undo restores the previous score.")))
	}
	return ui.Panel(ui.PanelProps{ID: "generator", Title: "Phrase generator", Description: "Deterministic acid phrases across eight scales. Preview before applying."}, form, preview)
}

var mutationNames = []string{"nudge", "accent", "slide", "octave", "rotate", "swap", "fill", "thin", "ratchet"}

func previewBar(source string) (seq.Pattern, error) {
	score, diagnostics := notation.Parse([]byte(source))
	if score == nil || len(score.Patterns) == 0 || len(score.Tracks) == 0 {
		return seq.Pattern{}, fmt.Errorf("phrase preview is unavailable; generate it again")
	}
	for _, d := range diagnostics {
		if d.Severity == "error" {
			return seq.Pattern{}, fmt.Errorf("phrase preview is invalid")
		}
	}
	bars, err := project.CompilePattern(score, score.Patterns[0], score.Tracks[0])
	if err != nil || len(bars) == 0 {
		return seq.Pattern{}, fmt.Errorf("phrase preview cannot compile")
	}
	return bars[0].Pattern, nil
}

func (s *studioApp) mutatePhrase(ctx *action.Context) error {
	store := session.Current(ctx.Request)
	generated, ok := s.draft(store.String("phrase-preview"), session.Token(ctx.Request))
	if !ok {
		return action.Validation("Generate a preview before mutating it.", nil, nil)
	}
	values := map[string]string{}
	if !store.Decode("phrase-params", &values) {
		return action.Validation("Generate a preview again.", nil, nil)
	}
	p, err := phraseParams(values)
	if err != nil {
		return err
	}
	p.Seed, err = strconv.ParseUint(ctx.FormData["seed"], 10, 64)
	if err != nil {
		return action.Validation("Mutation seed must be an unsigned 64-bit integer.", nil, ctx.FormData)
	}
	base, err := previewBar(generated.Source)
	if err != nil {
		return err
	}
	var locks uint64
	for i := uint8(0); i < base.Len; i++ {
		if ctx.FormData[fmt.Sprintf("lock-%d", i)] == "1" {
			locks |= uint64(1) << i
		}
	}
	store.Set("phrase-locks", strconv.FormatUint(locks, 16))
	var result phrase.Result
	if ctx.FormData["mode"] == "evolve" {
		generation, e := strconv.ParseUint(ctx.FormData["generation"], 10, 64)
		if e != nil {
			return action.Validation("Generation must be an unsigned whole number.", nil, ctx.FormData)
		}
		result, err = phrase.Evolve(p, base, generation, locks)
	} else {
		kind, e := choice(ctx.FormData["operation"], mutationNames)
		if e != nil {
			return action.Validation(e.Error(), nil, ctx.FormData)
		}
		rotation, e := integer(ctx.FormData, "rotation")
		if e != nil {
			return action.Validation(e.Error(), nil, ctx.FormData)
		}
		result, err = phrase.Mutate(p, base, []phrase.Op{{Kind: phrase.OpKind(kind + 1), Arg: rotation}}, locks)
	}
	if err != nil {
		return action.Validation(err.Error(), nil, ctx.FormData)
	}
	updated, err := edits.ReplacePreviewBar(generated.Source, result.Notation)
	if err != nil {
		return err
	}
	score, _ := notation.Parse([]byte(updated))
	if p, diagnostics := project.FromScore(score); p == nil {
		return fmt.Errorf("variation is invalid: %v", diagnostics)
	}
	receipt, err := s.storeDraft(updated, generated.Revision, session.Token(ctx.Request), "phrase")
	if err != nil {
		return err
	}
	store.Set("phrase-preview", receipt)
	values["seed"] = strconv.FormatUint(p.Seed, 10)
	store.Set("phrase-params", values)
	ctx.FormData = nil
	ctx.RedirectBackWithMessage("/?panel=generator", "Variation is ready. Locked steps were preserved; the open score has not changed.")
	return nil
}

func (s *studioApp) mutationControls(v workspace, csrf string, values map[string]string, mask, source string) gosx.Node {
	base, err := previewBar(source)
	if err != nil {
		return failure(err)
	}
	locks, _ := strconv.ParseUint(mask, 16, 64)
	checks := []gosx.Node{gosx.El("legend", gosx.Text("Locked steps"))}
	for i := uint8(0); i < base.Len; i++ {
		attrs := gosx.Attrs(gosx.Attr("type", "checkbox"), gosx.Attr("name", fmt.Sprintf("lock-%d", i)), gosx.Attr("value", "1"))
		if locks&(uint64(1)<<i) != 0 {
			attrs = append(attrs, gosx.BoolAttr("checked"))
		}
		checks = append(checks, field(strconv.Itoa(int(i)+1), gosx.El("input", attrs)))
	}
	return gosx.El("details", gosx.El("summary", gosx.Text("Mutate or evolve the first bar")), s.form(v, csrf, "generator", "mutate", field("Mutation seed", textInput("seed", values["seed"])), gosx.El("fieldset", gosx.Attrs(gosx.Attr("class", "lock-grid")), gosx.Fragment(checks...)), field("Operation", selectInput("operation", "nudge", mutationNames)), field("Rotation (steps)", numberInput("rotation", "2", "-63", "63")), field("Generation", textInput("generation", "0")), submit("mode", "mutate", "Mutate bar"), submit("mode", "evolve", "Evolve bar")))
}
