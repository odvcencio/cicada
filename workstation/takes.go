package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"m31labs.dev/cicada/workstation/control"
	"m31labs.dev/cicada/workstation/ui"
	"m31labs.dev/gosx"
	"m31labs.dev/gosx/engine"
	"m31labs.dev/gosx/server"
)

func (s *studioApp) takes(ctx *server.Context, v workspace, csrf string) gosx.Node {
	var state struct {
		control.CaptureState
		Takes []struct {
			ID         string `json:"id"`
			Track      string `json:"track"`
			Stage      string `json:"stage"`
			Frames     uint64 `json:"frames"`
			Rate       int    `json:"rate"`
			Incomplete bool   `json:"incomplete"`
			Asset      struct {
				Path string `json:"path"`
			} `json:"asset"`
		} `json:"takes"`
	}
	if err := s.backend.call(ctx.Request.Context(), http.MethodGet, "/api/takes", nil, &state); err != nil {
		return failure(err)
	}
	var tracks, scenes []string
	if v.Project != nil {
		for _, t := range v.Project.Tracks {
			if t.Kind == "audio" && v.Project.Edition == 2 {
				tracks = append(tracks, t.ID)
			}
		}
		for _, scene := range v.Project.Scenes {
			scenes = append(scenes, scene.ID)
		}
	}
	button := func(name, label string, disabled bool) gosx.Node {
		return gosx.El("button", gosx.Attrs(gosx.Attr("type", "submit"), gosx.Attr("name", "action"), gosx.Attr("value", name), gosx.Attr("data-capture-control", name), gosx.Attr("disabled", disabled)), gosx.Text(label))
	}
	recording := state.Capture != nil && state.Capture.Recording
	controls := s.form(v, csrf, "takes", "take", field("Audio track", selectInput("track", "", tracks)), field("Scene", selectInput("scene", "", scenes)), button("arm", "Arm take", state.Active != "" || len(tracks) == 0), button("start", "Record", state.Active == "" || recording), button("stop", "Finish take", state.Active == ""))
	var rows []gosx.Node
	selected := ctx.Request.URL.Query().Get("take")
	var audition gosx.Node = gosx.Fragment()
	for _, take := range state.Takes {
		length := fmt.Sprintf("%d frames", take.Frames)
		if take.Rate > 0 {
			length = fmt.Sprintf("%.2f seconds", float64(take.Frames)/float64(take.Rate))
		}
		if take.Incomplete {
			length += " · incomplete: inspect before selecting"
		}
		row := []gosx.Node{gosx.El("p", gosx.Text(fmt.Sprintf("%s · %s · %s · %s", take.ID, take.Track, length, take.Stage)))}
		if take.Asset.Path != "" && take.Frames > 0 {
			q := ctx.Request.URL.Query()
			q.Set("panel", "takes")
			q.Set("take", take.ID)
			q.Del("note")
			q.Del("root")
			q.Del("loop")
			row = append(row, gosx.El("a", gosx.Attrs(gosx.Attr("href", "/?"+q.Encode())), gosx.Text("Audition sample")))
			if selected == take.ID {
				note, root := 60, 60
				if n, err := strconv.Atoi(ctx.Request.URL.Query().Get("note")); err == nil && n >= 0 && n <= 127 {
					note = n
				}
				if n, err := strconv.Atoi(ctx.Request.URL.Query().Get("root")); err == nil && n >= 0 && n <= 127 {
					root = n
				}
				loop := ctx.Request.URL.Query().Get("loop") == "on"
				check := gosx.El("input", gosx.Attrs(gosx.Attr("type", "checkbox"), gosx.Attr("name", "loop"), gosx.Attr("checked", loop)))
				audition = gosx.El("section", gosx.Attrs(gosx.Attr("class", "strip")), gosx.El("h3", gosx.Text("Sample audition · "+take.ID)),
					gosx.El("form", gosx.Attrs(gosx.Attr("method", "get"), gosx.Attr("action", "/"), gosx.Attr("class", "actions")), hidden("panel", "takes"), hidden("take", take.ID), field("Root MIDI note", numberInput("root", strconv.Itoa(root), "0", "127")), field("Play MIDI note", numberInput("note", strconv.Itoa(note), "0", "127")), field("Loop region", check), submit("", "", "Update audition")),
					gosx.El("audio", gosx.Attrs(gosx.BoolAttr("controls"), gosx.Attr("loop", loop), gosx.Attr("preload", "none"), gosx.Attr("src", takePreviewURL(take.ID, v.Revision, note, root, loop)), gosx.Attr("aria-label", "Sample audition"))),
					gosx.El("p", gosx.Text("Preview uses Cicada's native sample voice. Selecting a take changes the score; audition leaves it unchanged.")))
			}
		}
		row = append(row, s.form(v, csrf, "takes", "take", hidden("takeId", take.ID), button("select", "Select take", state.Active != "" || take.Frames == 0), button("recover", "Recover take", state.Active != "" || take.Frames == 0)))
		rows = append(rows, gosx.El("li", gosx.Attrs(gosx.Attr("class", "strip")), gosx.Fragment(row...)))
	}
	status := "No active capture. Enable input in Audio, then arm an audio track."
	if state.Active != "" {
		status = "Active take: " + state.Active
	}
	projection, _ := json.Marshal(state)
	props, _ := json.Marshal(map[string]any{"csrf": csrf, "revision": v.Revision, "projection": fmt.Sprintf("%x", sha256.Sum256(projection)), "take": selected, "audition": ctx.Request.URL.RawQuery, "hasAudioTracks": len(tracks) > 0})
	content := gosx.Fragment(controls, gosx.El("p", gosx.Attrs(gosx.Attr("data-capture-status", ""), gosx.Attr("role", "status")), gosx.Text(status)), audition, gosx.El("ul", gosx.Attrs(gosx.Attr("class", "take-list")), gosx.Fragment(rows...)))
	surface := ctx.Engine(engine.Config{Name: "CicadaTakes", Kind: engine.KindSurface, MountID: "cicada-takes", Runtime: engine.RuntimeGoWASM, WASMPath: ui.MeterEnginePath, Props: props, Capabilities: []engine.Capability{engine.CapFetch, engine.CapAudio}, RequiredCapabilities: []engine.Capability{engine.CapWASM, engine.CapFetch}}, content)
	return gosx.Fragment(ui.Panel(ui.PanelProps{ID: "takes", Title: "Recorded takes", Description: "Tymbal records the selected native input with a two-bar count-in. Completed takes are saved through the durable journal."}, surface), s.audioClips(ctx, v, csrf))
}
