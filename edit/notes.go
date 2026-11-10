package edit

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
)

// EditNotes applies one batch of note operations to one pattern as one edit:
// the pattern compiles once, every named note is resolved, the operation runs
// in memory, and the source is written once. One request is one commit and one
// Undo step.
//
// Entity is pattern:<id>. Op is move, resize, velocity, delete, insert or
// quantize. Notes names the notes to change as the client saw them; the edit
// is refused when any of them is missing or differs. A note is identified by
// its start and its pitch (Pitch is the stored MIDI pitch, before the
// pattern's transpose) or, in drum patterns, its lane. Quantize with no notes
// acts on every note of the pattern.
//
// At, Length, DT, DL and Grid are ticks (960 per beat) for every pattern kind;
// step patterns and drum lanes accept only whole steps. DP moves pitch in
// semitones, or lanes in authored lane order in drum patterns. DV changes
// velocity; Velocity, when set, replaces it instead. Strength is the quantize
// percentage (0 means 100); step patterns and drum lanes quantize at 100.
// Copy keeps the originals of a move. Insert lists the notes an insert adds.
// Shared accepts only "pattern": phrase uses expand inside the edited pattern.
type EditNotes struct {
	Entity   EntityID  `json:"entity"`
	Op       string    `json:"op"`
	Notes    []NoteRef `json:"notes,omitempty"`
	DT       int64     `json:"dt,omitempty"`
	DP       int       `json:"dp,omitempty"`
	DL       int64     `json:"dl,omitempty"`
	DV       int       `json:"dv,omitempty"`
	Velocity int       `json:"velocity,omitempty"`
	Grid     int64     `json:"grid,omitempty"`
	Strength int       `json:"strength,omitempty"`
	Copy     bool      `json:"copy,omitempty"`
	Insert   []NoteRef `json:"insert,omitempty"`
	Shared   string    `json:"shared,omitempty"`
}

// NoteRef is one note as the editor saw it, or one note to insert.
type NoteRef struct {
	At       int64  `json:"at"`
	Pitch    int    `json:"pitch"`
	Lane     string `json:"lane,omitempty"`
	Length   int64  `json:"length"`
	Velocity int    `json:"velocity"`
}

func (EditNotes) Kind() string { return "editnotes" }

// MaxEditNotes bounds the notes one batch may name or insert: the largest
// performance (seq.MaxPerformanceNotes).
const MaxEditNotes = seq.MaxPerformanceNotes

// EditNotesOps lists the operations EditNotes accepts.
var EditNotesOps = []string{"move", "resize", "velocity", "delete", "insert", "quantize"}

func init() {
	Register("editnotes", func() Intent { return &EditNotes{} })
	Handle("editnotes", editNotes)
}

// NoteError is a refusal about one note. Error() names the note for a person;
// Code and the note's identity let a host report it as data. At is the start in
// ticks. Step is one-based in step patterns and drum lanes, and 0 in played
// performances. Pitch is the stored MIDI pitch; drum hits have a Lane instead.
type NoteError struct {
	Code    string
	At      int64
	Step    int
	Pitch   *int
	Lane    string
	Message string
}

func (e *NoteError) Error() string { return e.Message }

// Note returns the note's identity as JSON-ready fields.
func (e *NoteError) Note() map[string]any {
	note := map[string]any{"at": e.At}
	if e.Step > 0 {
		note["step"] = e.Step
	}
	if e.Pitch != nil {
		note["pitch"] = *e.Pitch
	}
	if e.Lane != "" {
		note["lane"] = e.Lane
	}
	return note
}

// NoteErrorCodes lists every reason code a NoteError can carry.
var NoteErrorCodes = []string{
	"invalid-note", "note-changed", "no-lanes", "not-whole-steps", "outside-pattern",
	"outside-lanes", "outside-loop", "pitch-range", "too-short", "gesture-cut",
	"overlap", "same-pitch", "same-cell", "chord-pitches-equal", "chord-size",
	"chord-articulation", "chord-length", "chord-modifiers", "acid-one-pitch",
	"fixed-length", "unknown-lane",
}

// performedNoteError names a note of a played performance by pitch and tick.
func performedNoteError(code string, at int64, pitch int, format string, args ...any) *NoteError {
	return &NoteError{Code: code, At: at, Pitch: &pitch, Message: fmt.Sprintf("note %d (%s) at tick %d: ", pitch, notePitchName(pitch), at) + fmt.Sprintf(format, args...)}
}

// Validate checks the shape of a batch without reading a score.
func (in *EditNotes) Validate() error {
	known := false
	for _, op := range EditNotesOps {
		known = known || in.Op == op
	}
	if !known {
		return fmt.Errorf("choose a note operation: %s", strings.Join(EditNotesOps, ", "))
	}
	if len(in.Notes) > MaxEditNotes || len(in.Insert) > MaxEditNotes {
		return fmt.Errorf("a note edit may name at most %d notes", MaxEditNotes)
	}
	if in.Shared != "" && in.Shared != "pattern" {
		return fmt.Errorf("note edits expand phrase uses inside the pattern; shared must be \"pattern\"")
	}
	const tickLimit = int64(1) << 40
	for field, refs := range map[string][]NoteRef{"notes": in.Notes, "insert": in.Insert} {
		for i, ref := range refs {
			if ref.At < 0 || ref.At > tickLimit || ref.Length < 0 || ref.Length > tickLimit || ref.Pitch < 0 || ref.Pitch > 127 || ref.Velocity < 0 || ref.Velocity > 127 {
				pitch := ref.Pitch
				return &NoteError{Code: "invalid-note", At: ref.At, Pitch: &pitch, Lane: ref.Lane, Message: fmt.Sprintf("%s[%d] (at %d, pitch %d, length %d, velocity %d): a note needs a start and length of 0 to 2^40 ticks, MIDI pitch 0–127 and velocity 0–127", field, i, ref.At, ref.Pitch, ref.Length, ref.Velocity)}
			}
		}
	}
	if in.DT < -tickLimit || in.DT > tickLimit || in.DL < -tickLimit || in.DL > tickLimit || in.DP < -127 || in.DP > 127 || in.DV < -126 || in.DV > 126 || in.Velocity < 0 || in.Velocity > 127 {
		return fmt.Errorf("note changes are out of range: dt and dl within ±2^40 ticks, dp within ±127, dv within ±126, velocity 1–127")
	}
	if in.Velocity != 0 && in.DV != 0 {
		return fmt.Errorf("set velocity (a new value) or dv (a change), not both")
	}
	switch in.Op {
	case "move":
		if len(in.Notes) == 0 {
			return fmt.Errorf("choose the notes to move")
		}
	case "resize":
		if len(in.Notes) == 0 {
			return fmt.Errorf("choose the notes to resize")
		}
	case "velocity":
		if len(in.Notes) == 0 {
			return fmt.Errorf("choose the notes to change")
		}
		if in.Velocity == 0 && in.DV == 0 {
			return fmt.Errorf("choose a velocity from 1 to 127 or a change")
		}
	case "delete":
		if len(in.Notes) == 0 {
			return fmt.Errorf("choose the notes to delete")
		}
	case "insert":
		if len(in.Insert) == 0 {
			return fmt.Errorf("choose the notes to insert")
		}
		for _, ref := range in.Insert {
			if ref.Length < 1 || ref.Velocity < 1 {
				return fmt.Errorf("an inserted note needs a length and a velocity from 1 to 127")
			}
		}
	case "quantize":
		if in.Grid < 30 || in.Grid > seq.TicksPerBar {
			return fmt.Errorf("choose a quantize grid from 30 to %d ticks", seq.TicksPerBar)
		}
		if in.Strength < 0 || in.Strength > 100 {
			return fmt.Errorf("quantize strength must be 1–100%%")
		}
	}
	return nil
}

func (in *EditNotes) strength() int64 {
	if in.Strength == 0 {
		return 100
	}
	return int64(in.Strength)
}

func editNotes(ctx *Context, intent Intent) error {
	in := intent.(*EditNotes)
	id, err := patternTarget(in.Entity)
	if err != nil {
		return err
	}
	if err := in.Validate(); err != nil {
		return err
	}
	plan, err := ctx.CurrentPlan()
	if err != nil {
		return err
	}
	for _, pattern := range plan.Patterns {
		if pattern.ID != id {
			continue
		}
		if pattern.Performance != nil {
			return editPerformedNotes(ctx, id, pattern, in)
		}
		return gridSourceEdit(editGridNotes)(ctx, intent)
	}
	return fmt.Errorf("unknown pattern %q", id)
}

// quantizeTick rounds tick to the nearest grid line, half up, as a recorded
// take's onsets are (record.go), then applies strength.
func quantizeTick(tick, grid, strength int64) int64 {
	target := (tick + grid/2) / grid * grid
	return tick + (target-tick)*strength/100
}

func gridLabel(ticks int64) string {
	switch ticks {
	case seq.TicksPerBar:
		return "1 bar"
	case seq.PPQ:
		return "1 beat"
	case seq.PPQ / 2:
		return "1/8"
	case seq.PPQ / 3:
		return "1/8T"
	case seq.PPQ / 4:
		return "1/16"
	case seq.PPQ / 6:
		return "1/16T"
	case seq.PPQ / 8:
		return "1/32"
	}
	return fmt.Sprintf("%d ticks", ticks)
}

var notePitchNames = [...]string{"C", "C#", "D", "D#", "E", "F", "F#", "G", "G#", "A", "A#", "B"}

func notePitchName(pitch int) string {
	return notePitchNames[((pitch%12)+12)%12] + strconv.Itoa(pitch/12-1)
}

func notesLabel(id string, in *EditNotes, count int) string {
	noun := "notes"
	if count == 1 {
		noun = "note"
	}
	switch in.Op {
	case "quantize":
		return fmt.Sprintf("Notes · %s · %d %s quantized to %s", id, count, noun, gridLabel(in.Grid))
	case "move":
		if in.Copy {
			return fmt.Sprintf("Notes · %s · %d %s copied", id, count, noun)
		}
		return fmt.Sprintf("Notes · %s · %d %s moved", id, count, noun)
	case "resize":
		return fmt.Sprintf("Notes · %s · %d %s resized", id, count, noun)
	case "velocity":
		return fmt.Sprintf("Notes · %s · %d %s velocity changed", id, count, noun)
	case "delete":
		return fmt.Sprintf("Notes · %s · %d %s deleted", id, count, noun)
	}
	return fmt.Sprintf("Notes · %s · %d %s added", id, count, noun)
}

// opName is the operation's name as a person reads it in a refusal.
func opName(in *EditNotes) string {
	if in.Op == "move" && in.Copy {
		return "copy"
	}
	return in.Op
}

func clampVelocity(value int) int {
	return max(1, min(127, value))
}

func (in *EditNotes) newVelocity(current int) int {
	if in.Velocity != 0 {
		return in.Velocity
	}
	return clampVelocity(current + in.DV)
}

// unchangedNotes reports a batch that writes nothing. The service returns
// "unchanged" with this message and records no history.
func unchangedNotes(ctx *Context, in *EditNotes) error {
	message := "Nothing changed."
	if in.Op == "quantize" {
		message = "Already on the grid."
	}
	ctx.Respond("message", message)
	return nil
}

// --- performances -----------------------------------------------------------

func clonePerformance(source *seq.Performance) *seq.Performance {
	copy := *source
	copy.Notes = append([]seq.PerformedNote(nil), source.Notes...)
	copy.Groups = append([]seq.ControlGroup(nil), source.Groups...)
	for i := range copy.Groups {
		copy.Groups[i].Points = append([]seq.GroupPoint(nil), copy.Groups[i].Points...)
	}
	for i := range copy.Notes {
		copy.Notes[i].Expressions = append([]seq.PerformedExpression(nil), copy.Notes[i].Expressions...)
	}
	return &copy
}

// editPerformedNotes changes notes in place by index, so gestures and shared
// gesture bindings stay attached, appends inserted notes, validates, and
// rewrites the declaration once.
func editPerformedNotes(ctx *Context, id string, pattern Pattern, in *EditNotes) error {
	performance := clonePerformance(pattern.Performance)
	transpose := int(pattern.Transpose)
	type key struct {
		tick  int64
		pitch int
	}
	byKey := make(map[key]int, len(performance.Notes))
	for i, note := range performance.Notes {
		byKey[key{note.Tick, int(note.Note)}] = i
	}
	var selected []int
	seen := map[int]bool{}
	for _, ref := range in.Notes {
		changed := func(why string, args ...any) error {
			return performedNoteError("note-changed", ref.At, ref.Pitch, "a selected note changed ("+why+"); select the notes again", args...)
		}
		if ref.Lane != "" {
			return performedNoteError("no-lanes", ref.At, ref.Pitch, "a played performance has no drum lanes, so lane %q cannot be named", ref.Lane)
		}
		index, ok := byKey[key{ref.At, ref.Pitch}]
		if !ok {
			return changed("no note of this pitch starts at this tick")
		}
		if seen[index] {
			return changed("the batch names it twice")
		}
		note := performance.Notes[index]
		if note.EndTick-note.Tick != ref.Length {
			return changed("it lasts %d ticks, not %d", note.EndTick-note.Tick, ref.Length)
		}
		if int(note.Velocity) != ref.Velocity {
			return changed("its velocity is %d, not %d", note.Velocity, ref.Velocity)
		}
		seen[index] = true
		selected = append(selected, index)
	}
	if len(in.Notes) == 0 && in.Op == "quantize" {
		for i := range performance.Notes {
			selected = append(selected, i)
		}
	}
	pitchOK := func(pitch int) bool {
		return pitch >= 0 && pitch <= 127 && pitch+transpose >= 0 && pitch+transpose <= 127
	}
	// origins[i] is note i as the request found it, so a refusal can name it.
	origins := append([]seq.PerformedNote(nil), performance.Notes...)
	length := performance.LengthTicks
	changed := 0
	switch in.Op {
	case "move":
		var copies []seq.PerformedNote
		for _, index := range selected {
			original := performance.Notes[index]
			note := original
			note.Tick += in.DT
			note.EndTick += in.DT
			pitch := int(note.Note) + in.DP
			refuse := func(code, format string, args ...any) error {
				return performedNoteError(code, original.Tick, int(original.Note), opName(in)+" "+format, args...)
			}
			if note.Tick < 0 {
				return refuse("outside-loop", "would start it at tick %d, before the loop starts", note.Tick)
			}
			if note.EndTick > length {
				return refuse("outside-loop", "would end it at tick %d, past the loop end at tick %d; lengthen the loop first", note.EndTick, length)
			}
			if !pitchOK(pitch) {
				return refuse("pitch-range", "would make it MIDI %d, which with the pattern transpose of %+d leaves 0–127", pitch, transpose)
			}
			note.Note = uint8(pitch)
			if in.Copy {
				note.Expressions = append([]seq.PerformedExpression(nil), note.Expressions...)
				copies = append(copies, note)
				origins = append(origins, original)
			} else {
				performance.Notes[index] = note
			}
		}
		performance.Notes = append(performance.Notes, copies...)
		if in.DT != 0 || in.DP != 0 {
			changed = len(selected)
		}
	case "resize":
		for _, index := range selected {
			note := &performance.Notes[index]
			end := note.EndTick + in.DL
			if end <= note.Tick {
				return performedNoteError("too-short", note.Tick, int(note.Note), "resize would leave it %d ticks long; a note lasts at least one tick", end-note.Tick)
			}
			if end > length {
				return performedNoteError("outside-loop", note.Tick, int(note.Note), "resize would end it at tick %d, past the loop end at tick %d; lengthen the loop first", end, length)
			}
			if last := len(note.Expressions) - 1; last >= 0 && note.Expressions[last].Tick > end-note.Tick {
				return performedNoteError("gesture-cut", note.Tick, int(note.Note), "resize to %d ticks would cut its gesture point at %d ticks; a note cannot become shorter than its last gesture point", end-note.Tick, note.Expressions[last].Tick)
			}
			if end != note.EndTick {
				changed++
			}
			note.EndTick = end
		}
	case "velocity":
		for _, index := range selected {
			note := &performance.Notes[index]
			velocity := uint8(in.newVelocity(int(note.Velocity)))
			if velocity != note.Velocity {
				changed++
			}
			note.Velocity = velocity
		}
	case "delete":
		if len(selected) >= len(performance.Notes) {
			return fmt.Errorf("a performance keeps at least one note; delete the pattern instead")
		}
		sort.Sort(sort.Reverse(sort.IntSlice(selected)))
		for _, index := range selected {
			performance.Notes = append(performance.Notes[:index], performance.Notes[index+1:]...)
			origins = append(origins[:index], origins[index+1:]...)
		}
		changed = len(selected)
	case "insert":
		for _, ref := range in.Insert {
			if ref.Lane != "" {
				return performedNoteError("no-lanes", ref.At, ref.Pitch, "a played performance has no drum lanes, so lane %q cannot be named", ref.Lane)
			}
			if ref.At+ref.Length > length {
				return performedNoteError("outside-loop", ref.At, ref.Pitch, "insert would end it at tick %d, past the loop end at tick %d; lengthen the loop first", ref.At+ref.Length, length)
			}
			if !pitchOK(ref.Pitch) {
				return performedNoteError("pitch-range", ref.At, ref.Pitch, "insert would place MIDI %d, which with the pattern transpose of %+d leaves 0–127", ref.Pitch, transpose)
			}
			inserted := seq.PerformedNote{Tick: ref.At, EndTick: ref.At + ref.Length, Note: uint8(ref.Pitch), Velocity: uint8(ref.Velocity)}
			performance.Notes = append(performance.Notes, inserted)
			origins = append(origins, inserted)
		}
		changed = len(in.Insert)
	case "quantize":
		strength := in.strength()
		for _, index := range selected {
			note := &performance.Notes[index]
			tick := quantizeTick(note.Tick, in.Grid, strength)
			if tick >= length {
				tick = 0 // a start that rounds to the loop end begins the loop
			}
			if tick == note.Tick {
				continue
			}
			end := note.EndTick + tick - note.Tick
			if end > length {
				return performedNoteError("outside-loop", note.Tick, int(note.Note), "quantize would move it to tick %d and end it at tick %d, past the loop end at tick %d; lengthen the loop or choose a finer grid", tick, end, length)
			}
			note.Tick, note.EndTick = tick, end
			changed++
		}
	}
	if changed == 0 {
		return unchangedNotes(ctx, in)
	}
	if len(performance.Notes) > seq.MaxPerformanceNotes {
		return fmt.Errorf("a performance may contain at most %d notes", seq.MaxPerformanceNotes)
	}
	if err := performedOverlap(performance.Notes, origins, in); err != nil {
		return err
	}
	if err := performance.Validate(); err != nil {
		return err
	}
	if err := replaceProjectPerformance(ctx, id, performance, true); err != nil {
		return err
	}
	ctx.SetLabel(notesLabel(id, in, changed))
	return nil
}

// performedOverlap names the first pair of same-pitch gates that overlap,
// by the note the batch moved, as the request found it. seq.Performance.Validate
// refuses them too, without saying where.
func performedOverlap(notes, origins []seq.PerformedNote, in *EditNotes) error {
	order := make([]int, len(notes))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		x, y := notes[order[a]], notes[order[b]]
		if x.Note != y.Note {
			return x.Note < y.Note
		}
		return x.Tick < y.Tick
	})
	same := func(a, b seq.PerformedNote) bool {
		return a.Tick == b.Tick && a.EndTick == b.EndTick && a.Note == b.Note
	}
	for i := 1; i < len(order); i++ {
		earlier, later := order[i-1], order[i]
		if notes[earlier].Note != notes[later].Note || notes[later].Tick >= notes[earlier].EndTick {
			continue
		}
		culprit, other := later, earlier
		if same(notes[later], origins[later]) {
			culprit, other = earlier, later
		}
		hint := "move one of them first"
		if in.Op == "quantize" {
			hint = "choose a finer grid"
		}
		note, origin, them := notes[culprit], origins[culprit], notes[other]
		return performedNoteError("overlap", origin.Tick, int(origin.Note), "%s would put it at tick %d to %d as %s, over the %s at tick %d to %d; a pitch plays one note at a time, so %s", opName(in), note.Tick, note.EndTick, notePitchName(int(note.Note)), notePitchName(int(them.Note)), them.Tick, them.EndTick, hint)
	}
	return nil
}

// --- step patterns and drum lanes --------------------------------------------

func editGridNotes(ctx *Context, intent Intent) error {
	in := intent.(*EditNotes)
	id, err := patternTarget(in.Entity)
	if err != nil {
		return err
	}
	// One parse validates the project (gridSourceEdit gives this context the
	// whole project) and finds the pattern. Phrase uses are expanded inside
	// this pattern, as range edits do, only when it has any; the expansion is
	// written only with a real change.
	local := ctx.Source
	working := ctx
	score, ds := working.Parse()
	if score == nil || hasErrors(ds) {
		return fmt.Errorf("score must validate before a note edit")
	}
	uses := false
	for _, pattern := range score.Patterns {
		for _, part := range pattern.Parts {
			uses = uses || pattern.Name == id && part.Use != nil
		}
	}
	if uses {
		if local, err = independentPatternSource(ctx, id); err != nil {
			return err
		}
		working = sourceContext(ctx, local)
		if score, ds = working.Parse(); score == nil || hasErrors(ds) {
			return fmt.Errorf("pattern must validate after expanding its phrase uses")
		}
	}
	plan, err := working.CurrentPlan()
	if err != nil || plan == nil {
		return fmt.Errorf("pattern must compile before a note edit")
	}
	var authored *notation.Pattern
	for i := range score.Patterns {
		if score.Patterns[i].Name == id {
			authored = &score.Patterns[i]
		}
	}
	var compiled *Pattern
	for i := range plan.Patterns {
		if plan.Patterns[i].ID == id {
			compiled = &plan.Patterns[i]
		}
	}
	if authored == nil || compiled == nil {
		return fmt.Errorf("unknown pattern %q", id)
	}
	grid := gridNotes{in: in, source: local, path: ctx.Options.Path, authored: authored, compiled: compiled, step: compiled.StepTicksOrDefault()}
	var spans []Span
	var count int
	if authored.Kind == "drums" {
		spans, count, err = grid.drums()
	} else {
		spans, count, err = grid.stepPattern()
	}
	if err != nil {
		return err
	}
	if len(spans) == 0 {
		return unchangedNotes(ctx, in)
	}
	updated, err := PatchSpans(local, ctx.Options.Path, spans)
	if err != nil {
		return err
	}
	ctx.Source, ctx.plan = updated, nil
	ctx.SetLabel(notesLabel(id, in, count))
	return nil
}

type gridNotes struct {
	in       *EditNotes
	source   []byte
	path     string
	authored *notation.Pattern
	compiled *Pattern
	step     int64 // ticks per step
}

// whole converts a tick amount to whole steps, refusing anything else.
func (g gridNotes) whole(ticks int64, what string) (int, error) {
	if ticks%g.step != 0 {
		return 0, fmt.Errorf("%s of %d ticks is not a whole number of %d-tick steps in this pattern; quantize a performance before pasting it into a step pattern", what, ticks, g.step)
	}
	return int(ticks / g.step), nil
}

func stepsText(n int) string {
	if n == 1 {
		return "1 step"
	}
	return strconv.Itoa(n) + " steps"
}

// noteAt names a note of a step pattern by its one-based step and spelling.
func (g gridNotes) noteAt(code string, step, pitch int, format string, args ...any) *NoteError {
	return &NoteError{Code: code, At: int64(step) * g.step, Step: step + 1, Pitch: &pitch, Message: fmt.Sprintf("step %d, %s: ", step+1, sourcePitch(pitch)) + fmt.Sprintf(format, args...)}
}

// origin returns where the request found an onset: its source step and its
// first pitch before the batch moved it.
func (g gridNotes) origin(o *stepOnset) (step, pitch int) {
	if o.origin == nil {
		return o.start, o.pitches()[0]
	}
	if o.base != nil {
		return o.origin.start, o.pitches()[0] - o.shift
	}
	return o.origin.start, o.add[0].pitch - g.in.DP
}

// hitAt names a drum hit by its lane and one-based step.
func (g gridNotes) hitAt(code string, step int, lane, format string, args ...any) *NoteError {
	return &NoteError{Code: code, At: int64(step) * g.step, Step: step + 1, Lane: lane, Message: fmt.Sprintf("%s step %d: ", lane, step+1) + fmt.Sprintf(format, args...)}
}

// refAt names a note exactly as a request named it, for a start that is not a
// whole step or a step outside the pattern.
func (g gridNotes) refAt(code string, ref NoteRef, format string, args ...any) *NoteError {
	pitch := ref.Pitch
	err := &NoteError{Code: code, At: ref.At, Lane: ref.Lane}
	if ref.Lane != "" {
		err.Message = fmt.Sprintf("the hit at tick %d in lane %s: ", ref.At, ref.Lane)
	} else {
		err.Pitch = &pitch
		err.Message = fmt.Sprintf("the %s at tick %d: ", sourcePitch(pitch), ref.At)
	}
	if ref.At%g.step == 0 {
		err.Step = int(ref.At/g.step) + 1
	}
	err.Message += fmt.Sprintf(format, args...)
	return err
}

// quantizeSteps returns the grid in steps, or 0 when the step grid is already
// as coarse as the requested grid.
func (g gridNotes) quantizeSteps() (int, error) {
	if g.in.strength() != 100 {
		return 0, fmt.Errorf("step patterns and drum lanes quantize at 100%% strength")
	}
	if g.in.Grid <= g.step {
		return 0, nil
	}
	if g.in.Grid%g.step != 0 {
		return 0, fmt.Errorf("choose a quantize grid that is a whole number of %d-tick steps", g.step)
	}
	return int(g.in.Grid / g.step), nil
}

func (g gridNotes) quantized(start, size, count int) int {
	target := int(quantizeTick(int64(start), int64(size), 100))
	if target >= count {
		return 0 // a start that rounds to the pattern end begins the pattern
	}
	return target
}

func (g gridNotes) patch(token notation.StepToken, text string) (Span, bool, error) {
	if text == token.Text {
		return Span{}, false, nil
	}
	at, err := Offset(g.source, g.path, token.Position)
	if err != nil {
		return Span{}, false, err
	}
	if at+len(token.Text) > len(g.source) || string(g.source[at:at+len(token.Text)]) != token.Text {
		return Span{}, false, fmt.Errorf("step no longer matches its source")
	}
	return Span{at, at + len(token.Text), text, g.path}, true, nil
}

// --- drum lanes ---

func drumLevel(velocity int) string {
	base := "x"
	best := intAbs(capturedVelocity("x") - velocity)
	for level := 1; level <= 9; level++ {
		if distance := intAbs(level*14 - velocity); distance < best {
			base, best = "x"+strconv.Itoa(level), distance
		}
	}
	if intAbs(127-velocity) < best {
		base = "X"
	}
	return base
}

func drumSuffix(text string) string {
	if len(text) < 2 {
		return ""
	}
	if at := strings.IndexAny(text[1:], "*?%"); at >= 0 {
		return text[1+at:]
	}
	return ""
}

func (g gridNotes) drums() ([]Span, int, error) {
	in := g.in
	lanes := g.authored.Lanes
	count := 0
	if len(lanes) > 0 {
		count = len(lanes[0].Hits)
	}
	laneIndex := map[string]int{}
	for i, lane := range lanes {
		laneIndex[lane.Name] = i
		if len(lane.Hits) != count || len(g.compiled.Lanes[lane.Name]) != count {
			return nil, 0, fmt.Errorf("drum lane %s no longer matches the pattern", lane.Name)
		}
	}
	type cell struct{ lane, step int }
	hit := func(c cell) *Step { return g.compiled.Lanes[lanes[c.lane].Name][c.step] }
	// after holds each occupied cell's text; moving records cells taken by the batch.
	after := map[cell]string{}
	for l, lane := range lanes {
		for i, token := range lane.Hits {
			if hit(cell{l, i}) != nil {
				after[cell{l, i}] = token.Text
			}
		}
	}
	var selected []cell
	seen := map[cell]bool{}
	for _, ref := range in.Notes {
		l, ok := laneIndex[ref.Lane]
		changed := func(why string, args ...any) error {
			return g.refAt("note-changed", ref, "a selected hit changed ("+why+"); select the notes again", args...)
		}
		switch {
		case !ok:
			return nil, 0, changed("the pattern has no lane %q", ref.Lane)
		case ref.At%g.step != 0:
			return nil, 0, changed("it does not start on a %d-tick step", g.step)
		case ref.Length != g.step:
			return nil, 0, changed("a hit lasts one %d-tick step, not %d ticks", g.step, ref.Length)
		}
		c := cell{l, int(ref.At / g.step)}
		switch {
		case c.step >= count || hit(c) == nil:
			return nil, 0, changed("no hit is there")
		case seen[c]:
			return nil, 0, changed("the batch names it twice")
		case int(hit(c).Velocity) != ref.Velocity:
			return nil, 0, changed("its velocity is %d, not %d", hit(c).Velocity, ref.Velocity)
		}
		seen[c] = true
		selected = append(selected, c)
	}
	if len(in.Notes) == 0 && in.Op == "quantize" {
		for l := range lanes {
			for i := 0; i < count; i++ {
				if hit(cell{l, i}) != nil {
					selected = append(selected, cell{l, i})
				}
			}
		}
	}
	changed := 0
	place := func(moves map[cell]cell) error {
		text := map[cell]string{}
		for from := range moves {
			text[from] = after[from]
			if !in.Copy {
				delete(after, from)
			}
		}
		// A selected hit that stays is not replaced by another selected hit.
		taken := map[cell]bool{}
		for _, c := range selected {
			if _, moved := moves[c]; !moved || in.Copy {
				taken[c] = true
			}
		}
		order := make([]cell, 0, len(moves))
		for from := range moves {
			order = append(order, from)
		}
		sort.Slice(order, func(a, b int) bool {
			if order[a].lane != order[b].lane {
				return order[a].lane < order[b].lane
			}
			return order[a].step < order[b].step
		})
		for _, from := range order {
			to := moves[from]
			if taken[to] {
				return g.hitAt("same-cell", from.step, lanes[from.lane].Name, "%s would put it on %s step %d, where another selected hit lands or stays; choose a finer grid or fewer hits", opName(in), lanes[to.lane].Name, to.step+1)
			}
			taken[to] = true
			after[to] = text[from] // a moved hit replaces an unselected hit in its cell
		}
		return nil
	}
	switch in.Op {
	case "move":
		dt, err := g.whole(in.DT, "a move")
		if err != nil {
			return nil, 0, err
		}
		moves := map[cell]cell{}
		for _, from := range selected {
			to := cell{from.lane + in.DP, from.step + dt}
			if to.lane < 0 || to.lane >= len(lanes) {
				return nil, 0, g.hitAt("outside-lanes", from.step, lanes[from.lane].Name, "%s by %+d lanes would leave the pattern's %d lanes", opName(in), in.DP, len(lanes))
			}
			if to.step < 0 || to.step >= count {
				return nil, 0, g.hitAt("outside-pattern", from.step, lanes[from.lane].Name, "%s would put it on step %d, outside the pattern's %d steps", opName(in), to.step+1, count)
			}
			if to != from {
				moves[from] = to
			}
		}
		if err := place(moves); err != nil {
			return nil, 0, err
		}
		changed = len(moves)
	case "quantize":
		size, err := g.quantizeSteps()
		if err != nil || size == 0 {
			return nil, 0, err
		}
		moves := map[cell]cell{}
		for _, from := range selected {
			to := cell{from.lane, g.quantized(from.step, size, count)}
			if to != from {
				moves[from] = to
			}
		}
		if err := place(moves); err != nil {
			return nil, 0, err
		}
		changed = len(moves)
	case "resize":
		first := selected[0]
		return nil, 0, g.hitAt("fixed-length", first.step, lanes[first.lane].Name, "drum hits have a fixed length of one step; change ratchet in the inspector instead")
	case "velocity":
		for _, c := range selected {
			text := after[c]
			next := drumLevel(in.newVelocity(int(hit(c).Velocity))) + drumSuffix(text)
			if next != text {
				after[c] = next
				changed++
			}
		}
	case "delete":
		for _, c := range selected {
			delete(after, c)
		}
		changed = len(selected)
	case "insert":
		for _, ref := range in.Insert {
			l, ok := laneIndex[ref.Lane]
			if !ok {
				return nil, 0, g.refAt("unknown-lane", ref, "pattern %s has no drum lane %q", g.compiled.ID, ref.Lane)
			}
			if ref.At%g.step != 0 {
				return nil, 0, g.refAt("not-whole-steps", ref, "insert needs a start on a %d-tick step", g.step)
			}
			at := int(ref.At / g.step)
			if ref.Length != g.step {
				return nil, 0, g.refAt("fixed-length", ref, "a drum hit lasts one %d-tick step, not %d ticks", g.step, ref.Length)
			}
			if at >= count {
				return nil, 0, g.refAt("outside-pattern", ref, "insert would put it on step %d, outside the pattern's %d steps", at+1, count)
			}
			after[cell{l, at}] = drumLevel(ref.Velocity)
		}
		changed = len(in.Insert)
	}
	var spans []Span
	for l, lane := range lanes {
		for i, token := range lane.Hits {
			want, ok := after[cell{l, i}]
			if !ok {
				want = "."
				if hit(cell{l, i}) == nil {
					want = token.Text
				}
			}
			span, write, err := g.patch(token, want)
			if err != nil {
				return nil, 0, err
			}
			if write {
				spans = append(spans, span)
			}
		}
	}
	return spans, changed, nil
}

// --- step patterns ---

// stepSource is one onset of the pattern before the edit: its step, its hold
// in steps (the onset and its ties), its pitches in authored order and the
// token that spells it.
type stepSource struct {
	start, length int
	pitches       []int
	token         notation.StepToken
	step          *Step
	velocity      int
}

// stepVoice is a pitch with an optional source spelling to reuse.
type stepVoice struct {
	pitch    int
	spelling string
}

// stepOnset is one onset after the edit. base supplies the spelling and
// modifiers of the voices it keeps; added voices are appended. shift moves
// every voice of an otherwise whole base token.
type stepOnset struct {
	start, length int
	velocity      int
	base          *stepSource
	keep          []int // retained base voices, by index into base.pitches
	shift         int
	add           []stepVoice
	suffix        string      // modifiers when there is no base token
	stationary    bool        // an onset the batch did not move
	pinned        bool        // a stationary onset with selected voices: never replaced
	origin        *stepSource // the onset this one came from, if any
}

func (o *stepOnset) pitches() []int {
	var out []int
	for _, i := range o.keep {
		out = append(out, o.base.pitches[i]+o.shift)
	}
	for _, voice := range o.add {
		out = append(out, voice.pitch)
	}
	return out
}

// hasArticulation reports slide or ratchet, which a chord cannot hold.
func (o *stepOnset) hasArticulation() bool {
	if o.base != nil && o.base.step != nil && (o.base.step.Slide || o.base.step.Ratchet > 1) {
		return true
	}
	return strings.ContainsAny(o.suffix, "~*")
}

func (o *stepOnset) whole() bool {
	return o.base != nil && len(o.keep) == len(o.base.pitches) && len(o.add) == 0
}

// voices returns the onset's voices with the source spelling of every pitch
// that kept its value.
func (o *stepOnset) voices() []stepVoice {
	var out []stepVoice
	for _, i := range o.keep {
		voice := stepVoice{pitch: o.base.pitches[i] + o.shift}
		if o.shift == 0 {
			voice.spelling = o.base.spelling(i)
		}
		out = append(out, voice)
	}
	return append(out, o.add...)
}

// spelling is the authored spelling of voice i, without modifiers.
func (s *stepSource) spelling(i int) string {
	if s.token.Transpose != 0 {
		return ""
	}
	if len(s.token.ChordPitches) > 0 {
		if i < len(s.token.ChordPitches) {
			return s.token.ChordPitches[i].Text
		}
		return ""
	}
	return strings.TrimSuffix(s.token.Text, noteSuffix(s.token.Text))
}

func (s *stepSource) suffix() string {
	if len(s.token.ChordPitches) > 0 {
		return s.token.ChordModifiers
	}
	return noteSuffix(s.token.Text)
}

func voiceText(voice stepVoice) string {
	if voice.spelling != "" {
		return voice.spelling
	}
	return sourcePitch(voice.pitch)
}

// text writes the onset's token, keeping the authored text, its spellings,
// comments and modifiers wherever the onset keeps them.
func (o *stepOnset) text() (string, error) {
	if o.base == nil {
		return spelledOnset(o.add, o.suffix, nil, ""), nil
	}
	token := o.base.token
	chord := len(token.ChordPitches) > 0
	if o.whole() {
		if o.shift == 0 {
			return token.Text, nil
		}
		if !chord {
			return sourcePitch(o.base.pitches[0]+o.shift) + noteSuffix(token.Text), nil
		}
		shifted := make([]int, len(o.base.pitches))
		for i, pitch := range o.base.pitches {
			shifted[i] = pitch + o.shift
		}
		return chordSource(token, shifted, token.ChordModifiers, nil)
	}
	if !chord || o.shift != 0 {
		// A single note gaining voices, or a transposed part of a chord: spell
		// the voices again with the base modifiers and comments.
		return spelledOnset(o.voices(), o.base.suffix(), token.ChordComments, token.Text), nil
	}
	// Remove the voices that left, keeping the spacing and comments of the rest.
	kept := map[int]bool{}
	for _, i := range o.keep {
		kept[i] = true
	}
	var spans []Span
	pitches := token.ChordPitches
	for i := range pitches {
		if kept[i] {
			continue
		}
		start, end := pitches[i].Start, pitches[i].End
		if i+1 < len(pitches) {
			if strings.TrimSpace(token.Text[end:pitches[i+1].Start]) == "" {
				end = pitches[i+1].Start
			}
		} else if i > 0 && strings.TrimSpace(token.Text[pitches[i-1].End:start]) == "" {
			start = pitches[i-1].End
		}
		spans = append(spans, Span{start, end, "", token.Position.File})
	}
	total := len(o.keep) + len(o.add)
	if total == 1 {
		return spelledOnset(o.voices(), token.ChordModifiers, token.ChordComments, token.Text), nil
	}
	if len(o.add) > 0 {
		var names []string
		for _, voice := range o.add {
			names = append(names, voiceText(voice))
		}
		last := pitches[len(pitches)-1].End
		spans = append(spans, Span{last, last, " " + strings.Join(names, " "), token.Position.File})
	}
	// Spans removing the final voice and appending after it share an edge;
	// apply them in two passes so each pass stays disjoint.
	var removals, additions []Span
	for _, span := range spans {
		if span.Start == span.End {
			additions = append(additions, span)
		} else {
			removals = append(removals, span)
		}
	}
	text := token.Text
	if len(additions) > 0 {
		updated, err := PatchSpans([]byte(text), token.Position.File, additions)
		if err != nil {
			return "", err
		}
		text = string(updated)
	}
	updated, err := PatchSpans([]byte(text), token.Position.File, removals)
	return string(updated), err
}

// spelledOnset writes voices as one note or a chord. Comments kept from a
// chord go before it, on lines ending like the chord's own text.
func spelledOnset(voices []stepVoice, suffix string, comments []string, text string) string {
	prefix := ""
	if len(comments) > 0 {
		newline := "\n"
		if strings.Contains(text, "\r\n") {
			newline = "\r\n"
		}
		// A comment's text keeps a CRLF file's carriage return; join the
		// comments with the file's own line ending instead.
		lines := make([]string, len(comments))
		for i, comment := range comments {
			lines[i] = strings.TrimRight(comment, "\r")
		}
		prefix = strings.Join(lines, newline) + newline
	}
	if len(voices) == 1 {
		return prefix + voiceText(voices[0]) + suffix
	}
	names := make([]string, len(voices))
	for i, voice := range voices {
		names[i] = voiceText(voice)
	}
	return prefix + "[" + strings.Join(names, " ") + "]" + suffix
}

func (g gridNotes) stepPattern() ([]Span, int, error) {
	in := g.in
	tokens := g.authored.Steps
	data := g.compiled.Data
	count := len(tokens)
	acid := g.authored.Kind == "acid"
	if len(data) != count {
		return nil, 0, fmt.Errorf("note editing cannot change a pattern with tuplet groups yet; edit it in Code")
	}
	if acid {
		for _, token := range tokens {
			if len(token.ChordPitches) > 0 {
				return nil, 0, fmt.Errorf("note editing cannot change a pattern with tuplet groups yet; edit it in Code")
			}
		}
	}
	transpose := int(g.compiled.Transpose)
	// Onsets before the edit, and which onset owns each step.
	var sources []*stepSource
	owner := make([]int, count)
	for i := range owner {
		owner[i] = -1
	}
	for i := 0; i < count; i++ {
		step := data[i]
		if step == nil || step.Tie {
			continue
		}
		source := &stepSource{start: i, length: 1, token: tokens[i], step: step, velocity: int(step.Velocity)}
		if len(step.Notes) > 0 {
			source.pitches = append([]int(nil), step.Notes...)
		} else {
			source.pitches = []int{int(step.Note)}
		}
		for i+source.length < count && data[i+source.length] != nil && data[i+source.length].Tie {
			source.length++
		}
		for k := i; k < i+source.length; k++ {
			owner[k] = len(sources)
		}
		sources = append(sources, source)
	}
	// Resolve the named notes to voices.
	type voiceKey struct{ source, voice int }
	selected := map[voiceKey]bool{}
	for _, ref := range in.Notes {
		changed := func(why string, args ...any) error {
			return g.refAt("note-changed", ref, "a selected note changed ("+why+"); select the notes again", args...)
		}
		if ref.Lane != "" {
			return nil, 0, g.refAt("no-lanes", ref, "a %s pattern has no drum lanes", g.authored.Kind)
		}
		if ref.At%g.step != 0 {
			return nil, 0, changed("it does not start on a %d-tick step", g.step)
		}
		start := int(ref.At / g.step)
		if start >= count || owner[start] < 0 {
			return nil, 0, changed("no note starts on that step")
		}
		if sources[owner[start]].start != start {
			return nil, 0, changed("that step holds the note from step %d; a hold cannot be selected alone", sources[owner[start]].start+1)
		}
		source := sources[owner[start]]
		voice := -1
		for v, pitch := range source.pitches {
			if pitch == ref.Pitch {
				voice = v
			}
		}
		key := voiceKey{owner[start], voice}
		switch {
		case voice < 0:
			return nil, 0, changed("no note of this pitch starts on that step")
		case selected[key]:
			return nil, 0, changed("the batch names it twice")
		case int64(source.length)*g.step != ref.Length:
			return nil, 0, changed("it lasts %d ticks, not %d", int64(source.length)*g.step, ref.Length)
		case source.velocity != ref.Velocity:
			return nil, 0, changed("its velocity is %d, not %d", source.velocity, ref.Velocity)
		}
		selected[key] = true
	}
	if len(in.Notes) == 0 && in.Op == "quantize" {
		for s, source := range sources {
			for v := range source.pitches {
				selected[voiceKey{s, v}] = true
			}
		}
	}
	all := func(s int) []int {
		out := make([]int, len(sources[s].pitches))
		for i := range out {
			out[i] = i
		}
		return out
	}
	stationary := func(s int, keep []int) *stepOnset {
		source := sources[s]
		return &stepOnset{start: source.start, length: source.length, velocity: source.velocity, base: source, keep: keep, stationary: true, origin: source}
	}
	var stay, moving []*stepOnset
	changed := 0
	// moveSource splits one onset into what stays and what lands at target.
	moveSource := func(s, target, dp int) {
		source := sources[s]
		var picked, rest []int
		for v := range source.pitches {
			if selected[voiceKey{s, v}] {
				picked = append(picked, v)
			} else {
				rest = append(rest, v)
			}
		}
		if len(picked) == 0 || target == source.start && dp == 0 {
			onset := stationary(s, all(s))
			onset.pinned = len(picked) > 0
			stay = append(stay, onset)
			return
		}
		changed += len(picked)
		if in.Copy {
			rest = all(s)
		}
		if len(rest) > 0 {
			stay = append(stay, stationary(s, rest))
		}
		if len(picked) == len(source.pitches) {
			moving = append(moving, &stepOnset{start: target, length: source.length, velocity: source.velocity, base: source, keep: picked, shift: dp, origin: source})
			return
		}
		// Part of a chord moves: it carries the chord's accent, chance and velocity.
		onset := &stepOnset{start: target, length: source.length, velocity: source.velocity, suffix: source.token.ChordModifiers, origin: source}
		for _, v := range picked {
			voice := stepVoice{pitch: source.pitches[v] + dp}
			if dp == 0 {
				voice.spelling = source.spelling(v)
			}
			onset.add = append(onset.add, voice)
		}
		moving = append(moving, onset)
	}
	touched := func(s int) bool {
		for v := range sources[s].pitches {
			if selected[voiceKey{s, v}] {
				return true
			}
		}
		return false
	}
	switch in.Op {
	case "move":
		dt, err := g.whole(in.DT, "a move")
		if err != nil {
			return nil, 0, err
		}
		for s, source := range sources {
			moveSource(s, source.start+dt, in.DP)
		}
	case "quantize":
		size, err := g.quantizeSteps()
		if err != nil || size == 0 {
			return nil, 0, err
		}
		for s, source := range sources {
			moveSource(s, g.quantized(source.start, size, count), 0)
		}
	case "resize":
		dl, err := g.whole(in.DL, "a length change")
		if err != nil {
			return nil, 0, err
		}
		for s, source := range sources {
			onset := stationary(s, all(s))
			// A chord shares its gate: resizing one voice resizes the chord.
			if touched(s) && dl != 0 {
				onset.length += dl
				if onset.length < 1 {
					return nil, 0, g.noteAt("too-short", source.start, source.pitches[0], "resize would leave it %s long; a note lasts at least one step", stepsText(onset.length))
				}
				if source.start+onset.length > count {
					return nil, 0, g.noteAt("outside-pattern", source.start, source.pitches[0], "resize to %s would hold it past the pattern end at step %d", stepsText(onset.length), count)
				}
				changed += len(source.pitches)
			}
			stay = append(stay, onset)
		}
	case "velocity":
		for s, source := range sources {
			onset := stationary(s, all(s))
			// Chord voices share one velocity in the velocity row.
			if touched(s) {
				onset.velocity = in.newVelocity(source.velocity)
				if onset.velocity != source.velocity {
					changed += len(source.pitches)
				}
			}
			stay = append(stay, onset)
		}
	case "delete":
		for s, source := range sources {
			var rest []int
			for v := range source.pitches {
				if selected[voiceKey{s, v}] {
					changed++
				} else {
					rest = append(rest, v)
				}
			}
			if len(rest) > 0 {
				stay = append(stay, stationary(s, rest))
			}
		}
	case "insert":
		for s := range sources {
			stay = append(stay, stationary(s, all(s)))
		}
		for _, ref := range in.Insert {
			if ref.Lane != "" {
				return nil, 0, g.refAt("no-lanes", ref, "a %s pattern has no drum lanes", g.authored.Kind)
			}
			if ref.At%g.step != 0 || ref.Length%g.step != 0 {
				return nil, 0, g.refAt("not-whole-steps", ref, "insert needs a start and a length of whole %d-tick steps (got %d ticks long); quantize a performance before pasting it into a step pattern", g.step, ref.Length)
			}
			moving = append(moving, &stepOnset{start: int(ref.At / g.step), length: int(ref.Length / g.step), velocity: ref.Velocity, add: []stepVoice{{pitch: ref.Pitch}}})
		}
		changed = len(in.Insert)
	}
	if changed == 0 {
		return nil, 0, nil
	}
	// Place the onsets: stationary ones first, then the moved ones in source order.
	after := map[int]*stepOnset{}
	for _, onset := range stay {
		after[onset.start] = onset
	}
	for _, onset := range moving {
		// Refusals name the note where the request found it.
		from, first := g.origin(onset)
		if onset.start < 0 || onset.start+onset.length > count {
			where := "past the pattern end"
			if onset.start < 0 {
				where = "before step 1"
			}
			return nil, 0, g.noteAt("outside-pattern", from, first, "%s would put it on step %d, %s long, %s (the pattern has %d steps)", opName(in), onset.start+1, stepsText(onset.length), where, count)
		}
		for _, pitch := range onset.pitches() {
			if pitch < 0 || pitch > 127 || pitch+transpose < 0 || pitch+transpose > 127 {
				return nil, 0, g.noteAt("pitch-range", from, first, "%s would make it MIDI %d, which with the pattern transpose of %+d leaves 0–127", opName(in), pitch, transpose)
			}
		}
		existing := after[onset.start]
		if existing == nil {
			after[onset.start] = onset
			continue
		}
		merged, err := g.mergeOnsets(existing, onset, acid)
		if err != nil {
			return nil, 0, err
		}
		after[onset.start] = merged
	}
	starts := make([]int, 0, len(after))
	for start := range after {
		starts = append(starts, start)
	}
	sort.Ints(starts)
	for i, start := range starts {
		onset := after[start]
		pitches := onset.pitches()
		if len(pitches) > 1 {
			if acid {
				return nil, 0, g.noteAt("acid-one-pitch", start, pitches[1], "%s would put %d pitches on this step; an acid pattern plays one pitch per step", opName(in), len(pitches))
			}
			if len(pitches) > 4 {
				return nil, 0, g.noteAt("chord-size", start, pitches[4], "%s would make it a fifth pitch of the chord on this step; a chord holds at most 4 pitches", opName(in))
			}
			if onset.hasArticulation() {
				return nil, 0, g.noteAt("chord-articulation", start, pitches[0], "%s would make a chord of a note that slides or ratchets; a chord cannot slide or ratchet", opName(in))
			}
		}
		if i+1 < len(starts) && start+onset.length > starts[i+1] {
			next := after[starts[i+1]]
			const hint = "a new note ends the one before it, so move or shorten one first"
			if !next.stationary {
				from, first := g.origin(next)
				return nil, 0, g.noteAt("overlap", from, first, "%s would put it on step %d, inside the %s on step %d, which holds through step %d; %s", opName(in), next.start+1, spelledPitches(pitches), start+1, start+onset.length, hint)
			}
			from, first := g.origin(onset)
			return nil, 0, g.noteAt("overlap", from, first, "%s would hold it through step %d, over the %s on step %d; %s", opName(in), start+onset.length, spelledPitches(next.pitches()), next.start+1, hint)
		}
	}
	// Write each step whose content changed, and the velocity row.
	var spans []Span
	covered := make([]int, count)
	for i := range covered {
		covered[i] = -1
	}
	for _, start := range starts {
		for k := start; k < start+after[start].length; k++ {
			covered[k] = start
		}
	}
	// A hold after a rest is silent: it extends nothing (kernel/seq extends a
	// gate only through holds that follow it). silent marks those holds before
	// the edit, wrapping from the last step to the first as the loop does; a
	// hold after the last step is not assumed silent.
	silent := make([]bool, count)
	for i := range silent {
		previous := i - 1
		if i == 0 {
			previous = count - 1
		}
		silent[i] = data[i] != nil && data[i].Tie && owner[i] < 0 && (data[previous] == nil || previous < i && silent[previous])
	}
	for i, token := range tokens {
		var want string
		previous := i - 1
		if i == 0 {
			previous = count - 1
		}
		switch {
		case covered[i] == i:
			text, err := after[i].text()
			if err != nil {
				return nil, 0, err
			}
			want = text
		case covered[i] >= 0:
			want = "-"
		case owner[i] >= 0:
			want = "."
		case silent[i] && covered[previous] >= 0:
			// A placed note would absorb this silent hold and grow longer:
			// write a rest so the note keeps its length and the hold stays silent.
			want = "."
		default:
			want = token.Text // a rest, or a hold that follows a rest
		}
		span, write, err := g.patch(token, want)
		if err != nil {
			return nil, 0, err
		}
		if write {
			spans = append(spans, span)
		}
	}
	velocity := make([]int, count) // 0 where any value will do
	for _, start := range starts {
		velocity[start] = after[start].velocity
	}
	rowSpans, err := g.velocityRow(velocity)
	if err != nil {
		return nil, 0, err
	}
	return append(spans, rowSpans...), changed, nil
}

// stepModifiers are what the voices of one step share: accent, slide,
// ratchet, chance and the velocity row's value.
type stepModifiers struct {
	accent, slide             bool
	ratchet, chance, velocity int
}

func (o *stepOnset) modifiers() stepModifiers {
	m := stepModifiers{ratchet: 1, chance: 100, velocity: o.velocity}
	source := o.origin
	if o.base != nil {
		source = o.base
	}
	if source != nil && source.step != nil {
		m.accent, m.slide = source.step.Accent, source.step.Slide
		m.ratchet, m.chance = max(1, int(source.step.Ratchet)), int(source.step.Probability)
		if m.chance == 0 {
			m.chance = 100
		}
	}
	return m
}

func spelledPitches(pitches []int) string {
	names := make([]string, len(pitches))
	for i, pitch := range pitches {
		names[i] = sourcePitch(pitch)
	}
	return strings.Join(names, " and ")
}

// mergeOnsets lands a moved onset on a step that already has one. A moved note
// replaces an unselected note of the same pitch. Otherwise onsets of the same
// length and the same accent, slide, ratchet, chance and velocity merge into
// one chord. Anything else is refused and names the note that would change.
func (g gridNotes) mergeOnsets(existing, moved *stepOnset, acid bool) (*stepOnset, error) {
	in := g.in
	from, first := g.origin(moved)
	landing := moved.start + 1
	movedPitches := map[int]bool{}
	for _, pitch := range moved.pitches() {
		movedPitches[pitch] = true
	}
	// A moved note replaces a stationary note of the same pitch, unless both
	// come from one chord: its pitches must stay distinct.
	collide := func(pitch int) error {
		if !existing.stationary || existing.pinned {
			return g.noteAt("same-pitch", from, first, "%s would put it on step %d as %s, where another selected %s lands or stays; choose a finer grid or move one first", opName(in), landing, sourcePitch(pitch), sourcePitch(pitch))
		}
		if existing.origin != nil && existing.origin == moved.origin {
			return g.noteAt("chord-pitches-equal", from, first, "%s would make it %s, equal to another pitch of its chord on step %d", opName(in), sourcePitch(pitch), landing)
		}
		return nil
	}
	var keep []int
	for _, i := range existing.keep {
		if pitch := existing.base.pitches[i] + existing.shift; !movedPitches[pitch] {
			keep = append(keep, i)
		} else if err := collide(pitch); err != nil {
			return nil, err
		}
	}
	var add []stepVoice
	for _, voice := range existing.add {
		if !movedPitches[voice.pitch] {
			add = append(add, voice)
		} else if err := collide(voice.pitch); err != nil {
			return nil, err
		}
	}
	if len(keep)+len(add) == 0 {
		return moved, nil
	}
	name, joining := spelledPitches(moved.pitches()), spelledPitches(existing.pitches())
	subject := "it"
	if len(moved.pitches()) > 1 {
		subject = name
	}
	if existing.length != moved.length {
		return nil, g.noteAt("chord-length", from, first, "%s would start %s, %s long, on step %d with %s, %s long; notes that start together share one length", opName(in), subject, stepsText(moved.length), landing, joining, stepsText(existing.length))
	}
	if acid {
		return nil, g.noteAt("acid-one-pitch", from, first, "%s would put %s on step %d with %s; an acid pattern plays one pitch per step", opName(in), subject, landing, joining)
	}
	if total := len(keep) + len(add) + len(moved.pitches()); total > 4 {
		return nil, g.noteAt("chord-size", from, first, "%s would add %s to the chord of %s on step %d, making %d pitches; a chord holds at most 4", opName(in), subject, joining, landing, total)
	}
	if moved.hasArticulation() || existing.hasArticulation() {
		return nil, g.noteAt("chord-articulation", from, first, "%s would make a chord of %s and %s on step %d, and one of them slides or ratchets; a chord cannot slide or ratchet", opName(in), name, joining, landing)
	}
	// The chord keeps one accent, chance and velocity: refuse to drop or impose
	// the moved note's own.
	mine, theirs := moved.modifiers(), existing.modifiers()
	var changes []string
	if mine.chance != theirs.chance {
		if mine.chance < 100 {
			changes = append(changes, fmt.Sprintf("lose its %d%% chance", mine.chance))
		} else {
			changes = append(changes, fmt.Sprintf("take a %d%% chance from %s", theirs.chance, joining))
		}
	}
	if mine.accent != theirs.accent {
		if mine.accent {
			changes = append(changes, "lose its accent")
		} else {
			changes = append(changes, "take an accent from "+joining)
		}
	}
	if mine.velocity != theirs.velocity {
		changes = append(changes, fmt.Sprintf("play at velocity %d instead of %d", theirs.velocity, mine.velocity))
	}
	if len(changes) > 0 {
		return nil, g.noteAt("chord-modifiers", from, first, "after %s, %s would join %s in a chord on step %d and %s; notes that start together share accent, chance and velocity, so make them match first", opName(in), subject, joining, landing, strings.Join(changes, " and "))
	}
	merged := *existing
	merged.keep, merged.add = keep, add
	if merged.base != nil && merged.shift != 0 {
		// Spell a transposed base again: its voices become added voices.
		merged.add = merged.voices()
		merged.suffix, merged.base, merged.keep, merged.shift = existing.base.suffix(), nil, nil, 0
	}
	merged.add = append(append([]stepVoice(nil), merged.add...), moved.voices()...)
	return &merged, nil
}

// velocityRow patches the velocity row so every onset step plays its value,
// keeping each token whose meaning holds, or adds a row when one is needed.
// want is 0 for steps without an onset.
func (g gridNotes) velocityRow(want []int) ([]Span, error) {
	row := g.authored.Velocity
	if row == nil {
		needed := false
		for _, value := range want {
			needed = needed || value != 0 && value != 100
		}
		if !needed {
			return nil, nil
		}
		return g.addVelocityRow(want)
	}
	if len(row) != len(want) {
		return nil, fmt.Errorf("the velocity row no longer matches the pattern length")
	}
	var spans []Span
	held := 0
	for i, token := range row {
		value := 0
		if token.Text != "." {
			value, _ = strconv.Atoi(token.Text)
		}
		text := token.Text
		switch {
		case want[i] == 0:
			if value != 0 {
				held = value
			}
		case token.Text == "." && held == want[i], value == want[i]:
			held = want[i]
		default:
			text = strconv.Itoa(want[i])
			held = want[i]
		}
		span, write, err := g.patch(token, text)
		if err != nil {
			return nil, err
		}
		if write {
			spans = append(spans, span)
		}
	}
	return spans, nil
}

// addVelocityRow writes "velocity:" before the first expression row, or before
// the pattern's closing brace: on its own line when the brace is on its own
// line, otherwise inline.
func (g gridNotes) addVelocityRow(want []int) ([]Span, error) {
	values := make([]string, len(want))
	held := 0
	for i, value := range want {
		switch {
		case i == 0 && value == 0:
			values[i], held = "100", 100
		case value == 0 || value == held:
			values[i] = "."
		default:
			values[i], held = strconv.Itoa(value), value
		}
	}
	row := "velocity: " + strings.Join(values, " ")
	node, _, err := declaration(g.source, patternTypes, g.compiled.ID)
	if err != nil {
		return nil, err
	}
	at := int(node.EndByte()) - 1
	if len(g.authored.Expression) > 0 {
		if at, err = Offset(g.source, g.path, g.authored.Expression[0].Position); err != nil {
			return nil, err
		}
	} else if at < 0 || g.source[at] != '}' {
		return nil, fmt.Errorf("pattern %s source no longer matches", g.compiled.ID)
	}
	lineStart := at
	for lineStart > 0 && g.source[lineStart-1] != '\n' {
		lineStart--
	}
	if strings.TrimSpace(string(g.source[lineStart:at])) == "" {
		// The brace or row starts its own line: add the row on a line before it.
		indent := "  "
		if len(g.authored.Steps) > 0 {
			if first, err := Offset(g.source, g.path, g.authored.Steps[0].Position); err == nil {
				start := first
				for start > 0 && g.source[start-1] != '\n' {
					start--
				}
				if prefix := string(g.source[start:first]); strings.TrimSpace(prefix) == "" {
					indent = prefix
				}
			}
		}
		if len(g.authored.Expression) > 0 {
			return []Span{{at, at, row + Newline(g.source) + string(g.source[lineStart:at]), g.path}}, nil
		}
		return []Span{{lineStart, lineStart, indent + row + Newline(g.source), g.path}}, nil
	}
	if at > 0 && g.source[at-1] != ' ' && g.source[at-1] != '\t' {
		row = " " + row
	}
	return []Span{{at, at, row + " ", g.path}}, nil
}
