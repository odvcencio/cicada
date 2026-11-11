package edit

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	gts "github.com/odvcencio/gotreesitter"

	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/kernel/limits"
	"m31labs.dev/cicada/notation"
)

// AddTrack adds a track after the last track. Voice names what the track
// plays: a built-in voice (acid, drums, piano, a modal or keyboard voice) or
// an instrument, kit, sampler or preset the score declares or imports.
// Preset instead declares a patch from the instrument library as a new
// instrument, named instrument or after the track, as addpreset does. The
// new track plays nothing until a scene binds a pattern to it (bindscene) or
// a placement puts one on it. A voice the score cannot play is refused with
// voice_invalid, and uses lists the problems. The catalog's reasons list
// names every refusal code.
//
// Example: {"kind":"addtrack","name":"pad","voice":"piano"}
// Example: {"kind":"addtrack","name":"pad","preset":"warm-pad","instrument":"warm"}
type AddTrack struct {
	Name       string `json:"name" cicada:"required"` // the new track's name
	Voice      string `json:"voice,omitempty"`        // a built-in voice, or a declared or imported instrument, kit, sampler or preset
	Preset     string `json:"preset,omitempty"`       // a patch from the instrument library, such as warm-pad
	Instrument string `json:"instrument,omitempty"`   // the name of the instrument a preset declares; default the track's name
}

func (AddTrack) Kind() string { return "addtrack" }

// DeleteTrack removes a track: its declaration, the comment lines above it,
// and its bindings in scenes. Its patterns stay. Refused with reason
// track_in_use while anything else names the track: automation, a scene
// setting, a modulation route, an envelope source, a MIDI mapping, a live
// macro, stinger or layer, an arrangement placement or a compressor
// sidechain. The refusal lists them in uses. Deleting the last track is
// refused with result_invalid. A track another file of the project declares
// is refused with declared_in_other_file and that file: the edit changes only
// the entry. The catalog's reasons list names every refusal code.
//
// Example: {"kind":"deletetrack","track":"keys"}
type DeleteTrack struct {
	Track string `json:"track" cicada:"required"` // the track's name
}

func (DeleteTrack) Kind() string { return "deletetrack" }

// SetTrackVoice changes what a track plays to another voice: a built-in
// voice, or an instrument, kit, sampler or preset the score declares or
// imports. The track keeps its name, mixer settings and scene bindings. A
// setting of the old voice that the new one does not have is refused with
// reason voice_params_unsupported, listing the settings in uses; with
// dropparams those settings are removed and the label names them. A pattern
// of a kind the new voice cannot play, or another reference the new voice
// cannot take, is refused with reason voice_change_invalid, listing the
// problems in uses. The catalog's reasons list names every refusal code.
//
// Example: {"kind":"settrackvoice","track":"keys","voice":"glow"}
// Example: {"kind":"settrackvoice","track":"lead","voice":"acid","dropparams":true}
type SetTrackVoice struct {
	Track      string `json:"track" cicada:"required"` // the track's name
	Voice      string `json:"voice" cicada:"required"` // the new voice
	DropParams bool   `json:"dropparams,omitempty"`    // remove the settings the new voice does not have
}

func (SetTrackVoice) Kind() string { return "settrackvoice" }

// AddScene adds an empty scene after the last scene. Bind patterns to it with
// bindscene and play it with appendsongentry. In the song, a track a scene
// does not bind keeps playing what the scene before it selected. The
// catalog's reasons list names every refusal code.
//
// Example: {"kind":"addscene","name":"drop"}
type AddScene struct {
	Name string `json:"name" cicada:"required"` // the new scene's name
}

func (AddScene) Kind() string { return "addscene" }

// DuplicateScene copies a scene, with its bindings, settings and the comments
// inside it, under a new name right after the original. The catalog's
// reasons list names every refusal code.
//
// Example: {"kind":"duplicatescene","scene":"main","name":"main-2"}
type DuplicateScene struct {
	Scene string `json:"scene" cicada:"required"` // the scene to copy
	Name  string `json:"name" cicada:"required"`  // the copy's name
}

func (DuplicateScene) Kind() string { return "duplicatescene" }

// DeleteScene removes a scene and the comment lines above it. Refused with
// reason scene_in_use while the song, a live state or a MIDI launch mapping
// names it; uses lists them. With cascade the scene's song entries are
// removed with it; live states and MIDI mappings still refuse, and so does
// a song that would be left empty (reason song_would_be_empty). The
// catalog's reasons list names every refusal code.
//
// Example: {"kind":"deletescene","scene":"outro"}
type DeleteScene struct {
	Scene   string `json:"scene" cicada:"required"` // the scene's name
	Cascade bool   `json:"cascade,omitempty"`       // also remove the song entries that play the scene
}

func (DeleteScene) Kind() string { return "deletescene" }

// DeletePattern removes a pattern and the comment lines above it. Refused with
// reason pattern_in_use while a scene binds it, or a track chain, an
// arrangement placement, a live stinger or a MIDI launch mapping names it;
// uses lists them. With cascade, each scene binding of the pattern becomes
// off (the track stops in that scene; an omitted binding would keep the
// previous pattern playing) and its placements are removed; chains, stingers
// and MIDI mappings still refuse. Deleting the last pattern is refused with
// result_invalid. The catalog's reasons list names every refusal code.
//
// Example: {"kind":"deletepattern","pattern":"fill"}
type DeletePattern struct {
	Pattern string `json:"pattern" cicada:"required"` // the pattern's name
	Cascade bool   `json:"cascade,omitempty"`         // set its scene bindings to off and remove its placements
}

func (DeletePattern) Kind() string { return "deletepattern" }

// Rename renames a track, a pattern or a scene and every reference to it:
// scene bindings and settings, the song, automation, modulation routes and
// envelope sources, MIDI mappings, live states, stingers, layers and macros,
// track chains, arrangement placements and compressor sidechains. Comments
// and all other text stay as they are. An entity another file of the project
// declares is refused with declared_in_other_file and that file; a rename
// that would leave a reference in another file dangling is refused with
// rename_incomplete, and uses lists it. The catalog's reasons list names
// every refusal code.
//
// Example: {"kind":"rename","entity":"track:bass","name":"sub"}
// Example: {"kind":"rename","entity":"pattern:riff","name":"riff-a"}
// Example: {"kind":"rename","entity":"scene:main","name":"verse"}
type Rename struct {
	Entity EntityID `json:"entity" cicada:"required"` // track:<name>, pattern:<name> or scene:<name>
	Name   string   `json:"name" cicada:"required"`   // the new name
}

func (Rename) Kind() string { return "rename" }

func init() {
	Register("addtrack", func() Intent { return &AddTrack{} })
	Handle("addtrack", addTrack)
	ruleKind("addtrack", fieldRules{oneOf: []FieldGroup{{Fields: []string{"voice", "preset"}}}})
	Register("deletetrack", func() Intent { return &DeleteTrack{} })
	Handle("deletetrack", deleteTrack)
	Register("settrackvoice", func() Intent { return &SetTrackVoice{} })
	Handle("settrackvoice", setTrackVoice)
	Register("addscene", func() Intent { return &AddScene{} })
	Handle("addscene", addScene)
	Register("duplicatescene", func() Intent { return &DuplicateScene{} })
	Handle("duplicatescene", duplicateScene)
	Register("deletescene", func() Intent { return &DeleteScene{} })
	Handle("deletescene", deleteScene)
	Register("deletepattern", func() Intent { return &DeletePattern{} })
	Handle("deletepattern", deletePattern)
	Register("rename", func() Intent { return &Rename{} })
	Handle("rename", renameEntity)
}

// validScore parses the project and refuses an invalid one, naming the
// operation the caller asked for.
func validScore(ctx *Context, operation string) (*notation.Score, error) {
	score, ds, err := ctx.ParseProject()
	if err != nil {
		return nil, err
	}
	if score == nil || hasErrors(ds) {
		return nil, refuse("score_invalid", "", "score must validate before %s", operation)
	}
	return score, nil
}

// candidateErrors parses the project with the entry source replaced and
// returns its error diagnostics. A candidate that parses clean is compiled
// too, and a compile error comes back as one diagnostic without a line.
func candidateErrors(ctx *Context, candidate []byte) ([]notation.Diagnostic, error) {
	working := *ctx
	working.Source, working.plan = candidate, nil
	_, ds, err := working.ParseProject()
	if err != nil {
		return nil, err
	}
	var errs []notation.Diagnostic
	for _, d := range ds {
		if d.Severity == "error" {
			errs = append(errs, d)
		}
	}
	if len(errs) == 0 {
		if _, err := ctx.Options.Compiler.Compile(candidate, ctx.fileOverrides()); err != nil {
			errs = append(errs, notation.Diagnostic{Severity: "error", Message: err.Error()})
		}
	}
	return errs, nil
}

// resultValid refuses a structural edit whose result would not validate, with
// reason result_invalid and the problems in uses.
func resultValid(ctx *Context, updated []byte, field, what string) error {
	errs, err := candidateErrors(ctx, updated)
	if err != nil {
		return err
	}
	if len(errs) > 0 {
		uses := diagnosticUses(errs)
		return &ReasonError{Reason: "result_invalid", Field: field, Uses: uses, Message: fmt.Sprintf("%s leaves the score invalid: %s", what, strings.Join(uses, "; "))}
	}
	return nil
}

func diagnosticUses(ds []notation.Diagnostic) []string {
	uses := make([]string, 0, len(ds))
	for _, d := range ds {
		if d.Position.Line == 0 {
			uses = append(uses, d.Text())
			continue
		}
		uses = append(uses, fmt.Sprintf("%s (line %d)", d.Text(), d.Position.Line))
	}
	return uses
}

// declaredInEntry reports whether the entry source declares the name.
func declaredInEntry(uses []nameUse) (nameUse, bool) {
	for _, use := range uses {
		if use.role == "declaration" {
			return use, true
		}
	}
	return nameUse{}, false
}

func trackDeclared(score *notation.Score, name string) bool {
	for _, track := range score.Tracks {
		if track.Name == name {
			return true
		}
	}
	return false
}

func patternDeclared(score *notation.Score, name string) bool {
	for _, pattern := range score.Patterns {
		if pattern.Name == name {
			return true
		}
	}
	return false
}

func sceneDeclared(score *notation.Score, name string) bool {
	for _, scene := range score.Scenes {
		if scene.Name == name {
			return true
		}
	}
	return false
}

// lineAfter returns the offset just past the line that holds offset.
func lineAfter(source []byte, offset int) int {
	if next := bytes.IndexByte(source[offset:], '\n'); next >= 0 {
		return offset + next + 1
	}
	return len(source)
}

// insertAfterLast puts a one-line declaration on the line after the last
// top-level declaration of one of types. With none, it goes before the first
// declaration of one of before, separated by a blank line, or at the end.
func insertAfterLast(source []byte, types, before []string, text string) ([]byte, error) {
	root, walker, err := notation.ParseTree(source)
	if err != nil {
		return nil, err
	}
	newline := Newline(source)
	last := -1
	first := -1
	for i := 0; i < root.NamedChildCount(); i++ {
		node := root.NamedChild(i)
		kind := walker.Type(node)
		if containsString(types, kind) {
			last = int(node.EndByte())
		}
		if first < 0 && containsString(before, kind) {
			first = int(node.StartByte())
		}
	}
	if last >= 0 {
		at := lineAfter(source, last)
		if at == len(source) && (at == 0 || source[at-1] != '\n') {
			return replaceMixerSpan(source, at, at, []byte(newline+text+newline)), nil
		}
		return replaceMixerSpan(source, at, at, []byte(text+newline)), nil
	}
	if first >= 0 {
		at := attachedCommentStart(source, first, 0)
		return replaceMixerSpan(source, at, at, []byte(text+newline+newline)), nil
	}
	at := topLevelAppendOffset(source, root, walker)
	return replaceMixerSpan(source, at, at, []byte(topLevelSeparator(source, at, text))), nil
}

// innerRemoval returns the span that removes a construct inside a
// declaration. A construct alone on its line (a trailing comment counts as
// its own) takes the line and the comment lines above it, down to lower.
// Otherwise it takes the spaces after it, or before it at the end of a line.
func innerRemoval(source []byte, start, end, lower int) (int, int) {
	lineStart := bytes.LastIndexByte(source[:start], '\n') + 1
	lineEnd := end
	for lineEnd < len(source) && source[lineEnd] != '\n' {
		lineEnd++
	}
	rest := bytes.TrimSpace(source[end:lineEnd])
	if len(bytes.TrimSpace(source[lineStart:start])) == 0 && (len(rest) == 0 || bytes.HasPrefix(rest, []byte("//"))) {
		if lineEnd < len(source) {
			lineEnd++
		}
		return attachedCommentStart(source, lineStart, lower), lineEnd
	}
	after := end
	for after < len(source) && (source[after] == ' ' || source[after] == '\t') {
		after++
	}
	before := start
	for before > lineStart && (source[before-1] == ' ' || source[before-1] == '\t') {
		before--
	}
	// The last construct between braces on one line leaves "{}".
	if before > 0 && source[before-1] == '{' && after < len(source) && source[after] == '}' {
		return before, after
	}
	if after > end && after < len(source) && source[after] != '\n' && source[after] != '\r' && !bytes.HasPrefix(source[after:], []byte("//")) {
		return start, after
	}
	return before, end
}

// removeSpans removes the given spans from source; spans may not overlap.
func removeSpans(source []byte, spans [][2]int) []byte {
	sort.Slice(spans, func(i, j int) bool { return spans[i][0] > spans[j][0] })
	updated := bytes.Clone(source)
	limit := len(source)
	for _, span := range spans {
		if span[1] > limit {
			span[1] = limit
		}
		if span[0] < span[1] {
			updated = replaceMixerSpan(updated, span[0], span[1], nil)
		}
		limit = span[0]
	}
	return updated
}

func addTrack(ctx *Context, intent Intent) error {
	in := intent.(*AddTrack)
	score, err := validScore(ctx, "adding a track")
	if err != nil {
		return err
	}
	if in.Voice != "" && in.Preset != "" {
		return refuse("voice_and_preset", "preset", "give voice or preset, not both")
	}
	if !patternName.MatchString(in.Name) {
		return refuse("invalid_name", "name", "choose a lowercase track name of at most 64 letters, digits, underscores or hyphens")
	}
	if mixerNameTaken(score, in.Name) {
		return refuse("name_taken", "name", "%s already names an effect, a track or a bus", in.Name)
	}
	if len(score.Tracks) >= limits.MaxTracks {
		return refuse("track_limit", "name", "a project supports at most %d tracks", limits.MaxTracks)
	}
	var updated []byte
	if in.Preset != "" {
		patch, ok := instrument.FindPatch(in.Preset)
		if !ok {
			return refuse("unknown_preset", "preset", "unknown preset %q; the instrument library has no such patch", in.Preset)
		}
		name, field := in.Instrument, "instrument"
		if name == "" {
			name, field = in.Name, "name"
		}
		if !patternName.MatchString(name) {
			return refuse("invalid_name", field, "choose a lowercase instrument name of at most 64 letters, digits, underscores or hyphens")
		}
		// The names are checked above; what is left is an instrument, sampler
		// or kit that already has the name.
		if updated, err = addPresetSourceParsed(ctx.Source, ctx.Options.Path, in.Preset, name, in.Name, score); err != nil {
			return &ReasonError{Reason: "name_taken", Field: field, Message: err.Error()}
		}
		ctx.Source, ctx.plan = updated, nil
		ctx.SetLabel("Track " + in.Name + " added, playing " + patch.Name)
		return nil
	}
	if !patternName.MatchString(in.Voice) && !qualifiedVoice(in.Voice) {
		return refuse("voice_invalid", "voice", "voice %q is not a name", in.Voice)
	}
	updated, err = insertAfterLast(ctx.Source, []string{"track_decl"}, []string{"phrase_decl", "acid_pattern", "note_pattern", "drum_pattern", "performance_pattern", "scene_decl", "song_decl", "arrange_decl"}, "track "+in.Name+" "+in.Voice+" {}")
	if err != nil {
		return err
	}
	errs, err := candidateErrors(ctx, updated)
	if err != nil {
		return err
	}
	if len(errs) > 0 {
		return &ReasonError{Reason: "voice_invalid", Field: "voice", Uses: diagnosticUses(errs), Message: fmt.Sprintf("a track cannot play %q: %s", in.Voice, errs[0].Text())}
	}
	ctx.Source, ctx.plan = updated, nil
	ctx.SetLabel("Track " + in.Name + " added, playing " + in.Voice)
	return nil
}

// qualifiedVoice accepts a library voice such as drums.steel.
func qualifiedVoice(voice string) bool {
	parts := strings.Split(voice, ".")
	if len(parts) < 2 {
		return false
	}
	for _, part := range parts {
		if !patternName.MatchString(part) {
			return false
		}
	}
	return true
}

// entityUses parses the entry source's names and finds one entity's uses,
// with its declaration. missing builds the refusal for a name the entry does
// not declare.
func entityUses(ctx *Context, kind, name string, missing func() error) ([]nameUse, error) {
	all, err := nameUses(ctx.Source)
	if err != nil {
		return nil, err
	}
	uses := usesOf(all, kind, name)
	if _, ok := declaredInEntry(uses); !ok {
		return nil, missing()
	}
	return uses, nil
}

// declaredElsewhere refuses an entity that another file of the project
// declares: the edit changes only the entry.
func declaredElsewhere(kind, name, field, file string) error {
	return &ReasonError{Reason: "declared_in_other_file", Field: field, File: file, Message: fmt.Sprintf("%s %s is declared in %s, another file of the project; this edit changes only the entry", kind, name, file)}
}

// missingTrack, missingPattern and missingScene refuse a name the entry does
// not declare: declared_in_other_file when another file does, else unknown.
func missingTrack(ctx *Context, score *notation.Score, name, field string) func() error {
	return func() error {
		if file := declaringFile(ctx, score, KindTrack, name); file != "" {
			return declaredElsewhere("track", name, field, file)
		}
		return refuse("unknown_track", field, "unknown track %q", name)
	}
}

func missingPattern(ctx *Context, score *notation.Score, name, field string) func() error {
	return func() error {
		if file := declaringFile(ctx, score, KindPattern, name); file != "" {
			return declaredElsewhere("pattern", name, field, file)
		}
		return refuse("unknown_pattern", field, "unknown pattern %q", name)
	}
}

func missingScene(ctx *Context, score *notation.Score, name, field string) func() error {
	return func() error {
		if file := declaringFile(ctx, score, KindScene, name); file != "" {
			return declaredElsewhere("scene", name, field, file)
		}
		return refuse("unknown_scene", field, "unknown scene %q", name)
	}
}

// declaringFile returns the file of the project that declares a track, a
// pattern or a scene when it is not the entry, else "".
func declaringFile(ctx *Context, score *notation.Score, kind, name string) string {
	var position notation.Position
	found := false
	switch kind {
	case KindTrack:
		for _, track := range score.Tracks {
			if track.Name == name {
				position, found = track.Position, true
			}
		}
	case KindPattern:
		for _, pattern := range score.Patterns {
			if pattern.Name == name {
				position, found = pattern.Position, true
			}
		}
	case KindScene:
		for _, scene := range score.Scenes {
			if scene.Name == name {
				position, found = scene.Position, true
			}
		}
	}
	if !found || position.File == "" || position.File == ctx.Options.Path {
		return ""
	}
	return position.File
}

func useNames(uses []nameUse) []string {
	names := make([]string, 0, len(uses))
	for _, use := range uses {
		names = append(names, use.use)
	}
	return names
}

func deleteTrack(ctx *Context, intent Intent) error {
	in := intent.(*DeleteTrack)
	score, err := validScore(ctx, "deleting a track")
	if err != nil {
		return err
	}
	uses, err := entityUses(ctx, KindTrack, in.Track, missingTrack(ctx, score, in.Track, "track"))
	if err != nil {
		return err
	}
	var bindings, blocking []nameUse
	for _, use := range uses {
		switch use.role {
		case "declaration":
		case "binding":
			bindings = append(bindings, use)
		default:
			blocking = append(blocking, use)
		}
	}
	if len(blocking) > 0 {
		names := useNames(blocking)
		return &ReasonError{Reason: "track_in_use", Field: "track", Uses: names, Message: fmt.Sprintf("track %s is still used by %s; remove those first", in.Track, strings.Join(names, ", "))}
	}
	// Remove the bindings first: they sit in scenes, apart from the
	// declaration, so the declaration's offsets stay valid.
	spans := make([][2]int, 0, len(bindings))
	for _, binding := range bindings {
		start, end := innerRemoval(ctx.Source, int(binding.node.StartByte()), int(binding.node.EndByte()), int(binding.node.Parent().StartByte()))
		spans = append(spans, [2]int{start, end})
	}
	updated := removeSpans(ctx.Source, spans)
	root, walker, err := notation.ParseTree(updated)
	if err != nil {
		return err
	}
	for i := 0; i < root.NamedChildCount(); i++ {
		node := root.NamedChild(i)
		if walker.Type(node) == "track_decl" && walker.Text(walker.Field(node, "name")) == in.Track {
			updated = removeTopLevelDeclaration(updated, root, node)
			break
		}
	}
	if err := resultValid(ctx, updated, "track", "deleting track "+in.Track); err != nil {
		return err
	}
	ctx.Source, ctx.plan = updated, nil
	label := "Track " + in.Track + " deleted"
	if len(bindings) > 0 {
		label += fmt.Sprintf(" with its %d scene binding", len(bindings))
		if len(bindings) > 1 {
			label += "s"
		}
	}
	ctx.SetLabel(label)
	return nil
}

func setTrackVoice(ctx *Context, intent Intent) error {
	in := intent.(*SetTrackVoice)
	score, err := validScore(ctx, "changing a track's voice")
	if err != nil {
		return err
	}
	if _, err := entityUses(ctx, KindTrack, in.Track, missingTrack(ctx, score, in.Track, "track")); err != nil {
		return err
	}
	if !patternName.MatchString(in.Voice) && !qualifiedVoice(in.Voice) {
		return refuse("voice_invalid", "voice", "voice %q is not a name", in.Voice)
	}
	node, walker, err := declaration(ctx.Source, []string{"track_decl"}, in.Track)
	if err != nil {
		return err
	}
	kind := walker.Field(node, "kind")
	before := walker.Text(kind)
	if before == in.Voice {
		return refuse("voice_unchanged", "voice", "track %s already plays %s", in.Track, in.Voice)
	}
	updated := replaceMixerSpan(ctx.Source, int(kind.StartByte()), int(kind.EndByte()), []byte(in.Voice))
	errs, err := candidateErrors(ctx, updated)
	if err != nil {
		return err
	}
	// Errors inside the track's own settings name settings the new voice does
	// not have; anything else is a pattern or reference it cannot take.
	node, walker, err = declaration(updated, []string{"track_decl"}, in.Track)
	if err != nil {
		return err
	}
	type setting struct {
		name       string
		start, end int
		line       int
	}
	var settings []setting
	for i := 0; i < node.NamedChildCount(); i++ {
		// Only settings can be dropped; a pattern chain plays music.
		child := node.NamedChild(i)
		if walker.Type(child) != "mix_setting" {
			continue
		}
		name := walker.Text(walker.Field(child, "name"))
		if name == "" && child.NamedChildCount() > 0 {
			name = walker.Text(walker.Field(child.NamedChild(0), "name"))
		}
		if name == "" {
			name, _, _ = strings.Cut(strings.TrimSpace(walker.Text(child)), " ")
		}
		line, _ := walker.Pos(child)
		settings = append(settings, setting{name: name, start: int(child.StartByte()), end: int(child.EndByte()), line: line})
	}
	var unsupported []setting
	var other []notation.Diagnostic
	for _, d := range errs {
		at, offsetErr := Offset(updated, ctx.Options.Path, d.Position)
		matched := false
		for _, s := range settings {
			if offsetErr == nil && s.start <= at && at < s.end {
				matched = true
				if len(unsupported) == 0 || unsupported[len(unsupported)-1].start != s.start {
					unsupported = append(unsupported, s)
				}
				break
			}
		}
		if !matched {
			other = append(other, d)
		}
	}
	if len(other) > 0 {
		uses := diagnosticUses(other)
		return &ReasonError{Reason: "voice_change_invalid", Field: "voice", Uses: uses, Message: fmt.Sprintf("track %s cannot change to %s: %s", in.Track, in.Voice, strings.Join(uses, "; "))}
	}
	var dropped []string
	for _, s := range unsupported {
		dropped = append(dropped, s.name)
	}
	if len(unsupported) > 0 && !in.DropParams {
		uses := make([]string, 0, len(unsupported))
		for _, s := range unsupported {
			uses = append(uses, fmt.Sprintf("setting %s (line %d)", s.name, s.line))
		}
		return &ReasonError{Reason: "voice_params_unsupported", Field: "voice", Uses: uses, Message: fmt.Sprintf("%s has no setting %s that track %s sets; give dropparams to remove them", in.Voice, strings.Join(dropped, ", "), in.Track)}
	}
	if len(unsupported) > 0 {
		spans := make([][2]int, 0, len(unsupported))
		for _, s := range unsupported {
			start, end := innerRemoval(updated, s.start, s.end, int(node.StartByte()))
			spans = append(spans, [2]int{start, end})
		}
		updated = removeSpans(updated, spans)
		if errs, err = candidateErrors(ctx, updated); err != nil {
			return err
		}
		if len(errs) > 0 {
			uses := diagnosticUses(errs)
			return &ReasonError{Reason: "voice_change_invalid", Field: "voice", Uses: uses, Message: fmt.Sprintf("track %s cannot change to %s: %s", in.Track, in.Voice, strings.Join(uses, "; "))}
		}
	}
	ctx.Source, ctx.plan = updated, nil
	label := "Track " + in.Track + ": voice " + before + " changed to " + in.Voice
	if len(dropped) > 0 {
		label += "; dropped settings " + strings.Join(dropped, ", ")
	}
	ctx.SetLabel(label)
	return nil
}

func validNewName(kind, name string) error {
	if !patternName.MatchString(name) {
		return refuse("invalid_name", "name", "choose a lowercase %s name of at most 64 letters, digits, underscores or hyphens", kind)
	}
	if kind == "pattern" && (name == "off" || name == "keep" || name == "stop") {
		return refuse("invalid_name", "name", "off, keep and stop are scene controls; choose another pattern name")
	}
	return nil
}

func sceneNameTaken(score *notation.Score, name string) error {
	if sceneDeclared(score, name) {
		return refuse("name_taken", "name", "scene %s already exists", name)
	}
	return nil
}

func addScene(ctx *Context, intent Intent) error {
	in := intent.(*AddScene)
	score, err := validScore(ctx, "adding a scene")
	if err != nil {
		return err
	}
	if err := validNewName("scene", in.Name); err != nil {
		return err
	}
	if err := sceneNameTaken(score, in.Name); err != nil {
		return err
	}
	updated, err := insertAfterLast(ctx.Source, []string{"scene_decl"}, []string{"song_decl", "arrange_decl"}, "scene "+in.Name+" {}")
	if err != nil {
		return err
	}
	ctx.Source, ctx.plan = updated, nil
	ctx.SetLabel("Scene " + in.Name + " added")
	return nil
}

func duplicateScene(ctx *Context, intent Intent) error {
	in := intent.(*DuplicateScene)
	score, err := validScore(ctx, "duplicating a scene")
	if err != nil {
		return err
	}
	if _, err := entityUses(ctx, KindScene, in.Scene, missingScene(ctx, score, in.Scene, "scene")); err != nil {
		return err
	}
	if err := validNewName("scene", in.Name); err != nil {
		return err
	}
	if err := sceneNameTaken(score, in.Name); err != nil {
		return err
	}
	node, walker, err := declaration(ctx.Source, []string{"scene_decl"}, in.Scene)
	if err != nil {
		return err
	}
	start, end := int(node.StartByte()), int(node.EndByte())
	name := walker.Field(node, "name")
	clone := string(ctx.Source[start:name.StartByte()]) + in.Name + string(ctx.Source[name.EndByte():end])
	newline := Newline(ctx.Source)
	at := lineAfter(ctx.Source, end)
	text := clone + newline
	if at == len(ctx.Source) && (at == 0 || ctx.Source[at-1] != '\n') {
		text = newline + clone + newline
	}
	ctx.Source, ctx.plan = replaceMixerSpan(ctx.Source, at, at, []byte(text)), nil
	ctx.SetLabel("Scene " + in.Scene + " duplicated as " + in.Name)
	return nil
}

func deleteScene(ctx *Context, intent Intent) error {
	in := intent.(*DeleteScene)
	score, err := validScore(ctx, "deleting a scene")
	if err != nil {
		return err
	}
	uses, err := entityUses(ctx, KindScene, in.Scene, missingScene(ctx, score, in.Scene, "scene"))
	if err != nil {
		return err
	}
	var entries, blocking []nameUse
	for _, use := range uses {
		switch {
		case use.role == "declaration":
		case in.Cascade && strings.HasPrefix(use.use, "song entry "):
			entries = append(entries, use)
		default:
			blocking = append(blocking, use)
		}
	}
	if len(blocking) > 0 {
		names := useNames(blocking)
		message := fmt.Sprintf("scene %s is still used by %s", in.Scene, strings.Join(names, ", "))
		if !in.Cascade {
			message += "; remove those first, or give cascade to remove its song entries with it"
		}
		return &ReasonError{Reason: "scene_in_use", Field: "scene", Uses: names, Message: message}
	}
	updated := ctx.Source
	if len(entries) > 0 {
		if len(entries) == len(score.Song) {
			return refuse("song_would_be_empty", "cascade", "every song entry plays scene %s; the song would be left empty", in.Scene)
		}
		spans := make([][2]int, 0, len(entries))
		for _, entry := range entries {
			start, end := innerRemoval(updated, int(entry.node.StartByte()), int(entry.node.EndByte()), int(entry.node.Parent().StartByte()))
			spans = append(spans, [2]int{start, end})
		}
		updated = removeSpans(updated, spans)
	}
	root, walker, err := notation.ParseTree(updated)
	if err != nil {
		return err
	}
	for i := 0; i < root.NamedChildCount(); i++ {
		node := root.NamedChild(i)
		if walker.Type(node) == "scene_decl" && walker.Text(walker.Field(node, "name")) == in.Scene {
			updated = removeTopLevelDeclaration(updated, root, node)
			break
		}
	}
	if err := resultValid(ctx, updated, "scene", "deleting scene "+in.Scene); err != nil {
		return err
	}
	ctx.Source, ctx.plan = updated, nil
	label := "Scene " + in.Scene + " deleted"
	if len(entries) > 0 {
		label += fmt.Sprintf(" with %d song entr", len(entries))
		if len(entries) == 1 {
			label += "y"
		} else {
			label += "ies"
		}
	}
	ctx.SetLabel(label)
	return nil
}

func deletePattern(ctx *Context, intent Intent) error {
	in := intent.(*DeletePattern)
	score, err := validScore(ctx, "deleting a pattern")
	if err != nil {
		return err
	}
	uses, err := entityUses(ctx, KindPattern, in.Pattern, missingPattern(ctx, score, in.Pattern, "pattern"))
	if err != nil {
		return err
	}
	var bindings, placements, blocking []nameUse
	for _, use := range uses {
		switch {
		case use.role == "declaration":
		case in.Cascade && use.role == "binding":
			bindings = append(bindings, use)
		case in.Cascade && strings.HasPrefix(use.use, "placement "):
			placements = append(placements, use)
		default:
			blocking = append(blocking, use)
		}
	}
	if len(blocking) > 0 {
		names := useNames(blocking)
		message := fmt.Sprintf("pattern %s is still used by %s", in.Pattern, strings.Join(names, ", "))
		if !in.Cascade {
			message += "; remove those first, or give cascade to set its scene bindings to off and remove its placements"
		}
		return &ReasonError{Reason: "pattern_in_use", Field: "pattern", Uses: names, Message: message}
	}
	spans := make([]Span, 0, len(bindings))
	for _, binding := range bindings {
		spans = append(spans, Span{binding.start, binding.end, "off", ctx.Options.Path})
	}
	updated, err := PatchSpans(ctx.Source, ctx.Options.Path, spans)
	if err != nil {
		return err
	}
	if len(placements) > 0 {
		removals := make([][2]int, 0, len(placements))
		all, err := nameUses(updated)
		if err != nil {
			return err
		}
		for _, use := range usesOf(all, "pattern", in.Pattern) {
			if strings.HasPrefix(use.use, "placement ") {
				node := use.node
				removals = append(removals, [2]int{attachedCommentStart(updated, int(node.StartByte()), int(node.Parent().StartByte())), lineEndOf(updated, node)})
			}
		}
		updated = removeSpans(updated, removals)
	}
	root, walker, err := notation.ParseTree(updated)
	if err != nil {
		return err
	}
	for i := 0; i < root.NamedChildCount(); i++ {
		node := root.NamedChild(i)
		if containsString(patternTypes, walker.Type(node)) && walker.Text(walker.Field(node, "name")) == in.Pattern {
			updated = removeTopLevelDeclaration(updated, root, node)
			break
		}
	}
	if err := resultValid(ctx, updated, "pattern", "deleting pattern "+in.Pattern); err != nil {
		return err
	}
	ctx.Source, ctx.plan = updated, nil
	label := "Pattern " + in.Pattern + " deleted"
	var parts []string
	if len(bindings) > 0 {
		parts = append(parts, fmt.Sprintf("%d scene binding%s set to off", len(bindings), plural(len(bindings))))
	}
	if len(placements) > 0 {
		parts = append(parts, fmt.Sprintf("%d placement%s removed", len(placements), plural(len(placements))))
	}
	if len(parts) > 0 {
		label += "; " + strings.Join(parts, ", ")
	}
	ctx.SetLabel(label)
	return nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// lineEndOf returns the offset past the line a node ends on when only
// whitespace follows it there, else the node's end.
func lineEndOf(source []byte, node *gts.Node) int {
	end := int(node.EndByte())
	next := lineAfter(source, end)
	if len(bytes.TrimSpace(source[end:next])) == 0 {
		return next
	}
	return end
}

func renameEntity(ctx *Context, intent Intent) error {
	in := intent.(*Rename)
	kind, old := in.Entity.Kind(), in.Entity.Name()
	if kind != KindTrack && kind != KindPattern && kind != KindScene {
		return refuse("unknown_entity", "entity", "rename takes a track, a pattern or a scene, not %q", in.Entity)
	}
	score, err := validScore(ctx, "renaming a "+kind)
	if err != nil {
		return err
	}
	missing := missingTrack(ctx, score, old, "entity")
	switch kind {
	case KindPattern:
		missing = missingPattern(ctx, score, old, "entity")
	case KindScene:
		missing = missingScene(ctx, score, old, "entity")
	}
	uses, err := entityUses(ctx, kind, old, missing)
	if err != nil {
		return err
	}
	if in.Name == old {
		return refuse("name_unchanged", "name", "%s %s already has that name", kind, old)
	}
	if err := validNewName(kind, in.Name); err != nil {
		return err
	}
	switch kind {
	case KindTrack:
		if mixerNameTaken(score, in.Name) {
			return refuse("name_taken", "name", "%s already names an effect, a track or a bus", in.Name)
		}
	case KindPattern:
		if patternDeclared(score, in.Name) {
			return refuse("name_taken", "name", "pattern %s already exists", in.Name)
		}
		for _, clip := range score.Clips {
			if clip.Name == in.Name {
				return refuse("name_taken", "name", "clip %s already exists; patterns and clips share names", in.Name)
			}
		}
	case KindScene:
		if err := sceneNameTaken(score, in.Name); err != nil {
			return err
		}
	}
	spans := make([]Span, 0, len(uses))
	for _, use := range uses {
		spans = append(spans, Span{use.start, use.end, in.Name, ctx.Options.Path})
	}
	updated, err := PatchSpans(ctx.Source, ctx.Options.Path, spans)
	if err != nil {
		return err
	}
	// A reference the walk does not know is left dangling; validation names
	// it, and the rename is refused rather than written half done.
	errs, err := candidateErrors(ctx, updated)
	if err != nil {
		return err
	}
	if len(errs) > 0 {
		uses := diagnosticUses(errs)
		return &ReasonError{Reason: "rename_incomplete", Field: "entity", Uses: uses, Message: fmt.Sprintf("renaming %s %s to %s leaves the score invalid: %s", kind, old, in.Name, strings.Join(uses, "; "))}
	}
	ctx.Source, ctx.plan = updated, nil
	ctx.SetLabel(fmt.Sprintf("%s %s renamed to %s (%d reference%s)", strings.ToUpper(kind[:1])+kind[1:], old, in.Name, len(uses)-1, plural(len(uses)-1)))
	return nil
}
