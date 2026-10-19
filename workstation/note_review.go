package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/workstation/control"
	"m31labs.dev/gosx"
)

func (s *studioApp) noteTakeReview(v workspace, csrf string, d draft) gosx.Node {
	var takes []control.Recording
	if json.Unmarshal([]byte(d.Source), &takes) != nil || control.ValidateRecordings(takes) != nil {
		return failure(fmt.Errorf("this retained note take could not be read"))
	}
	count := 0
	for _, take := range takes {
		count += len(take.Notes)
	}
	children := []gosx.Node{
		gosx.El("h3", gosx.Text("Retained note take")),
		gosx.El("p", gosx.Attrs(gosx.Attr("class", "take-summary")), gosx.Text(fmt.Sprintf("%s · %s", noteCount(count), destinationCount(len(takes))))),
		gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Keep played timing preserves individual starts, held lengths, pitches, velocities and supported note gestures in a looping performance pattern. Acid and authored synths keep bend, pressure, timbre and vibrato. Every destination shares the first played bar as its origin and the final release rounds the loop up to a whole bar. Zero-length hits use a one-tick gate.")),
	}
	for _, take := range takes {
		var pattern *project.Pattern
		for i := range v.Project.Patterns {
			if v.Project.Patterns[i].ID == take.Pattern {
				pattern = &v.Project.Patterns[i]
				break
			}
		}
		children = append(children, noteRecordingReview(take, pattern))
	}
	playedBlocked := d.PlayedCheckRevision == v.Revision && d.PlayedCheckError != ""
	if playedBlocked {
		children = append(children, gosx.El("p", gosx.Attrs(gosx.Attr("class", "take-check take-check-error")), gosx.Text("Played timing: "+d.PlayedCheckError+" Your performance is still retained.")))
	}
	children = append(children, s.form(v, csrf, "live", "note-commit", gosx.El("button", gosx.Attrs(gosx.Attr("type", "submit"), gosx.Attr("name", "intent"), gosx.Attr("value", "played"), gosx.Attr("disabled", playedBlocked)), gosx.Text("Keep played timing"))))
	children = append(children, gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Replaces the destination patterns. Their current sounds and scene assignments stay in use; song and placement lengths stay as authored. Use the pattern view to play the full loop. Undo restores the previous patterns.")))
	intent, label := "commit", "Commit notes to patterns"
	if d.Revision != v.Revision {
		intent, label = "rebase", "Apply notes to current score"
		children = append(children, gosx.El("p", gosx.Attrs(gosx.Attr("class", "take-check")), gosx.Text("The score changed since this take. Check the current patterns before applying.")))
	}
	blocked := d.CheckRevision == v.Revision && d.CheckError != ""
	var gridCheck gosx.Node = gosx.Fragment()
	if blocked {
		reason := strings.TrimSpace(d.CheckError)
		if !strings.HasSuffix(reason, ".") {
			reason += "."
		}
		gridCheck = gosx.El("p", gosx.Attrs(gosx.Attr("class", "take-check take-check-error"), gosx.Attr("role", "status")), gosx.Text("Pattern check: "+reason+" Your performance is still retained."))
	} else if d.CheckRevision == v.Revision {
		gridCheck = gosx.El("p", gosx.Attrs(gosx.Attr("class", "take-check")), gosx.Text("Pattern check passed. The score is unchanged until you apply. Undo restores the previous patterns."))
	}
	apply := gosx.El("button", gosx.Attrs(gosx.Attr("type", "submit"), gosx.Attr("name", "intent"), gosx.Attr("value", intent), gosx.Attr("disabled", blocked)), gosx.Text(label))
	children = append(children, gosx.El("details", gosx.El("summary", gosx.Text("Snap to the existing step grid")), gridCheck, gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Snaps starts to the destination grid and repeats at its saved length. Held lengths and overlapping notes may need a played-timing pattern instead.")), s.form(v, csrf, "live", "note-commit", apply)), s.form(v, csrf, "live", "note-commit", submit("intent", "check", "Check current patterns"), submit("intent", "discard", "Discard note take")))
	return gosx.El("section", gosx.Attrs(gosx.Attr("class", "note-preview"), gosx.Attr("aria-label", "Retained note take")), gosx.Fragment(children...))
}

func noteCount(n int) string {
	if n == 1 {
		return "1 note"
	}
	return fmt.Sprintf("%d notes", n)
}
func destinationCount(n int) string {
	if n == 1 {
		return "1 pattern destination"
	}
	return fmt.Sprintf("%d pattern destinations", n)
}
func performancePitch(note int) string {
	return fmt.Sprintf("%s%d", [...]string{"C", "C♯", "D", "D♯", "E", "F", "F♯", "G", "G♯", "A", "A♯", "B"}[note%12], note/12-1)
}
func performancePosition(tick int64) string {
	position := fmt.Sprintf("%d.%d.%d", tick/seq.TicksPerBar+1, tick%seq.TicksPerBar/seq.PPQ+1, tick%seq.PPQ/seq.TicksPerStep+1)
	if remainder := tick % seq.TicksPerStep; remainder != 0 {
		position += fmt.Sprintf(" +%d ticks", remainder)
	}
	return position
}
func performanceDuration(ticks int64) string {
	value := strings.TrimRight(strings.TrimRight(strconv.FormatFloat(float64(ticks)/float64(seq.PPQ), 'f', 3, 64), "0"), ".")
	if ticks == seq.PPQ {
		return value + " beat"
	}
	return value + " beats"
}

func noteRecordingReview(take control.Recording, pattern *project.Pattern) gosx.Node {
	// Display a sorted copy; captured order and raw note timing stay intact.
	notes := append([]control.Note(nil), take.Notes...)
	sort.SliceStable(notes, func(i, j int) bool { return notes[i].Tick < notes[j].Tick })
	stepTicks, steps, drums := seq.TicksPerStep, int64(0), false
	if pattern != nil && pattern.Performance == nil {
		stepTicks, steps, drums = (edit.Pattern{StepTicks: pattern.StepTicks}).StepTicksOrDefault(), int64(pattern.Steps), pattern.Kind == "drums"
	}
	key := func(n control.Note) string {
		if steps == 0 {
			return ""
		}
		step := (n.Tick+stepTicks/2)/stepTicks%steps + 1
		lane := uint16(0)
		if drums {
			lane, _ = edit.GMDrumLane(n.Note)
		}
		return fmt.Sprintf("%d:%d", step, lane)
	}
	counts := map[string]int{}
	for _, n := range notes {
		counts[key(n)]++
	}
	var rows []gosx.Node
	nativeTiming, estimatedTiming := false, false
	for _, n := range notes {
		duration := performanceDuration(n.EndTick - n.Tick)
		if n.EndEstimated {
			duration += " · estimated"
			estimatedTiming = true
		}
		if n.Input != nil {
			nativeTiming = true
			if !n.EndEstimated && (n.Input.Estimated || n.Input.EndHostNanos == 0) {
				duration += " · estimated"
				estimatedTiming = true
			}
		}
		placement := "Target unavailable"
		if pattern != nil && pattern.Performance != nil {
			placement = "Played timing"
		}
		if steps > 0 {
			placement = fmt.Sprintf("Step %d", (n.Tick+stepTicks/2)/stepTicks%steps+1)
		}
		class := ""
		if steps > 0 && counts[key(n)] > 1 {
			placement += " · shared step"
			class = "take-collision"
		}
		var cells []gosx.Node
		for _, text := range []string{performancePitch(n.Note), performancePosition(n.Tick), duration, strconv.Itoa(n.Velocity), placement} {
			cells = append(cells, gosx.El("td", gosx.Text(text)))
		}
		rows = append(rows, gosx.El("tr", gosx.Attrs(gosx.Attr("class", class)), gosx.Fragment(cells...)))
	}
	var heads []gosx.Node
	for _, text := range []string{"Note", "Played at", "Held for", "Velocity", "Pattern destination"} {
		heads = append(heads, gosx.El("th", gosx.Attrs(gosx.Attr("scope", "col")), gosx.Text(text)))
	}
	destination := "Target pattern is no longer available."
	if pattern != nil && pattern.Performance != nil {
		destination = "Played timing · " + performanceDuration(pattern.Performance.LengthTicks) + " loop"
	}
	if steps > 0 {
		destination = fmt.Sprintf("%d steps · %s per step", steps, performanceDuration(stepTicks))
	}
	table := gosx.El("table",
		gosx.El("caption", gosx.Text("Captured notes for "+take.Track+" → "+take.Pattern)),
		gosx.El("thead", gosx.El("tr", gosx.Fragment(heads...))),
		gosx.El("tbody", gosx.Fragment(rows...)),
	)
	inspection := gosx.El("details",
		gosx.El("summary", gosx.Text("Inspect "+noteCount(len(notes)))),
		gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Played at uses bar.beat.sixteenth, with the remaining ticks shown at 960 ticks per beat.")),
		gosx.El("div", gosx.Attrs(gosx.Attr("class", "take-table-scroll"), gosx.Attr("tabindex", "0"), gosx.Attr("role", "region"), gosx.Attr("aria-label", take.Track+" recorded notes")), table),
	)
	if nativeTiming {
		message := "MIDI timing uses the controller’s timestamps when supplied; otherwise it uses the time input reached this Mac."
		if estimatedTiming {
			message += " Estimated lengths include notes released without an input timestamp or a matching playback clock."
		}
		inspection = gosx.Fragment(inspection, gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text(message)))
	}
	return gosx.El("article", gosx.Attrs(gosx.Attr("class", "take-recording")),
		gosx.El("div", gosx.Attrs(gosx.Attr("class", "take-destination")), gosx.El("h4", gosx.Text(take.Track+" → "+take.Pattern)), gosx.El("span", gosx.Text(noteCount(len(notes))+" · "+destination))),
		notePerformanceTrace(notes, take.Track),
		inspection,
	)
}

func notePerformanceTrace(notes []control.Note, track string) gosx.Node {
	start, end, low, high := notes[0].Tick, notes[0].EndTick, notes[0].Note, notes[0].Note
	for _, n := range notes {
		start = min(start, n.Tick)
		end = max(end, n.EndTick, n.Tick+1)
		low = min(low, n.Note)
		high = max(high, n.Note)
	}
	origin := start / seq.TicksPerBar * seq.TicksPerBar
	bars := (end - origin + seq.TicksPerBar - 1) / seq.TicksPerBar
	span := max(seq.TicksPerBar, bars*seq.TicksPerBar)
	x := func(tick int64) float64 { return 24 + 912*float64(tick-origin)/float64(span) }
	number := func(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }
	var marks []gosx.Node
	spacing := max(int64(1), (bars+7)/8)
	for bar := int64(0); bar <= bars; bar += spacing {
		position := number(x(origin + bar*seq.TicksPerBar))
		marks = append(marks, gosx.El("line", gosx.Attrs(gosx.Attr("x1", position), gosx.Attr("x2", position), gosx.Attr("y1", "28"), gosx.Attr("y2", "130"))), gosx.El("text", gosx.Attrs(gosx.Attr("x", position), gosx.Attr("y", "22")), gosx.Text(fmt.Sprintf("%d", origin/seq.TicksPerBar+bar+1))))
	}
	for _, n := range notes {
		y := float64(76)
		if high > low {
			y = 42 + 68*float64(high-n.Note)/float64(high-low)
		}
		marks = append(marks, gosx.El("rect", gosx.Attrs(gosx.Attr("x", number(x(n.Tick))), gosx.Attr("y", number(y)), gosx.Attr("width", number(max(3., x(n.EndTick)-x(n.Tick)))), gosx.Attr("height", "10"), gosx.Attr("rx", "3"), gosx.Attr("opacity", number(.35+.65*float64(n.Velocity)/127))), gosx.El("title", gosx.Text(performancePitch(n.Note)+" · "+performancePosition(n.Tick)+" · "+performanceDuration(n.EndTick-n.Tick)))))
	}
	graph := gosx.El("svg", gosx.Attrs(gosx.Attr("viewBox", "0 0 960 142"), gosx.Attr("role", "img"), gosx.Attr("aria-label", track+" performance: "+noteCount(len(notes))+", "+performancePitch(low)+" to "+performancePitch(high))), gosx.Fragment(marks...))
	return gosx.El("figure", gosx.Attrs(gosx.Attr("class", "take-trace")),
		gosx.El("div", gosx.Attrs(gosx.Attr("class", "take-trace-scroll"), gosx.Attr("tabindex", "0"), gosx.Attr("role", "region"), gosx.Attr("aria-label", track+" performance timeline")), graph),
		gosx.El("figcaption", gosx.Text(performancePitch(low)+"–"+performancePitch(high)+" · Bar "+strconv.FormatInt(origin/seq.TicksPerBar+1, 10)+" onward · Line length is held time; brightness is velocity.")),
	)
}
