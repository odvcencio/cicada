package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"m31labs.dev/cicada/host/takejournal"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/workstation/control"
	"m31labs.dev/cicada/workstation/ui"
	"m31labs.dev/gosx"
	"m31labs.dev/gosx/engine"
	"m31labs.dev/gosx/server"
	"m31labs.dev/gosx/session"
)

type vocalCompProjection struct {
	Track    string                `json:"track"`
	Segments []control.VocalPhrase `json:"segments"`
}

type vocalLane struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	Start      int64  `json:"start"`
	End        int64  `json:"end"`
	Incomplete bool   `json:"incomplete"`
	Stamp      string `json:"stamp"`
	Tempo      int64  `json:"tempo"`
}

type vocalLanesProps struct {
	Revision     string                `json:"revision"`
	End          int64                 `json:"end"`
	Selected     string                `json:"selected"`
	Start        int64                 `json:"start"`
	Stop         int64                 `json:"stop"`
	Lanes        []vocalLane           `json:"lanes"`
	Track        string                `json:"track"`
	Phrases      []control.VocalPhrase `json:"phrases"`
	GainDB       float64               `json:"gainDB"`
	AuditionMode string                `json:"auditionMode"`
	Edits        control.VocalEdits    `json:"edits"`
}

func vocalPosition(tick int64) string {
	if tick%seq.TicksPerStep != 0 {
		return fmt.Sprintf("%dticks", tick)
	}
	return fmt.Sprintf("@%d.%d.%d", tick/seq.TicksPerBar+1, tick%seq.TicksPerBar/seq.PPQ+1, tick%seq.PPQ/seq.TicksPerStep+1)
}

func (s *studioApp) vocals(ctx *server.Context, v workspace, csrf string) gosx.Node {
	if v.Project == nil || v.Project.Edition != 2 {
		return gosx.Fragment()
	}
	var state struct {
		control.CaptureState
		Takes []takejournal.Take    `json:"takes"`
		Comps []vocalCompProjection `json:"comps"`
	}
	if err := s.backend.call(ctx.Request.Context(), http.MethodGet, "/api/takes", nil, &state); err != nil {
		return failure(err)
	}
	p := v.Project
	var selectedRange struct {
		TakeID string `json:"takeId"`
		Start  string `json:"start"`
		End    string `json:"end"`
	}
	session.Current(ctx.Request).Decode("vocal-range", &selectedRange)
	var tracks []string
	for _, track := range p.Tracks {
		if track.Kind == "audio" {
			tracks = append(tracks, track.ID)
		}
	}
	end := int64(0)
	if p.Arrange != nil {
		for _, place := range p.Arrange.Placements {
			end = max(end, place.AtTick+place.LengthTicks)
		}
	} else {
		for _, entry := range p.Song {
			end += int64(entry.Bars) * seq.TicksPerBar
		}
	}
	chosen := ctx.Request.URL.Query().Get("vocalTrack")
	if chosen == "" && len(tracks) > 0 {
		chosen = tracks[0]
		for _, track := range tracks {
			if track == "danilo" {
				chosen = track
			}
		}
	}
	button := func(action, label string, disabled bool) gosx.Node {
		return gosx.El("button", gosx.Attrs(gosx.Attr("type", "submit"), gosx.Attr("name", "action"), gosx.Attr("value", action), gosx.Attr("data-capture-control", action), gosx.Attr("disabled", disabled), gosx.Attr("class", map[bool]string{true: "primary", false: ""}[action == "record"])), gosx.Text(label))
	}
	active := state.Active != ""
	fullSong := gosx.El("button", gosx.Attrs(gosx.Attr("type", "button"), gosx.Attr("data-capture-full-song", vocalPosition(end)), gosx.Attr("disabled", active)), gosx.Text("Full song"))
	controls := s.form(v, csrf, "session", "take", hidden("timeline", "on"), field("Vocal track", selectInput("track", chosen, tracks, "Vocal track")), field("Punch in", textInput("recordStart", "@1.1.1")), field("Punch out", textInput("recordEnd", vocalPosition(end))), field("Passes", selectInput("passes", "1", []string{"1", "2", "3", "4", "5", "6", "7", "8"}, "Passes")), button("record", "Record take", active || len(tracks) == 0), button("stop", "Finish take", !active), fullSong)
	add := s.form(v, csrf, "session", "clip", hidden("action", "audio-track"), field("New vocal track", textInput("newName", "danilo")), submit("", "", "Add vocal track"))
	var audio audioState
	audioError := s.backend.call(ctx.Request.Context(), http.MethodGet, "/api/audio/config", nil, &audio)
	readiness := recordingReadiness(audio)
	idle := "Microphone and playback settings could not be checked. Open Recording setup before recording."
	var summary gosx.Node = gosx.Fragment()
	if audioError == nil {
		idle = readiness.Title + ". " + readiness.Detail
		summary = audioSummary(audio)
	}
	if len(tracks) == 0 {
		idle = "Add a vocal track in Recording setup, then choose your microphone."
	} else if readiness.Ready {
		idle = "Microphone configured. Record the full song, or set a punch range for a phrase."
	}
	setupURL := "/?panel=audio&returnTo=session&vocalTrack=" + url.QueryEscape(chosen)
	setup := gosx.El("details", gosx.Attrs(gosx.Attr("class", "vocal-setup"), gosx.Attr("open", len(tracks) == 0 || !readiness.Ready)), gosx.El("summary", gosx.Text("Recording setup")), summary, gosx.El("a", gosx.Attrs(gosx.Attr("href", setupURL), gosx.Attr("class", "button-link")), gosx.Text("Choose microphone and output")), add)

	var trackLinks []gosx.Node
	for _, track := range tracks {
		trackLinks = append(trackLinks, gosx.El("a", gosx.Attrs(gosx.Attr("href", "/?panel=session&vocalTrack="+url.QueryEscape(track)), gosx.Attr("aria-current", map[bool]string{true: "page", false: "false"}[track == chosen])), gosx.Text(track)))
	}
	var compRows []gosx.Node
	takeNames := map[string]string{}
	n := 0
	for _, take := range state.Takes {
		if take.Timeline != nil && take.Track == chosen {
			n++
			takeNames[take.ID] = fmt.Sprintf("Take %d", n)
		}
	}
	for _, comp := range state.Comps {
		if comp.Track != chosen {
			continue
		}
		for _, seg := range comp.Segments {
			compRows = append(compRows, gosx.El("li", gosx.Attrs(gosx.Attr("data-comp-take", seg.Take)), gosx.El("strong", gosx.Text(takeNames[seg.Take])), gosx.Text(" · "+vocalPosition(seg.Start)+" → "+vocalPosition(seg.End)+" · "+control.VocalGainText(seg.GainDB))))
		}
	}
	var lanes []vocalLane
	for _, take := range state.Takes {
		if take.Timeline == nil || take.Track != chosen || take.Frames == 0 || take.Asset.Path == "" {
			continue
		}
		clock := seq.Clock{SampleRate: int64(take.Rate), BPMMilli: take.Timeline.TempoMilli}
		origin := int64(0)
		if take.FirstBlock != nil {
			origin = int64(take.FirstBlock.RawFrame) - take.FirstBlock.Placement.EngineFrame
		}
		coveredEnd := min(take.Timeline.EndTick, clock.TickAtSample(clock.SampleAtTick(take.Timeline.PlaybackStartTick)+int64(take.Frames)-origin))
		if coveredEnd <= take.Timeline.StartTick {
			continue
		}
		if take.Timeline.StartTick < end {
			lanes = append(lanes, vocalLane{ID: take.ID, Label: takeNames[take.ID], Start: take.Timeline.StartTick, End: coveredEnd, Incomplete: take.Incomplete, Stamp: take.Created.Format("Jan 2 15:04 UTC"), Tempo: take.Timeline.TempoMilli})
		}
	}
	var current gosx.Node = gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Your chosen vocal phrases will appear here. Every pass stays available below."))
	if len(compRows) > 0 {
		current = gosx.El("ol", gosx.Attrs(gosx.Attr("class", "vocal-comp"), gosx.Attr("aria-label", "Chosen vocal regions")), gosx.Fragment(compRows...))
	}
	status := idle
	if active {
		status = "Take armed or recording · " + state.Active
	}
	// Managed navigation retains an engine whose props are unchanged. Include
	// the saved score, take projection and selection so its server-owned forms
	// and waveform lanes refresh when another vocal track or take is added.
	projection, _ := json.Marshal(state)
	props, _ := json.Marshal(map[string]any{"revision": v.Revision, "projection": fmt.Sprintf("%x", sha256.Sum256(projection)), "track": chosen, "selection": selectedRange, "audition": ctx.Request.URL.RawQuery, "hasAudioTracks": len(tracks) > 0, "active": state.Active, "timeline": true})
	var laneEditor gosx.Node = gosx.Fragment()
	if len(lanes) > 0 {
		selected := lanes[0]
		want := ctx.Request.URL.Query().Get("vocalTake")
		if want == "" {
			want = selectedRange.TakeID
		}
		for _, lane := range lanes {
			if lane.ID == want {
				selected = lane
			}
		}
		lo, hi := selected.Start, min(selected.End, end)
		if selected.ID == selectedRange.TakeID {
			a, e1 := notation.MusicalTicks(selectedRange.Start, true)
			b, e2 := notation.MusicalTicks(selectedRange.End, true)
			if e1 == nil && e2 == nil && a >= lo && b <= hi && a < b {
				lo, hi = a, b
			}
		}
		var phrases []control.VocalPhrase
		for _, comp := range state.Comps {
			if comp.Track == chosen {
				phrases = comp.Segments
			}
		}
		laneEditor = s.vocalLanes(ctx, v, csrf, chosen, lanes, state.Comps, vocalLanesProps{Revision: v.Revision, End: end, Selected: selected.ID, Start: lo, Stop: hi, Lanes: lanes, Track: chosen, Phrases: phrases}, active, button)
	}
	content := gosx.Fragment(gosx.El("div", gosx.Attrs(gosx.Attr("class", "vocal-record"), gosx.Attr("data-capture-idle", idle)), controls), inputMeter(), gosx.El("p", gosx.Attrs(gosx.Attr("data-capture-status", ""), gosx.Attr("role", "status")), gosx.Text(status)), gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("One-bar count-in, then backing pre-roll before the punch. Recording stops at Punch out. A clean pass updates that range in your vocal; Undo restores the previous choices.")), setup, gosx.El("div", gosx.Attrs(gosx.Attr("class", "vocal-choices")), gosx.El("h3", gosx.Text("Choose the best phrases")), gosx.El("nav", gosx.Attrs(gosx.Attr("class", "actions"), gosx.Attr("aria-label", "Vocal tracks")), gosx.Fragment(trackLinks...)), current, laneEditor))
	surface := meterEngine(ctx, engine.Config{Name: "CicadaTakes", Kind: engine.KindSurface, MountID: "cicada-vocals", Runtime: engine.RuntimeGoWASM, Props: props, Capabilities: []engine.Capability{engine.CapFetch, engine.CapAudio}, RequiredCapabilities: []engine.Capability{engine.CapWASM, engine.CapFetch}}, content)
	return ui.Panel(ui.PanelProps{ID: "vocals", Title: "Record vocals", Description: "Full-song takes, punch-ins and your chosen vocal phrases."}, surface)
}

func (s *studioApp) vocalLanes(ctx *server.Context, v workspace, csrf, track string, lanes []vocalLane, comps []vocalCompProjection, props vocalLanesProps, active bool, button func(string, string, bool) gosx.Node) gosx.Node {
	percent := func(tick int64) string {
		return fmt.Sprintf("%.6f%%", 100*float64(tick)/float64(max(int64(1), props.End)))
	}
	var regions, rows []gosx.Node
	names := map[string]string{}
	for _, lane := range lanes {
		names[lane.ID] = lane.Label
	}
	for _, comp := range comps {
		if comp.Track != track {
			continue
		}
		for _, seg := range comp.Segments {
			label := names[seg.Take] + " · " + vocalPosition(seg.Start) + " → " + vocalPosition(seg.End) + " · " + control.VocalGainText(seg.GainDB)
			regions = append(regions, gosx.El("button", gosx.Attrs(gosx.Attr("type", "button"), gosx.Attr("class", "vocal-comp-region"), gosx.Attr("data-vocal-pick", seg.Take), gosx.Attr("data-vocal-start", seg.Start), gosx.Attr("data-vocal-end", seg.End), gosx.Attr("data-vocal-gain-db", seg.GainDB), gosx.Attr("style", "left:"+percent(seg.Start)+";width:"+percent(seg.End-seg.Start)), gosx.Attr("title", label), gosx.Attr("aria-label", "Choose "+label)), gosx.Text(names[seg.Take]+" · "+control.VocalGainText(seg.GainDB))))
		}
	}
	rows = append(rows, gosx.El("div", gosx.Attrs(gosx.Attr("class", "vocal-lane-row vocal-ruler-row")), gosx.El("div", gosx.Attrs(gosx.Attr("class", "vocal-lane-label")), gosx.Text("Song bars")), gosx.El("div", gosx.Attrs(gosx.Attr("class", "vocal-ruler"), gosx.Attr("data-vocal-ruler", "")))), gosx.El("div", gosx.Attrs(gosx.Attr("class", "vocal-lane-row vocal-comp-row")), gosx.El("div", gosx.Attrs(gosx.Attr("class", "vocal-lane-label")), gosx.Text("Chosen vocal")), gosx.El("div", gosx.Attrs(gosx.Attr("class", "vocal-lane-track vocal-comp-track")), gosx.Fragment(regions...))))
	for _, lane := range lanes {
		var chosen []gosx.Node
		for _, comp := range comps {
			if comp.Track == track {
				for _, seg := range comp.Segments {
					if seg.Take == lane.ID {
						chosen = append(chosen, gosx.El("span", gosx.Attrs(gosx.Attr("class", "vocal-chosen-region"), gosx.Attr("style", "left:"+percent(seg.Start)+";width:"+percent(seg.End-seg.Start)), gosx.Attr("aria-hidden", "true"))))
					}
				}
			}
		}
		selection := gosx.El("div", gosx.Attrs(gosx.Attr("class", "vocal-selection"), gosx.Attr("data-vocal-selection", ""), gosx.Attr("hidden", lane.ID != props.Selected)), gosx.El("button", gosx.Attrs(gosx.Attr("type", "button"), gosx.Attr("class", "vocal-selection-handle start"), gosx.Attr("data-vocal-edge", "start"), gosx.Attr("role", "slider"), gosx.Attr("aria-label", "Selection start")), gosx.Text("❘")), gosx.El("button", gosx.Attrs(gosx.Attr("type", "button"), gosx.Attr("class", "vocal-selection-move"), gosx.Attr("data-vocal-move", ""), gosx.Attr("aria-label", "Move selected phrase"), gosx.Attr("title", "Drag to move the selection")), gosx.Text("↔")), gosx.El("button", gosx.Attrs(gosx.Attr("type", "button"), gosx.Attr("class", "vocal-selection-handle end"), gosx.Attr("data-vocal-edge", "end"), gosx.Attr("role", "slider"), gosx.Attr("aria-label", "Selection end")), gosx.Text("❘")))
		label := lane.Label
		if lane.Incomplete {
			label += " · gaps"
		}
		pickURL := "/?panel=session&vocalTrack=" + url.QueryEscape(track) + "&vocalTake=" + url.QueryEscape(lane.ID)
		wave := gosx.El("svg", gosx.Attrs(gosx.Attr("class", "vocal-wave"), gosx.Attr("data-vocal-wave-svg", lane.ID), gosx.Attr("data-gosx-key", "vocal-wave-"+lane.ID), gosx.Attr("viewBox", "0 0 8192 96"), gosx.Attr("preserveAspectRatio", "none"), gosx.Attr("aria-hidden", "true"), gosx.Attr("style", "left:"+percent(lane.Start)+";width:"+percent(lane.End-lane.Start))), gosx.El("path", gosx.Attrs(gosx.Attr("data-vocal-wave-path", ""))))
		rows = append(rows, gosx.El("div", gosx.Attrs(gosx.Attr("class", "vocal-lane-row"), gosx.Attr("data-vocal-take", lane.ID), gosx.Attr("data-vocal-selected", lane.ID == props.Selected)), gosx.El("div", gosx.Attrs(gosx.Attr("class", "vocal-lane-label")), gosx.El("a", gosx.Attrs(gosx.Attr("href", pickURL), gosx.Attr("data-gosx-link", "false"), gosx.Attr("data-vocal-pick", lane.ID), gosx.Attr("title", "Recorded "+lane.Stamp)), gosx.Text(label)), gosx.El("small", gosx.Text(vocalPosition(lane.Start)+" → "+vocalPosition(lane.End)))), gosx.El("div", gosx.Attrs(gosx.Attr("class", "vocal-lane-track"), gosx.Attr("data-vocal-wave", lane.ID), gosx.Attr("tabindex", "0"), gosx.Attr("role", "group"), gosx.Attr("aria-label", "Select a phrase from "+lane.Label+" waveform")), gosx.Fragment(chosen...), wave, gosx.El("span", gosx.Attrs(gosx.Attr("class", "vocal-wave-status"), gosx.Attr("data-vocal-wave-status", "")), gosx.Text("Loading waveform…")), selection)))
	}
	form := func(children ...gosx.Node) gosx.Node {
		return ui.Form(ui.FormProps{Action: "/__actions/take", CSRF: csrf, Revision: v.Revision, ReturnTo: "/?panel=session&vocalTrack=" + url.QueryEscape(track), Class: "action-form"}, children...)
	}
	props.GainDB = control.VocalPhraseGain(props.Phrases, props.Selected, control.VocalRange{Start: props.Start, End: props.Stop})
	props.Edits = control.VocalPhraseEdits(props.Phrases, props.Selected, control.VocalRange{Start: props.Start, End: props.Stop})
	props.AuditionMode = "context"
	var draft struct{ Revision, TakeID, Start, End, GainDB, AuditionMode, OffsetMS, FadeMS, CrossfadeMS string }
	session.Current(ctx.Request).Decode("vocal-audition", &draft)
	if draft.Revision == v.Revision && draft.TakeID == props.Selected && draft.Start == vocalPosition(props.Start) && draft.End == vocalPosition(props.Stop) {
		if gain, err := strconv.ParseFloat(draft.GainDB, 64); err == nil && control.ValidVocalGain(gain) {
			props.GainDB = gain
		}
		if draft.AuditionMode == "solo" {
			props.AuditionMode = "solo"
		}
		for name, target := range map[string]*float64{"offsetMS": &props.Edits.OffsetMS, "fadeMS": &props.Edits.FadeMS, "crossfadeMS": &props.Edits.CrossfadeMS} {
			text := map[string]string{"offsetMS": draft.OffsetMS, "fadeMS": draft.FadeMS, "crossfadeMS": draft.CrossfadeMS}[name]
			if n, err := strconv.ParseFloat(text, 64); err == nil {
				lo, hi := 0., 100.
				if name == "offsetMS" {
					lo, hi = -500, 500
				}
				if n >= lo && n <= hi {
					*target = n
				}
			}
		}
	}
	gainValue := strconv.FormatFloat(props.GainDB, 'f', 1, 64)
	level := gosx.El("div", gosx.Attrs(gosx.Attr("class", "vocal-phrase-controls")),
		field("Phrase gain (dB)", gosx.El("input", gosx.Attrs(gosx.Attr("type", "number"), gosx.Attr("name", "gainDB"), gosx.Attr("aria-label", "Phrase gain (dB)"), gosx.Attr("min", -24), gosx.Attr("max", 18), gosx.Attr("step", "0.1"), gosx.Attr("value", gainValue), gosx.Attr("data-vocal-level-control", ""), gosx.Attr("disabled", active)))),
		gosx.El("input", gosx.Attrs(gosx.Attr("type", "range"), gosx.Attr("aria-label", "Adjust phrase gain"), gosx.Attr("min", -24), gosx.Attr("max", 18), gosx.Attr("step", "0.1"), gosx.Attr("value", gainValue), gosx.Attr("data-vocal-gain-slider", ""), gosx.Attr("data-vocal-level-control", ""), gosx.Attr("disabled", active))),
		gosx.El("button", gosx.Attrs(gosx.Attr("type", "button"), gosx.Attr("data-vocal-gain-reset", ""), gosx.Attr("data-vocal-level-control", ""), gosx.Attr("disabled", active)), gosx.Text("Reset gain")),
		field("Hear with", gosx.El("select", gosx.Attrs(gosx.Attr("name", "auditionMode"), gosx.Attr("aria-label", "Hear with"), gosx.Attr("data-vocal-level-control", ""), gosx.Attr("disabled", active)),
			gosx.El("option", gosx.Attrs(gosx.Attr("value", "context"), gosx.Attr("selected", props.AuditionMode == "context")), gosx.Text("Song")),
			gosx.El("option", gosx.Attrs(gosx.Attr("value", "solo"), gosx.Attr("selected", props.AuditionMode == "solo")), gosx.Text("Vocal only")))),
		gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Hear selection previews this level. Use selection saves it to this phrase; original takes stay unchanged.")))
	selection := form(hidden("takeId", props.Selected), hidden("timeline", "on"), gosx.El("p", gosx.Attrs(gosx.Attr("class", "vocal-selection-readout"), gosx.Attr("data-vocal-readout", ""), gosx.Attr("role", "status")), gosx.Text(names[props.Selected]+" · "+vocalPosition(props.Start)+" → "+vocalPosition(props.Stop))), level, phraseTiming(props.Edits, active), button("listen", "Hear selection", active), button("listen-loop", "Loop selection", active), button("comp", "Use selection", active), gosx.El("button", gosx.Attrs(gosx.Attr("type", "button"), gosx.Attr("data-vocal-punch", ""), gosx.Attr("disabled", active)), gosx.Text("Set punch range")), gosx.El("details", gosx.Attrs(gosx.Attr("class", "vocal-exact")), gosx.El("summary", gosx.Text("Exact positions")), field("Range start", textInput("recordStart", vocalPosition(props.Start))), field("Range end", textInput("recordEnd", vocalPosition(props.Stop)))))
	whole := form(hidden("takeId", props.Selected), button("select", "Use whole take", active))
	controls := gosx.El("div", gosx.Attrs(gosx.Attr("class", "vocal-lane-tools")), field("Zoom", selectInput("vocalZoom", "song", []string{"song", "16 bars", "8 bars", "4 bars", "1 bar"}, "Zoom")), field("Snap", selectInput("vocalSnap", "1/16", []string{"1/16", "beat", "bar", "free"}, "Snap")), gosx.El("span", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Drag a phrase. Trim its edges, or drag ↔ to move it.")), field("Follow playback", gosx.El("input", gosx.Attrs(gosx.Attr("type", "checkbox"), gosx.Attr("name", "vocalFollow"), gosx.Attr("aria-label", "Follow playback")))))
	content := gosx.Fragment(controls, gosx.El("p", gosx.Attrs(gosx.Attr("class", "vocal-playback-status muted"), gosx.Attr("data-vocal-playback-status", ""), gosx.Attr("aria-live", "off")), gosx.Text("Hear a phrase once, or loop it until you stop. Space on a waveform hears or stops the selection.")), gosx.El("div", gosx.Attrs(gosx.Attr("class", "vocal-lane-scroll"), gosx.Attr("data-vocal-scroll", "")), gosx.El("div", gosx.Attrs(gosx.Attr("class", "vocal-lane-world"), gosx.Attr("data-vocal-world", "")), gosx.Fragment(rows...), gosx.El("div", gosx.Attrs(gosx.Attr("class", "vocal-playhead"), gosx.Attr("data-vocal-playhead", ""), gosx.Attr("hidden", true), gosx.Attr("aria-hidden", "true"))))), gosx.El("div", gosx.Attrs(gosx.Attr("class", "vocal-selection-actions"), gosx.Attr("data-vocal-selection-actions", "")), selection, whole, ui.Form(ui.FormProps{Action: "/__actions/transport", CSRF: csrf, Revision: v.Revision, ReturnTo: "/?panel=session&vocalTrack=" + url.QueryEscape(track), Class: "action-form"}, gosx.El("button", gosx.Attrs(gosx.Attr("type", "submit"), gosx.Attr("name", "action"), gosx.Attr("value", "stop"), gosx.Attr("data-vocal-stop", ""), gosx.Attr("data-capture-control", "audition-stop"), gosx.Attr("disabled", active)), gosx.Text("Stop audition")))), gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Amber marks chosen phrases. Arrow keys on a selection edge trim it; Shift moves four steps. Escape cancels a drag. Waveforms are scaled for visibility.")))
	encoded, _ := json.Marshal(props)
	return meterEngine(ctx, engine.Config{Name: "CicadaVocalLanes", Kind: engine.KindSurface, MountID: "cicada-vocal-lanes", Runtime: engine.RuntimeGoWASM, Props: encoded, Capabilities: []engine.Capability{engine.CapFetch, engine.CapPointer, engine.CapKeyboard}, RequiredCapabilities: []engine.Capability{engine.CapWASM, engine.CapFetch}}, gosx.El("div", gosx.Attrs(gosx.Attr("data-vocal-lane-props", string(encoded))), content))
}
