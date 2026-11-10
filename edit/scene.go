package edit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"m31labs.dev/cicada/notation"
)

// SetSceneSetting sets the value a parameter takes when a scene starts, or
// adds that setting to the scene. The parameter must be live and automatable;
// the scene tempo is the one bare path a scene may set.
//
// Example: {"kind":"setscenesetting","entity":"setting:main/bass.cutoff","value":"1.25kHz"}
type SetSceneSetting struct {
	Entity EntityID        `json:"entity" cicada:"required"` // the setting, as setting:<scene>/<owner>.<field>
	Value  json.RawMessage `json:"value" cicada:"required"`  // a number, or a string with a unit such as "1.25kHz" or "-6dB"; cicada params lists ranges and units
}

func (SetSceneSetting) Kind() string { return "setscenesetting" }

// RemoveSceneSetting removes one parameter setting from a scene.
//
// Example: {"kind":"removescenesetting","entity":"setting:main/bass.reso"}
type RemoveSceneSetting struct {
	Entity EntityID `json:"entity" cicada:"required"` // the setting, as setting:<scene>/<owner>.<field>
}

func (RemoveSceneSetting) Kind() string { return "removescenesetting" }

func init() {
	Register("setscenesetting", func() Intent { return &SetSceneSetting{} })
	Register("removescenesetting", func() Intent { return &RemoveSceneSetting{} })
	Handle("setscenesetting", editSceneSetting)
	Handle("removescenesetting", editSceneSetting)
}

func editSceneSetting(ctx *Context, intent Intent) error {
	var entity EntityID
	var value json.RawMessage
	remove := false
	switch in := intent.(type) {
	case *SetSceneSetting:
		entity, value = in.Entity, in.Value
	case *RemoveSceneSetting:
		entity, remove = in.Entity, true
	}
	if _, err := ParseEntityID(string(entity)); err != nil {
		return entityError(err)
	}
	if entity.Kind() != KindSetting {
		return entityError(fmt.Errorf("scene setting edit needs a setting entity, got %q", entity))
	}
	scene, path, _ := strings.Cut(entity.Name(), "/")
	updated, err := sceneSettingSource(ctx, scene, path, value, remove)
	if err != nil {
		return err
	}
	ctx.Source, ctx.plan = updated, nil
	action := "set"
	if remove {
		action = "removed"
	}
	ctx.SetLabel(fmt.Sprintf("Automation · %s / %s %s", scene, path, action))
	return nil
}

// Scene points use the existing parameter catalog and playback/render glides.
// Only the assignment's value or syntax span changes; surrounding comments,
// bindings, authored whitespace, and newline convention remain intact.
func sceneSettingSource(ctx *Context, sceneID, path string, raw json.RawMessage, remove bool) ([]byte, error) {
	_, source, file, err := sceneSource(ctx, sceneID)
	if err != nil {
		return nil, err
	}
	p, err := ctx.CurrentPlan()
	if err != nil {
		return nil, fmt.Errorf("score parameters must validate before editing automation")
	}
	if p.ResolveParam == nil {
		return nil, fmt.Errorf("score parameters must validate before editing automation")
	}
	resolved, err := p.ResolveParam(path)
	if err != nil {
		return nil, err
	}
	patch := func(span Span) ([]byte, error) {
		after, err := ReplaceSpan(source, file, span)
		if err != nil {
			return nil, err
		}
		return stageProjectSource(ctx, file, source, after), nil
	}
	// The scene tempo is the one bare path a scene may set. It is not live and
	// not automatable: it steps at the scene boundary.
	tempo := path == "tempo" && resolved.Descriptor.ID == "global.tempo"
	if !tempo && (!strings.Contains(path, ".") || !resolved.Descriptor.Live || !remove && !resolved.Descriptor.Automatable) {
		return nil, fmt.Errorf("%s cannot be automated at a scene boundary", path)
	}
	node, walker, err := declaration(source, []string{"scene_decl"}, sceneID)
	if err != nil {
		return nil, err
	}
	literal := ""
	if !remove {
		descriptor := resolved.Descriptor
		// Display steps guide controls, but an authored automation value keeps
		// its full precision within the shared parameter range.
		descriptor.DisplayStep = 0
		literal, err = CanonicalMixerLiteral(descriptor, raw)
		if err != nil {
			return nil, err
		}
	}
	for i := 0; i < node.NamedChildCount(); i++ {
		assignment := node.NamedChild(i)
		if walker.Type(assignment) != "scene_assignment" || walker.Text(walker.Field(assignment, "target")) != path || tempo && !notation.SceneAssignmentIsSetting(path, walker.Text(walker.Field(assignment, "value"))) {
			continue
		}
		if remove {
			return patch(Span{int(assignment.StartByte()), int(assignment.EndByte()), "", file})
		}
		value := walker.Field(assignment, "value")
		return patch(Span{int(value.StartByte()), int(value.EndByte()), literal, file})
	}
	if remove {
		return nil, fmt.Errorf("scene %s has no point for %s", sceneID, path)
	}
	newline := "\n"
	if bytes.Contains(source, []byte("\r\n")) {
		newline = "\r\n"
	}
	at := int(node.StartByte()) + bytes.IndexByte(source[node.StartByte():node.EndByte()], '{') + 1
	return patch(Span{at, at, newline + "  " + path + " = " + literal + newline, file})
}
