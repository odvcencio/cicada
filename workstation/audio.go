package main

import (
	"net/url"
	"strconv"
	"strings"

	"m31labs.dev/cicada/workstation/control"
	"m31labs.dev/cicada/workstation/ui"
	"m31labs.dev/gosx"
	"m31labs.dev/gosx/server"
)

type audioReadiness struct {
	Title, Detail, Input, Output string
	Ready                        bool
	PlaybackReady                bool
}

func audioCaptureSupported(state audioState) bool {
	if state.Inventory.CaptureSupported != nil {
		return *state.Inventory.CaptureSupported
	}
	// Older domain services did not publish a capability flag. Their Tymbal
	// backends were WASAPI/ALSA, both of which support recording input.
	return state.Inventory.BackendName == "tymbal"
}

func audioDeviceName(state audioState, input bool) (string, bool) {
	selected := state.Options.OutputDevice
	if input {
		selected = state.Options.InputDevice
	}
	for _, d := range state.Inventory.Devices {
		channels, isDefault := d.Outputs, d.DefaultOutput
		if input {
			channels, isDefault = d.Inputs, d.DefaultInput
		}
		if channels > 0 && (selected != "" && d.ID == selected || selected == "" && isDefault) {
			return d.Name, true
		}
	}
	if selected != "" {
		return "Saved device is disconnected", false
	}
	return "No default device", false
}

func recordingReadiness(state audioState) audioReadiness {
	r := audioReadiness{}
	var input, output bool
	r.Input, input = audioDeviceName(state, true)
	r.Output, output = audioDeviceName(state, false)
	backend := strings.ToLower(state.Inventory.BackendName)
	if backend == "" {
		backend = strings.ToLower(state.Inventory.Backend)
	}
	if backend == "null" {
		r.Title, r.Detail, r.Output, r.Input = "Silent playback", "This song was opened with audio disabled. Reopen it in Cicada Studio with native audio to use your speakers and microphone.", "Silent output", "Microphone unavailable"
		return r
	}
	if backend != "tymbal" {
		r.Title, r.Detail, r.Input, r.Output = "Microphone unavailable", "This audio backend supports playback only. Recording input is unavailable.", "Recording unavailable", "System default"
		r.PlaybackReady = backend == "oto" && state.Runtime.Error == ""
		return r
	}
	if !state.Inventory.Supported || state.Inventory.Message != "" {
		r.Title, r.Detail = "Audio devices need attention", "Connect your audio devices and refresh the list. Check Audio details if they still do not appear."
		return r
	}
	if !output {
		r.Title, r.Detail = "Choose a playback output", "Connect headphones or speakers, then refresh devices and choose an output."
		for _, d := range state.Inventory.Devices {
			if d.Outputs > 0 {
				r.Detail = "Choose an available output below and apply the settings. Your system default or saved output is unavailable."
				break
			}
		}
		return r
	}
	if state.Runtime.Error != "" {
		r.Title, r.Detail = "Audio needs attention", "Stop playback, check your input and output, then apply the settings to try again."
		return r
	}
	r.PlaybackReady = true
	if !audioCaptureSupported(state) {
		r.Title, r.Detail, r.Input = "Playback configured", "You can choose a native playback device. This audio driver does not support recording input yet.", "Recording unavailable"
		return r
	}
	if !state.Options.InputEnabled {
		r.Title, r.Detail, r.Input = "Microphone off", "Choose your microphone and enable input before recording. Playback is available.", "Off"
		return r
	}
	if access := state.Inventory.InputAccess; access != "" && access != "granted" {
		r.Title, r.Detail = "Allow microphone access", "Enable input and apply the settings to allow microphone access. Playback is available."
		if access == "denied" || access == "restricted" {
			r.Detail = "Allow microphone access for Cicada Studio in System Settings > Privacy & Security > Microphone, then refresh devices. Playback is available."
		} else if access == "unavailable" {
			r.Title, r.Detail = "Input access unavailable", "Microphone permission could not be checked. Refresh devices and check Audio details. Playback is available."
		}
		return r
	}
	if !input {
		r.Title, r.Detail = "Choose a microphone", "Connect your microphone, refresh devices, then choose it and apply the settings."
		return r
	}

	r.Title, r.Detail, r.Ready = "Microphone configured", "Record a vocal pass. Use headphones to keep the backing out of your microphone.", true
	return r
}

func audioConnections(state audioState) gosx.Node {
	r := recordingReadiness(state)
	connection := func(kind, name, detail string) gosx.Node {
		return gosx.El("section", gosx.Attrs(gosx.Attr("class", "audio-connection")),
			gosx.El("span", gosx.Attrs(gosx.Attr("class", "audio-connection-kind")), gosx.Text(kind)),
			gosx.El("h3", gosx.Text(name)), gosx.El("p", gosx.Text(detail)))
	}
	input := "Recording stays off until you enable it."
	if !audioCaptureSupported(state) {
		input = "Recording input is not available with this driver."
	} else if r.Ready {
		input = "Configured for recording. Your raw takes are retained."
	} else if state.Options.InputEnabled && state.Inventory.InputAccess != "" && state.Inventory.InputAccess != "granted" {
		input = "Microphone permission is needed before recording."
	}
	return gosx.El("div", gosx.Attrs(gosx.Attr("class", "audio-connections"), gosx.Attr("aria-label", "Audio connections"), gosx.Attr("data-input-ready", r.Ready), gosx.Attr("data-output-ready", r.PlaybackReady)),
		connection("INPUT", r.Input, input),
		connection("MUSIC", "Cicada", "Play, capture, reshape. The score keeps your decisions."),
		connection("OUTPUT", r.Output, "Your selected listening destination. Change it below."))
}

func audioSummary(state audioState) gosx.Node {
	r := recordingReadiness(state)
	return gosx.El("div", gosx.Attrs(gosx.Attr("class", "audio-readiness"), gosx.Attr("data-audio-ready", r.Ready)),
		gosx.El("strong", gosx.Text(r.Title)), gosx.El("p", gosx.Text(r.Detail)),
		gosx.El("dl", gosx.El("div", gosx.El("dt", gosx.Text("Microphone")), gosx.El("dd", gosx.Text(r.Input))), gosx.El("div", gosx.El("dt", gosx.Text("Playback")), gosx.El("dd", gosx.Text(r.Output)))))
}

func audioChoices(state audioState, input bool) gosx.Node {
	selected, label := state.Options.OutputDevice, "Default output"
	if input {
		selected, label = state.Options.InputDevice, "Default microphone"
	}
	for _, d := range state.Inventory.Devices {
		if !input && d.DefaultOutput && d.Outputs > 0 || input && d.DefaultInput && d.Inputs > 0 {
			label += " — " + d.Name
			break
		}
	}
	options := []gosx.Node{gosx.El("option", gosx.Attrs(gosx.Attr("value", ""), gosx.Attr("selected", selected == "")), gosx.Text(label))}
	found := selected == ""
	for _, d := range state.Inventory.Devices {
		if input && d.Inputs == 0 || !input && d.Outputs == 0 {
			continue
		}
		found = found || d.ID == selected
		options = append(options, gosx.El("option", gosx.Attrs(gosx.Attr("value", d.ID), gosx.Attr("selected", d.ID == selected)), gosx.Text(d.Name)))
	}
	// Retain an unplugged saved route instead of silently choosing the default.
	if !found {
		options = append(options, gosx.El("option", gosx.Attrs(gosx.Attr("value", selected), gosx.BoolAttr("selected")), gosx.Text("Saved device (disconnected)")))
	}
	name, accessible := "outputDevice", "Output"
	if input {
		name, accessible = "inputDevice", "Input"
	}
	return gosx.El("select", gosx.Attrs(gosx.Attr("name", name), gosx.Attr("aria-label", accessible)), gosx.Fragment(options...))
}

func audioBufferChoices(state audioState) gosx.Node {
	selected := state.Options.PeriodFrames
	if selected == 0 {
		selected = 256
	}
	var options []gosx.Node
	for _, choice := range []struct {
		Frames int
		Detail string
	}{{64, "Low latency"}, {128, "Recording"}, {256, "Balanced"}, {512, "More headroom"}, {1024, "Mixing"}} {
		options = append(options, gosx.El("option", gosx.Attrs(gosx.Attr("value", choice.Frames), gosx.Attr("selected", selected == choice.Frames)), gosx.Text(strconv.Itoa(choice.Frames)+" frames · "+choice.Detail)))
	}
	return gosx.El("select", gosx.Attrs(gosx.Attr("name", "periodFrames"), gosx.Attr("aria-label", "Buffer size")), gosx.Fragment(options...))
}

func audioPerformance(state audioState) gosx.Node {
	var rows []gosx.Node
	for _, row := range control.AudioRuntimeRows(state.Runtime, state.Options.PeriodFrames) {
		rows = append(rows, gosx.El("div", gosx.El("dt", gosx.Text(row.Label)), gosx.El("dd", gosx.Attrs(gosx.Attr("data-audio-value", row.Key)), gosx.Text(row.Value))))
	}
	warning := control.AudioRuntimeWarning(state.Runtime)
	return gosx.El("div", gosx.Attrs(gosx.Attr("class", "audio-readiness"), gosx.Attr("data-audio-runtime", "")),
		gosx.El("strong", gosx.Text("Buffer and timing")),
		gosx.El("dl", gosx.Fragment(rows...)),
		gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Round trip is the sum of device-reported input and output latency. A hardware loopback measurement is needed to confirm microphone-to-headphone delay.")),
		gosx.El("p", gosx.Attrs(gosx.Attr("role", "status"), gosx.Attr("data-audio-runtime-warning", ""), gosx.Attr("hidden", warning == "")), gosx.Text(warning)))
}

func (s *studioApp) audio(ctx *server.Context, v workspace, csrf string) gosx.Node {
	var state audioState
	if err := s.backend.call(ctx.Request.Context(), "GET", "/api/audio/config", nil, &state); err != nil {
		return failure(err)
	}
	checkbox := func(name, label string, checked bool) gosx.Node {
		return field(label, gosx.El("input", gosx.Attrs(gosx.Attr("type", "checkbox"), gosx.Attr("name", name), gosx.Attr("checked", checked))))
	}
	returnTo, apply := "/?panel=audio", "Apply audio settings"
	back := "/?panel=session"
	if track := ctx.Request.URL.Query().Get("vocalTrack"); track != "" {
		back += "&vocalTrack=" + url.QueryEscape(track)
	}
	if ctx.Request.URL.Query().Get("returnTo") == "session" {
		returnTo, apply = back, "Apply and return to recording"
	}
	var backControl gosx.Node = gosx.El("a", gosx.Attrs(gosx.Attr("href", back)), gosx.Text("Back to recording"))
	if ctx.Request.URL.Path == "/studio" {
		returnTo = back
		backControl = gosx.El("button", gosx.Attrs(gosx.Attr("type", "button"), gosx.Attr("data-gosx-disclosure-close", "#audio-dialog")), gosx.Text("Back to recording"))
	}
	locked := state.Playing || state.Runtime.Armed
	captureSettings := gosx.El("fieldset", gosx.Attrs(gosx.Attr("class", "audio-input-settings"), gosx.Attr("disabled", !audioCaptureSupported(state))),
		gosx.El("legend", gosx.Text("Recording input")), field("Input", audioChoices(state, true)), checkbox("inputEnabled", "Enable input", state.Options.InputEnabled),
		gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Apply with input enabled to allow microphone access. Input stays closed until you play or record.")))
	fields := gosx.El("fieldset", gosx.Attrs(gosx.Attr("class", "audio-settings"), gosx.Attr("data-audio-routing", ""), gosx.Attr("disabled", locked)),
		gosx.El("legend", gosx.Text("Input and playback")), field("Output", audioChoices(state, false)), captureSettings,
		field("Buffer size", audioBufferChoices(state)),
		gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Smaller buffers reduce monitoring delay. Choose more headroom if playback drops out. Your device may grant a larger buffer; sample rate and sound quality stay the same.")),
		gosx.El("details", gosx.El("summary", gosx.Text("Recording timing")), field("Recording timing correction (ms)", numberInput("recordingOffsetMS", strconv.FormatFloat(state.Options.RecordingOffsetMS, 'f', -1, 64), "-500", "500")), gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Device timing is already applied. If recorded phrases consistently land late, a positive correction advances them; a negative correction delays them. This corrects remaining input timing, not headphone monitoring latency."))),
		gosx.El("details", gosx.El("summary", gosx.Text("Headphone monitoring")), gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Monitoring only changes what you hear. Your microphone is recorded even when monitoring is muted.")),
			checkbox("monitorMuted", "Mute monitoring", state.Options.MonitorMuted), field("Monitor gain", numberInput("monitorGain", strconv.FormatFloat(state.Options.MonitorGain, 'f', -1, 64), "0", "2")), field("Monitor mode", selectInput("monitorMode", state.Options.MonitorMode, []string{"stereo", "mono1", "mono2"}, "Monitor mode"))),
		gosx.El("button", gosx.Attrs(gosx.Attr("type", "submit")), gosx.Text(apply)))
	form := ui.Form(ui.FormProps{Action: "/__actions/audio", CSRF: csrf, Revision: v.Revision, ReturnTo: returnTo, Class: "action-form"}, fields)
	setup := gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Device, input, buffer, and timing choices are saved for this computer. New sessions start stopped, with headphone monitoring muted."))
	settingsWarning := gosx.El("p", gosx.Attrs(gosx.Attr("class", "error"), gosx.Attr("role", "status"), gosx.Attr("hidden", state.SettingsWarning == "")), gosx.Text(state.SettingsWarning))
	notice := gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted"), gosx.Attr("data-audio-lock-notice", ""), gosx.Attr("hidden", !locked)), gosx.Text("Stop playback or finish the take before changing audio devices or buffer size."))

	refresh := gosx.El("a", gosx.Attrs(gosx.Attr("href", ctx.Request.URL.RequestURI()), gosx.Attr("data-workspace-refresh", ""), gosx.Attr("class", "button-link")), gosx.Text("Refresh devices"))
	details := gosx.El("details", gosx.Attrs(gosx.Attr("class", "audio-details")), gosx.El("summary", gosx.Text("Audio details")), gosx.El("p", gosx.Text(state.Status)), gosx.El("p", gosx.Text(state.Inventory.Message)), gosx.El("p", gosx.Text(state.Inventory.InputMessage)))
	var permission gosx.Node = gosx.Fragment()
	if strings.EqualFold(state.Inventory.Host, "coreaudio") || strings.Contains(strings.ToLower(state.Inventory.Backend), "coreaudio") {
		permission = gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("macOS will ask for microphone access when you enable input and apply. If access was denied, open System Settings → Privacy & Security → Microphone, allow Cicada Studio, then reopen the app. Use headphones while recording with the built-in microphone."))
	}
	return ui.Panel(ui.PanelProps{ID: "audio", Title: "Microphone and playback", Description: "Choose where you record and listen."}, audioConnections(state), audioSummary(state),
		gosx.El("div", gosx.Attrs(gosx.Attr("class", "actions")), refresh, backControl), settingsWarning, setup, notice, form, permission, audioPerformance(state), details)
}
