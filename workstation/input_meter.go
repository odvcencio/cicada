package main

import "m31labs.dev/gosx"

func inputMeter() gosx.Node {
	rows := []gosx.Node{}
	for _, channel := range []string{"L", "R"} {
		rows = append(rows, gosx.El("div", gosx.Attrs(gosx.Attr("class", "input-meter-row"), gosx.Attr("data-input-channel", channel), gosx.Attr("hidden", channel == "R")),
			gosx.El("span", gosx.Attrs(gosx.Attr("data-input-label", "")), gosx.Text("Input")),
			gosx.El("div", gosx.Attrs(gosx.Attr("class", "input-meter-bar"), gosx.Attr("role", "meter"), gosx.Attr("aria-label", "Microphone "+channel+" level"), gosx.Attr("aria-valuemin", "-60"), gosx.Attr("aria-valuemax", "0"), gosx.Attr("aria-valuenow", "-60"), gosx.Attr("aria-valuetext", "Microphone off")),
				gosx.El("span", gosx.Attrs(gosx.Attr("class", "input-meter-fill"), gosx.Attr("data-input-fill", ""))),
				gosx.El("span", gosx.Attrs(gosx.Attr("class", "input-meter-hold"), gosx.Attr("data-input-hold", ""), gosx.BoolAttr("hidden"))))))
	}
	return gosx.El("section", gosx.Attrs(gosx.Attr("class", "input-meter"), gosx.Attr("data-input-meter", ""), gosx.Attr("aria-label", "Microphone level")),
		gosx.El("div", gosx.Attrs(gosx.Attr("class", "input-meter-heading")), gosx.El("strong", gosx.Text("Microphone level")), gosx.El("span", gosx.Attrs(gosx.Attr("data-input-peak", "")), gosx.Text("Peak —"))),
		gosx.Fragment(rows...),
		gosx.El("div", gosx.Attrs(gosx.Attr("class", "input-meter-scale"), gosx.Attr("aria-hidden", "true")), gosx.El("span", gosx.Text("−60")), gosx.El("span", gosx.Text("−36")), gosx.El("span", gosx.Text("−18")), gosx.El("span", gosx.Text("−6")), gosx.El("span", gosx.Text("0 dBFS"))),
		gosx.El("p", gosx.Attrs(gosx.Attr("class", "input-meter-status"), gosx.Attr("data-input-status", ""), gosx.Attr("role", "status")), gosx.Text("Checking microphone state…")),
		gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted"), gosx.Attr("data-input-hint", "")), gosx.Text("Play the song and sing to check your level.")),
		gosx.El("small", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Peak hold resets when playback or a take starts.")))
}
