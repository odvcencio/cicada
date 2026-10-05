package main

import (
	"fmt"
	"net/url"
	"strconv"

	"m31labs.dev/cicada/workstation/ui"
	"m31labs.dev/gosx"
	"m31labs.dev/gosx/server"
)

func (s *studioApp) audioClips(ctx *server.Context, v workspace, csrf string) gosx.Node {
	if v.Project == nil {
		return gosx.Fragment()
	}
	p := v.Project
	var tracks, scenes []string
	for _, track := range p.Tracks {
		if track.Kind == "audio" && p.Edition == 2 {
			tracks = append(tracks, track.ID)
		}
	}
	for _, scene := range p.Scenes {
		scenes = append(scenes, scene.ID)
	}
	var rows []gosx.Node
	if p.Edition == 2 {
		name:="audio"
		for n:=2;;n++ { used:=false;for _,track:=range p.Tracks { used=used||track.ID==name };if !used {break};name="audio-"+strconv.Itoa(n) }
		rows = append(rows, s.form(v, csrf, "takes", "clip", hidden("action", "audio-track"), field("New audio track", textInput("newName",name)), submit("", "", "Add audio track")))
	} else {
		rows = append(rows, gosx.El("p", gosx.Text("Audio tracks use score edition 2. "), gosx.El("a", gosx.Attrs(gosx.Attr("href", "/?panel=mixer")), gosx.Text("Upgrade through Mixer")), gosx.Text(" before adding an audio track.")))
	}
	for _, clip := range p.Clips {
		rate, frames := 0, int64(0)
		for _, asset := range p.Assets {
			if asset.Name == clip.Asset {
				rate, frames = asset.RateHz, asset.Frames
			}
		}
		length := fmt.Sprintf("%d source frames", clip.EndFrame-clip.StartFrame)
		if rate > 0 {
			length = fmt.Sprintf("%.3f seconds · %d Hz", float64(clip.EndFrame-clip.StartFrame)/float64(rate), rate)
		}
		form := func(children ...gosx.Node) gosx.Node {
			return ui.Form(ui.FormProps{Action: "/__actions/clip", CSRF: csrf, Revision: v.Revision, ReturnTo: "/?panel=takes&clip=" + url.QueryEscape(clip.Name), Class: "action-form"}, append([]gosx.Node{hidden("pattern", clip.Name)}, children...)...)
		}
		frameInput := func(name string, value, max int64) gosx.Node {
			return wholeInput(name,int(value),0,int(max))
		}
		settings := form(hidden("action", "clip-settings"), field("Start (source frame)", frameInput("start", clip.StartFrame, frames-1)), field("End (exclusive source frame)", frameInput("end", clip.EndFrame, frames)), field("Clip gain (dB)", numberInput("gainDB", strconv.FormatFloat(clip.GainDB, 'f', -1, 64), "-60", "24")), field("Fade in (source frames)", frameInput("fadeIn", clip.FadeInFrames, frames)), field("Fade out (source frames)", frameInput("fadeOut", clip.FadeOutFrames, frames)), submit("", "", "Save audio region"))
		var assign gosx.Node = gosx.Fragment()
		if len(tracks) > 0 && len(scenes) > 0 {
			assign = form(hidden("action", "clip-bind"), field("Scene", selectInput("scene", "", scenes)), field("Audio track", selectInput("track", "", tracks)), submit("", "", "Place clip in scene"))
		}
		rows = append(rows, gosx.El("details", gosx.Attrs(gosx.Attr("class", "arrangement-block"), gosx.Attr("open", ctx.Request.URL.Query().Get("clip") == clip.Name)), gosx.El("summary", gosx.Text(clip.Name+" · "+length)), gosx.El("p", gosx.Text("Asset: "+clip.Asset+". Region edits preserve the recorded file. Undo restores the previous region.")), gosx.El("div", gosx.Attrs(gosx.Attr("class", "editor-fields")), settings, assign)))
	}
	return ui.Panel(ui.PanelProps{ID: "audio-clips", Title: "Audio tracks and clips", Description: "Recorded takes play through the native mixer and Tymbal. Clips play once from their source region; keep continues playback and off releases it."}, gosx.Fragment(rows...))
}
