package controls_test

import (
	"reflect"
	"strings"
	"testing"

	"m31labs.dev/cicada/internal/controls"
	"m31labs.dev/cicada/internal/paramdefs"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// value reads a source literal as the typed project does.
func value(literal string) project.Value {
	n, unit, err := paramdefs.ParseBase(literal)
	if err != nil {
		return project.Value{Unit: "enum", Text: literal}
	}
	return project.Value{Unit: unit, Number: &n}
}

// compiled returns what a compiler builds from one setting, or from none
// when literal is empty. ok is false for an owner this test does not cover.
func compiled(owner controls.Owner, c controls.Control, literal string) (any, bool, error) {
	values := map[string]project.Value{}
	params := []notation.Param{}
	if literal != "" {
		values[c.Name] = value(literal)
		params = append(params, notation.Param{Name: c.Name, Value: literal})
	}
	effect := map[string]func(map[string]project.Value) (any, error){
		"drive":   func(v map[string]project.Value) (any, error) { return project.DriveParamsFromValues(v) },
		"delay":   func(v map[string]project.Value) (any, error) { return project.DelayParamsFromValues(v) },
		"reverb":  func(v map[string]project.Value) (any, error) { return project.ReverbParamsFromValues(v) },
		"chorus":  func(v map[string]project.Value) (any, error) { return project.ChorusParamsFromValues(v) },
		"deesser": func(v map[string]project.Value) (any, error) { return project.DeesserParamsFromValues(v) },
		"ampcab":  func(v map[string]project.Value) (any, error) { return project.AmpCabParamsFromValues(v) },
		"tape":    func(v map[string]project.Value) (any, error) { return project.TapeParamsFromValues(v) },
		"comp": func(v map[string]project.Value) (any, error) {
			p, _, err := project.CompSpecFromValues(v)
			return p, err
		},
	}
	switch owner.Kind {
	case controls.KindPatch:
		if c.Name == "octave" {
			return nil, false, nil
		}
		spec, err := project.KeysSpecFromValues(owner.Name, values)
		return spec, true, err
	case controls.KindEffect:
		compile, ok := effect[owner.Name]
		if !ok || c.Name == "sidechain" {
			return nil, false, nil
		}
		v, err := compile(values)
		return v, true, err
	case controls.KindVoice:
		track := notation.Track{Kind: owner.Name, Params: params}
		switch owner.Name {
		case "acid":
			if c.Name == "octave" {
				return nil, false, nil
			}
			v, err := project.CompileAcidParams(track)
			return v, true, err
		case "drums":
			v, err := project.CompileDrumParams(track)
			return v, true, err
		case "guitar":
			if c.Name == "octave" || c.Name == "experimental" {
				return nil, false, nil
			}
			track.Params = append(track.Params, notation.Param{Name: "experimental", Value: "on"})
			v, err := project.CompileGuitarParams(track)
			return v, true, err
		case "piano":
			if c.Name != "sustain" {
				return nil, false, nil
			}
			v, err := project.PianoSustainFromValues(values)
			return v, true, err
		}
		if strings.HasPrefix(owner.Name, "model_") && c.Name == "sustain" {
			v, err := project.CompileModalSustain(track)
			return v, true, err
		}
	}
	return nil, false, nil
}

// Writing a control at the default the catalog states builds exactly what
// leaving it out builds, so the default an agent reads is the one in effect.
func TestCatalogDefaultsAreTheDefaultsInEffect(t *testing.T) {
	catalog := controls.All()
	checked := 0
	for _, c := range catalog.Controls {
		owner, _ := catalog.Owner(c.Owner)
		if c.Default == nil || c.Refused {
			continue
		}
		without, covered, err := compiled(owner, c, "")
		if !covered {
			continue
		}
		if err != nil {
			t.Fatalf("%s: the owner does not build without settings: %v", c.ID, err)
		}
		with, _, err := compiled(owner, c, controls.Example(c))
		if err != nil {
			t.Errorf("%s = %s: %v", c.ID, controls.Example(c), err)
			continue
		}
		if !reflect.DeepEqual(with, without) {
			t.Errorf("%s: the catalog default %s builds something other than leaving it out", c.ID, controls.Example(c))
		}
		checked++
	}
	if checked < 600 {
		t.Fatalf("only %d defaults checked", checked)
	}
	t.Logf("%d defaults checked against their compilers", checked)
}
