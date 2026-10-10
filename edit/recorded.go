package edit

import (
	"bytes"
	"fmt"
	"strings"
)

// PublishRecorded adds the sampler, track, pattern and scene binding for a
// recording pack. The host publishes the immutable pack before applying it.
type PublishRecorded struct {
	Name        string `json:"name"`
	Declaration string `json:"declaration"`
	Track       string `json:"track,omitempty"`
	Pattern     string `json:"pattern,omitempty"`
	Scene       string `json:"scene"`
	ScenePath   string `json:"scenepath,omitempty"`
	Level       string `json:"level"`
	Note        string `json:"note"`
}

func (PublishRecorded) Kind() string { return "publishrecorded" }
func init() {
	Register("publishrecorded", func() Intent { return &PublishRecorded{} })
	Handle("publishrecorded", publishRecorded)
}

func recordedName(plan *Plan, base string) string {
	for n := 1; ; n++ {
		name := base
		if n > 1 {
			name = fmt.Sprintf("%s_%d", base, n)
		}
		if !plan.Names[name] && !plan.Names[name+"_track"] && !plan.Names[name+"_taps"] {
			return name
		}
	}
}

func publishRecorded(ctx *Context, intent Intent) error {
	in := intent.(*PublishRecorded)
	plan, err := ctx.CurrentPlan()
	if err != nil {
		return err
	}
	name := recordedName(plan, in.Name)
	track, pattern := in.Track, in.Pattern
	if track == "" {
		track = name + "_track"
	}
	if pattern == "" {
		pattern = name + "_taps"
	}
	scene := in.Scene
	if scene == "" && len(plan.Scenes) > 0 {
		scene = plan.Scenes[0].ID
	}
	declaration := strings.Replace(in.Declaration, "sampler "+in.Name+" {", "sampler "+name+" {", 1)
	updated := bytes.Clone(ctx.Source)
	edition := plan.Edition
	if edition == 0 {
		score, _, err := ctx.ParseProject()
		if err != nil {
			return err
		}
		if score != nil {
			edition = score.Version
		}
	}
	if edition == 1 {
		updated = append([]byte("cicada 2\n"), updated...)
	}
	updated = append(updated, []byte(fmt.Sprintf("\n%s\ntrack %s %s { level = %s }\npattern %s { %s . . . %s . . . }\n", declaration, track, name, in.Level, pattern, in.Note, in.Note))...)
	if in.ScenePath == "" || in.ScenePath == ctx.Options.Path {
		updated, err = bindPatternSource(sourceContext(ctx, updated), scene, track, pattern)
	} else {
		found := false
		for _, file := range ctx.Options.Sources {
			if file.Path != in.ScenePath {
				continue
			}
			found = true
			var after []byte
			after, err = bindRecordedScene(file.Source, scene, track, pattern)
			if err == nil {
				ctx.AddFile(File{Path: file.Path, Before: bytes.Clone(file.Source), After: after})
			}
			break
		}
		if !found {
			return fmt.Errorf("recorded scene source is unavailable")
		}
	}
	if err != nil {
		return err
	}
	ctx.Source, ctx.plan = updated, nil
	ctx.SetLabel("Recorded instrument added · " + name)
	ctx.Respond("instrument", name)
	ctx.Respond("track", track)
	ctx.Respond("declaration", declaration)
	return nil
}

func bindRecordedScene(source []byte, scene, track, pattern string) ([]byte, error) {
	node, _, err := declaration(source, []string{"scene_decl"}, scene)
	if err != nil {
		return nil, err
	}
	newline := "\n"
	if bytes.Contains(source, []byte("\r\n")) {
		newline = "\r\n"
	}
	at := int(node.EndByte()) - 1
	return ReplaceSpan(source, at, at, []byte(newline+"  "+track+" = "+pattern+newline))
}
