package edit

import (
	"bytes"
	"fmt"
	"math"
	"strconv"
	"strings"

	"m31labs.dev/cicada/notation"
)

type AddAudioTrack struct {
	Name string `json:"name"`
}

func (AddAudioTrack) Kind() string { return "addaudiotrack" }

type SetClipSettings struct {
	Entity  EntityID `json:"entity"`
	Start   int64    `json:"start"`
	End     int64    `json:"end"`
	GainDB  float64  `json:"gaindb"`
	FadeIn  int64    `json:"fadein"`
	FadeOut int64    `json:"fadeout"`
}

func (SetClipSettings) Kind() string { return "setclipsettings" }

func init() {
	Register("addaudiotrack", func() Intent { return &AddAudioTrack{} })
	Register("setclipsettings", func() Intent { return &SetClipSettings{} })
	Handle("addaudiotrack", editClip)
	Handle("setclipsettings", editClip)
}

func editClip(ctx *Context, intent Intent) error {
	var updated []byte
	var label string
	var err error
	switch in := intent.(type) {
	case *AddAudioTrack:
		updated, err = newAudioTrackSource(ctx, in.Name)
		label = "Audio track added · " + in.Name
	case *SetClipSettings:
		if _, e := ParseEntityID(string(in.Entity)); e != nil {
			return e
		}
		if in.Entity.Kind() != KindClip {
			return fmt.Errorf("clip settings need a clip entity, got %q", in.Entity)
		}
		updated, err = clipSettingsSource(ctx, in.Entity.Name(), in)
		label = "Audio region edited · " + in.Entity.Name()
	}
	if err != nil {
		return err
	}
	ctx.Source, ctx.plan = updated, nil
	ctx.SetLabel(label)
	return nil
}
func newAudioTrackSource(ctx *Context, name string) ([]byte, error) {
	source := ctx.Source
	score, ds := ctx.Parse()
	edition := ctx.Options.Edition
	if edition == 0 && score != nil {
		edition = score.Version
	}
	if edition != 2 || !patternName.MatchString(name) {
		return nil, fmt.Errorf("choose a lowercase audio track name in an edition-2 score")
	}
	if score == nil || hasErrors(ds) {
		return nil, fmt.Errorf("score must validate before adding a track")
	}
	for _, track := range score.Tracks {
		if track.Name == name {
			return nil, fmt.Errorf("track %s already exists", name)
		}
	}
	if len(score.Tracks) >= 16 {
		return nil, fmt.Errorf("a project supports at most 16 tracks")
	}
	root, walker, err := notation.ParseTree(source)
	if err != nil {
		return nil, err
	}
	at := len(source)
	for i := 0; i < root.NamedChildCount(); i++ {
		node := root.NamedChild(i)
		if walker.Type(node) == "scene_decl" {
			at = int(node.StartByte())
			break
		}
	}
	newline := "\n"
	if bytes.Contains(source, []byte("\r\n")) {
		newline = "\r\n"
	}
	text := "track " + name + " audio {}" + newline + newline
	if at > 0 && source[at-1] != '\n' {
		text = newline + text
	}
	return ReplaceSpan(source, at, at, []byte(text))
}

func clipSettingsSource(ctx *Context, id string, settings *SetClipSettings) ([]byte, error) {
	source := ctx.Source
	if settings == nil || settings.Start < 0 || settings.End <= settings.Start || settings.FadeIn < 0 || settings.FadeOut < 0 || settings.FadeIn > settings.End-settings.Start || settings.FadeOut > settings.End-settings.Start || math.IsNaN(settings.GainDB) || math.IsInf(settings.GainDB, 0) || settings.GainDB < -60 || settings.GainDB > 24 {
		return nil, fmt.Errorf("choose a valid source region, fades no longer than the region, and gain −60 to +24 dB")
	}
	score, ds := ctx.Parse()
	if score == nil || hasErrors(ds) {
		return nil, fmt.Errorf("score must validate before editing audio")
	}
	var original *notation.Clip
	for i := range score.Clips {
		if score.Clips[i].Name == id {
			original = &score.Clips[i]
			break
		}
	}
	if original == nil {
		return nil, fmt.Errorf("unknown clip %s", id)
	}
	for _, asset := range score.Assets {
		if asset.Name == original.Asset && settings.End > asset.Frames {
			return nil, fmt.Errorf("clip end exceeds the source asset")
		}
	}
	node, walker, err := declaration(source, []string{"clip_decl"}, id)
	if err != nil {
		return nil, err
	}
	values := map[string]string{}
	for _, value := range []struct {
		name      string
		old, next int64
	}{{"start", original.StartFrame, settings.Start}, {"end", original.EndFrame, settings.End}, {"fade_in", original.FadeInFrames, settings.FadeIn}, {"fade_out", original.FadeOutFrames, settings.FadeOut}} {
		if value.old != value.next {
			values[value.name] = strconv.FormatInt(value.next, 10) + "frames"
		}
	}
	if original.GainDB != settings.GainDB {
		values["gain"] = strconv.FormatFloat(settings.GainDB, 'f', -1, 64) + "dB"
	}
	var edits []Span
	for i := 0; i < node.NamedChildCount(); i++ {
		param := node.NamedChild(i)
		if walker.Type(param) != "param_decl" {
			continue
		}
		name := walker.Text(walker.Field(param, "name"))
		if text, ok := values[name]; ok {
			value := walker.Field(param, "value")
			edits = append(edits, Span{int(value.StartByte()), int(value.EndByte()), text})
			delete(values, name)
		}
	}
	var fields []string
	for _, name := range []string{"start", "end", "gain", "fade_in", "fade_out"} {
		if text, ok := values[name]; ok {
			fields = append(fields, name+" = "+text)
		}
	}
	if len(fields) > 0 {
		at := int(node.StartByte()) + bytes.IndexByte(source[node.StartByte():node.EndByte()], '{') + 1
		edits = append(edits, Span{at, at, " " + strings.Join(fields, " ") + " "})
	}
	return PatchSpans(source, edits)
}
