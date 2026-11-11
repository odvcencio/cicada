package edit

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/kernel/limits"
	"m31labs.dev/cicada/notation"
)

// The structural intents edit every source file of the project that names
// the entity, apart from libraries, in one revision-checked change with one
// label (one Undo step in Studio); other files come back as staged files of
// the result. Which comments a removal takes is decided in one place,
// attachedComments (edit/effects.go), and described in docs/manual/editors.md.
// The catalog's reasons list of each intent names every refusal code.

// AddTrack adds a track after the last track. Voice names what the track
// plays: a built-in voice (acid, drums, piano, a modal or keyboard voice) or
// an instrument, kit, sampler or preset the score declares or imports.
// Preset instead declares a patch from the instrument library as a new
// instrument, named instrument or after the track, as addpreset does;
// instrument is refused without preset. The new track plays nothing until a
// scene binds a pattern to it (bindscene) or a placement puts one on it. A
// voice the score cannot play is refused with voice_invalid, and uses lists
// the problems.
//
// Example: {"kind":"addtrack","name":"pad","voice":"piano"}
// Example: {"kind":"addtrack","name":"pad","preset":"warm-pad","instrument":"warm"}
type AddTrack struct {
	Name       string `json:"name" cicada:"required"` // the new track's name
	Voice      string `json:"voice,omitempty"`        // a built-in voice, or a declared or imported instrument, kit, sampler or preset
	Preset     string `json:"preset,omitempty"`       // a patch from the instrument library, such as warm-pad
	Instrument string `json:"instrument,omitempty"`   // with preset, the name of the instrument it declares; default the track's name
	Reseed     bool   `json:"reseed,omitempty"`       // accept that random choices of other tracks change (see deletetrack)
}

func (AddTrack) Kind() string { return "addtrack" }

// DeleteTrack removes a track: its declaration and its bindings in scenes, in
// every file of the project. Its patterns stay. Refused with track_in_use
// while anything else names the track: automation, a scene setting, a
// modulation route, an envelope source, a MIDI mapping, a live macro,
// stinger or layer, an arrangement placement or a compressor sidechain; uses
// lists them. The last track is refused with last_track. The engine seeds
// chance and humanize by a track's position, so deleting a track before a
// track that plays such a pattern changes its random choices: refused with
// reseeds_random_choices unless reseed accepts it, and then the result's
// warnings name each track.
//
// Example: {"kind":"deletetrack","track":"keys"}
// Example: {"kind":"deletetrack","track":"keys","reseed":true}
type DeleteTrack struct {
	Track  string `json:"track" cicada:"required"` // the track's name
	Reseed bool   `json:"reseed,omitempty"`        // accept that random choices of later tracks change
}

func (DeleteTrack) Kind() string { return "deletetrack" }

// SetTrackVoice changes what a track plays to another voice: a built-in
// voice, or an instrument, kit, sampler or preset the score declares or
// imports. The track keeps its name, mixer settings, scene bindings and
// chain. Nothing changes silently. A track setting the new voice lacks is
// refused with voice_params_unsupported, unless dropparams removes it (the
// label names each). A pattern the new voice cannot play, a parameter path
// (automation, scene setting, modulation route, MIDI or live mapping) it does
// not take, or any other problem the change adds to cicada check is refused
// with voice_change_invalid, and uses lists every one. A change that plays
// but sounds different, such as drum lanes the new kit does not play or a
// row or mark the old voice played and the new one ignores (a pressure row on
// acid, accent on piano: notation.VoiceReads), is refused with
// voice_changes_sound unless acceptchanges accepts it; the result's warnings
// then name each.
//
// Example: {"kind":"settrackvoice","track":"keys","voice":"glow"}
// Example: {"kind":"settrackvoice","track":"lead","voice":"acid","dropparams":true}
// Example: {"kind":"settrackvoice","track":"keys","voice":"glow","acceptchanges":true}
type SetTrackVoice struct {
	Track         string `json:"track" cicada:"required"` // the track's name
	Voice         string `json:"voice" cicada:"required"` // the new voice
	DropParams    bool   `json:"dropparams,omitempty"`    // remove the track settings the new voice does not have
	AcceptChanges bool   `json:"acceptchanges,omitempty"` // accept the listed changes to the sound, such as silent drum lanes
}

func (SetTrackVoice) Kind() string { return "settrackvoice" }

// AddScene adds an empty scene after the last scene. Bind patterns to it with
// bindscene and play it with appendsongentry. In the song, a track a scene
// does not bind keeps playing what the scene before it selected.
//
// Example: {"kind":"addscene","name":"drop"}
type AddScene struct {
	Name string `json:"name" cicada:"required"` // the new scene's name
}

func (AddScene) Kind() string { return "addscene" }

// DuplicateScene copies a scene, with its bindings, settings and the comments
// inside it, under a new name right after the original.
//
// Example: {"kind":"duplicatescene","scene":"main","name":"main-2"}
type DuplicateScene struct {
	Scene string `json:"scene" cicada:"required"` // the scene to copy
	Name  string `json:"name" cicada:"required"`  // the copy's name
}

func (DuplicateScene) Kind() string { return "duplicatescene" }

// DeleteScene removes a scene. Refused with scene_in_use while the song, a
// live state or a MIDI launch mapping names it; uses lists them. With cascade
// the scene's song entries are removed with it; live states and MIDI
// mappings still refuse, and so does a song that would be left empty
// (song_would_be_empty). Random choices that move are refused with
// reseeds_random_choices unless reseed accepts them (see deletetrack).
//
// Example: {"kind":"deletescene","scene":"outro"}
// Example: {"kind":"deletescene","scene":"outro","cascade":true}
type DeleteScene struct {
	Scene   string `json:"scene" cicada:"required"` // the scene's name
	Cascade bool   `json:"cascade,omitempty"`       // also remove the song entries that play the scene
	Reseed  bool   `json:"reseed,omitempty"`        // accept that random choices change
}

func (DeleteScene) Kind() string { return "deletescene" }

// DeletePattern removes a pattern. Refused with pattern_in_use while a scene
// binds it (a binding to a pattern named stop included), or a track chain,
// an arrangement placement, a live stinger or a MIDI launch mapping names it;
// uses lists them. With cascade, each scene binding of the pattern becomes
// off (the track stops in that scene; an omitted binding would keep the
// previous pattern playing) and its placements are removed; chains,
// stingers and MIDI mappings still refuse. The last pattern is refused with
// last_pattern. The engine seeds chance and humanize by a pattern's slot on
// its track, so a deletion that moves the slots of such patterns is refused
// with reseeds_random_choices unless reseed accepts it.
//
// Example: {"kind":"deletepattern","pattern":"fill"}
// Example: {"kind":"deletepattern","pattern":"fill","cascade":true}
type DeletePattern struct {
	Pattern string `json:"pattern" cicada:"required"` // the pattern's name
	Cascade bool   `json:"cascade,omitempty"`         // set its scene bindings to off and remove its placements
	Reseed  bool   `json:"reseed,omitempty"`          // accept that random choices change
}

func (DeletePattern) Kind() string { return "deletepattern" }

// Rename renames a track, a pattern or a scene and every reference to it, in
// every file of the project: scene bindings and settings, the song,
// automation, modulation routes and envelope sources, MIDI mappings, live
// states, stingers, layers and macros, track chains, arrangement placements
// and compressor sidechains. Comments, strings and other text stay as they
// are. A track named master whose name also begins parameter paths is
// refused with name_ambiguous: master.<field> may mean the master bus. A
// rename that would leave the score invalid is refused with
// rename_incomplete, and uses lists every problem.
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

// validNewName refuses a new name that is not a name, or that a scene binding
// would read as a control.
func validNewName(kind, name string) error {
	if !patternName.MatchString(name) {
		return refuse("invalid_name", "name", "choose a lowercase %s name of at most 64 letters, digits, underscores or hyphens", kind)
	}
	if kind == KindPattern && (name == "off" || name == "keep" || name == "stop") {
		return refuse("invalid_name", "name", "off, keep and stop are scene controls; choose another pattern name")
	}
	return nil
}

// nameFree refuses a new name its namespace already has, or that an import
// uses as its alias.
func nameFree(score *notation.Score, kind, name, field string) error {
	if namespace, ok := score.LibraryAliases[name]; ok {
		return refuse("name_taken", field, "%s is the name of the imported library %s", name, namespace)
	}
	switch kind {
	case KindTrack:
		if mixerNameTaken(score, name) {
			return refuse("name_taken", field, "%s already names an effect, a track or a bus", name)
		}
	case KindPattern:
		for _, pattern := range score.Patterns {
			if pattern.Name == name {
				return refuse("name_taken", field, "pattern %s already exists", name)
			}
		}
		for _, clip := range score.Clips {
			if clip.Name == name {
				return refuse("name_taken", field, "clip %s already exists; patterns and clips share names", name)
			}
		}
	case KindScene:
		for _, scene := range score.Scenes {
			if scene.Name == name {
				return refuse("name_taken", field, "scene %s already exists", name)
			}
		}
	}
	return nil
}

// findEntity finds where a track, pattern or scene is declared and used. A
// name no file of the project declares is refused by missing.
func findEntity(st *staged, kind, name string, missing func() error) ([]fileUses, string, error) {
	found, err := projectUses(st, kind, name)
	if err != nil {
		return nil, "", err
	}
	if path, ok := declarationIn(found); ok {
		return found, path, nil
	}
	return nil, "", missing()
}

// unknownTrack, unknownPattern and unknownScene refuse a name no file of the
// project declares.
func unknownTrack(name, field string) func() error {
	return func() error { return refuse("unknown_track", field, "unknown track %q", name) }
}

func unknownPattern(name, field string) func() error {
	return func() error { return refuse("unknown_pattern", field, "unknown pattern %q", name) }
}

func unknownScene(name, field string) func() error {
	return func() error { return refuse("unknown_scene", field, "unknown scene %q", name) }
}

// usesOutside lists the uses whose role is not one of roles, named for a
// refusal.
func usesOutside(st *staged, found []fileUses, keep func(notation.NameUse) bool) []string {
	var names []string
	for _, file := range found {
		for _, use := range file.uses {
			if use.Role != "declaration" && !keep(use) {
				names = append(names, describe(st.ctx, file.path, use))
			}
		}
	}
	return names
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
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
	if in.Instrument != "" && in.Preset == "" {
		return refuse("instrument_without_preset", "instrument", "instrument names the instrument a preset declares; give preset with it, or leave instrument out")
	}
	if err := validNewName(KindTrack, in.Name); err != nil {
		return err
	}
	if err := nameFree(score, KindTrack, in.Name, "name"); err != nil {
		return err
	}
	if len(score.Tracks) >= limits.MaxTracks {
		return refuse("track_limit", "name", "a project supports at most %d tracks", limits.MaxTracks)
	}
	st := newStaged(ctx)
	label := ""
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
		if notation.BuiltinVoice(score.Version, name) {
			return refuse("invalid_name", field, "%s is a built-in voice; choose another instrument name", name)
		}
		if namespace, ok := score.LibraryAliases[name]; ok {
			return refuse("name_taken", field, "%s is the name of the imported library %s", name, namespace)
		}
		// The names are checked above; what is left is an instrument, sampler
		// or kit that already has the name.
		updated, err := addPresetSourceParsed(ctx.Source, ctx.Options.Path, in.Preset, name, in.Name, score)
		if err != nil {
			return &ReasonError{Reason: "name_taken", Field: field, Message: err.Error()}
		}
		st.set(ctx.Options.Path, updated)
		if err := resultValid(st, "preset", "adding preset "+in.Preset); err != nil {
			return err
		}
		label = "Track " + in.Name + " added, playing " + patch.Name
	} else {
		if !patternName.MatchString(in.Voice) && !qualifiedVoice(in.Voice) {
			return refuse("voice_invalid", "voice", "voice %q is not a name", in.Voice)
		}
		updated, err := insertAfterLast(ctx.Source, []string{"track_decl"}, []string{"phrase_decl", "acid_pattern", "note_pattern", "drum_pattern", "performance_pattern", "scene_decl", "song_decl", "arrange_decl"}, "track "+in.Name+" "+in.Voice+" {}")
		if err != nil {
			return err
		}
		st.set(ctx.Options.Path, updated)
		ds, err := stagedDiagnostics(st)
		if err != nil {
			return err
		}
		if errs := errorsOf(ds); len(errs) > 0 {
			uses := make([]string, 0, len(errs))
			for _, d := range errs {
				uses = append(uses, diagnosticText(ctx, d))
			}
			return &ReasonError{Reason: "voice_invalid", Field: "voice", Uses: uses, Message: fmt.Sprintf("a track cannot play %q: %s", in.Voice, strings.Join(uses, "; "))}
		}
		label = "Track " + in.Name + " added, playing " + in.Voice
	}
	if err := checkReseed(st, in.Reseed); err != nil {
		return err
	}
	st.commit()
	ctx.SetLabel(label)
	return nil
}

func deleteTrack(ctx *Context, intent Intent) error {
	in := intent.(*DeleteTrack)
	score, err := validScore(ctx, "deleting a track")
	if err != nil {
		return err
	}
	st := newStaged(ctx)
	found, path, err := findEntity(st, KindTrack, in.Track, unknownTrack(in.Track, "track"))
	if err != nil {
		return err
	}
	if len(score.Tracks) == 1 {
		return refuse("last_track", "track", "track %s is the only track, and a score needs one; add another track first", in.Track)
	}
	if blocking := usesOutside(st, found, func(use notation.NameUse) bool { return use.Role == "binding" }); len(blocking) > 0 {
		return &ReasonError{Reason: "track_in_use", Field: "track", Uses: blocking, Message: fmt.Sprintf("track %s is still used by %s; remove those first", in.Track, strings.Join(blocking, ", "))}
	}
	// The bindings sit in scenes, apart from the declaration: remove them
	// first, then find the declaration in the result.
	bindings := 0
	for _, file := range found {
		source := st.source(file.path)
		var spans [][2]int
		for _, use := range file.uses {
			if use.Role == "binding" {
				start, end := innerRemoval(source, int(use.Node.StartByte()), int(use.Node.EndByte()), int(use.Node.Parent().StartByte()))
				spans = append(spans, [2]int{start, end})
				bindings++
			}
		}
		if len(spans) > 0 {
			st.set(file.path, removeSpans(source, spans))
		}
	}
	updated, err := withoutDeclaration(st.source(path), []string{"track_decl"}, in.Track)
	if err != nil {
		return err
	}
	st.set(path, updated)
	if err := resultValid(st, "track", "deleting track "+in.Track); err != nil {
		return err
	}
	if err := checkReseed(st, in.Reseed); err != nil {
		return err
	}
	st.commit()
	label := "Track " + in.Track + " deleted"
	if bindings > 0 {
		label += fmt.Sprintf(" with its %d scene binding%s", bindings, plural(bindings))
	}
	ctx.SetLabel(label)
	return nil
}

// withoutDeclaration removes one top-level declaration by type and name.
func withoutDeclaration(source []byte, types []string, name string) ([]byte, error) {
	root, walker, err := notation.ParseTree(source)
	if err != nil {
		return nil, err
	}
	for i := 0; i < root.NamedChildCount(); i++ {
		node := root.NamedChild(i)
		if containsString(types, walker.Type(node)) && walker.Text(walker.Field(node, "name")) == name {
			return removeTopLevelDeclaration(source, root, node), nil
		}
	}
	return nil, fmt.Errorf("unknown declaration %q", name)
}

func setTrackVoice(ctx *Context, intent Intent) error {
	in := intent.(*SetTrackVoice)
	score, err := validScore(ctx, "changing a track's voice")
	if err != nil {
		return err
	}
	if !patternName.MatchString(in.Voice) && !qualifiedVoice(in.Voice) {
		return refuse("voice_invalid", "voice", "voice %q is not a name", in.Voice)
	}
	st := newStaged(ctx)
	_, path, err := findEntity(st, KindTrack, in.Track, unknownTrack(in.Track, "track"))
	if err != nil {
		return err
	}
	node, walker, err := declaration(st.source(path), []string{"track_decl"}, in.Track)
	if err != nil {
		return err
	}
	kind := walker.Field(node, "kind")
	before := walker.Text(kind)
	if before == in.Voice {
		return refuse("voice_unchanged", "voice", "track %s already plays %s", in.Track, in.Voice)
	}
	updated := replaceMixerSpan(st.source(path), int(kind.StartByte()), int(kind.EndByte()), []byte(in.Voice))
	st.set(path, updated)
	node, walker, err = declaration(updated, []string{"track_decl"}, in.Track)
	if err != nil {
		return err
	}
	var settings []trackSetting
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
		settings = append(settings, trackSetting{name: name, start: int(child.StartByte()), end: int(child.EndByte()), line: line})
	}
	change, err := checkVoiceChange(st, score, in.Track, before, in.Voice, path, int(node.StartByte()), int(walker.Field(node, "kind").EndByte()), settings, true)
	if err != nil {
		return err
	}
	if change.voiceInvalid != "" {
		return refuse("voice_invalid", "voice", "a track cannot play %q: %s", in.Voice, change.voiceInvalid)
	}
	if len(change.invalid) > 0 {
		return &ReasonError{Reason: "voice_change_invalid", Field: "voice", Uses: change.invalid, Message: fmt.Sprintf("track %s cannot change to %s: %s", in.Track, in.Voice, strings.Join(change.invalid, "; "))}
	}
	var dropped []string
	for _, s := range change.unsupported {
		dropped = append(dropped, s.name)
	}
	if len(change.unsupported) > 0 && !in.DropParams {
		uses := make([]string, 0, len(change.unsupported))
		for _, s := range change.unsupported {
			uses = append(uses, fmt.Sprintf("setting %s (line %d)", s.name, s.line))
		}
		return &ReasonError{Reason: "voice_params_unsupported", Field: "voice", Uses: uses, Message: fmt.Sprintf("%s has no setting %s that track %s sets; give dropparams to remove them", in.Voice, strings.Join(dropped, ", "), in.Track)}
	}
	if len(change.changes) > 0 && !in.AcceptChanges {
		return &ReasonError{Reason: "voice_changes_sound", Field: "voice", Uses: change.changes, Message: fmt.Sprintf("changing track %s to %s changes what it plays: %s; give acceptchanges to accept that", in.Track, in.Voice, strings.Join(change.changes, "; "))}
	}
	if len(change.unsupported) > 0 {
		spans := make([][2]int, 0, len(change.unsupported))
		for _, s := range change.unsupported {
			start, end := innerRemoval(updated, s.start, s.end, int(node.StartByte()))
			spans = append(spans, [2]int{start, end})
		}
		st.set(path, removeSpans(updated, spans))
		if err := resultValid(st, "voice", fmt.Sprintf("changing track %s to %s", in.Track, in.Voice)); err != nil {
			return err
		}
	}
	for _, c := range change.changes {
		ctx.warn("%s", c)
	}
	st.commit()
	label := "Track " + in.Track + ": voice " + before + " changed to " + in.Voice
	if len(dropped) > 0 {
		label += "; dropped settings " + strings.Join(dropped, ", ")
	}
	ctx.SetLabel(label)
	return nil
}

func addScene(ctx *Context, intent Intent) error {
	in := intent.(*AddScene)
	score, err := validScore(ctx, "adding a scene")
	if err != nil {
		return err
	}
	if err := validNewName(KindScene, in.Name); err != nil {
		return err
	}
	if err := nameFree(score, KindScene, in.Name, "name"); err != nil {
		return err
	}
	updated, err := insertAfterLast(ctx.Source, []string{"scene_decl"}, []string{"song_decl", "arrange_decl"}, "scene "+in.Name+" {}")
	if err != nil {
		return err
	}
	st := newStaged(ctx)
	st.set(ctx.Options.Path, updated)
	if err := resultValid(st, "name", "adding scene "+in.Name); err != nil {
		return err
	}
	st.commit()
	ctx.SetLabel("Scene " + in.Name + " added")
	return nil
}

func duplicateScene(ctx *Context, intent Intent) error {
	in := intent.(*DuplicateScene)
	score, err := validScore(ctx, "duplicating a scene")
	if err != nil {
		return err
	}
	st := newStaged(ctx)
	_, path, err := findEntity(st, KindScene, in.Scene, unknownScene(in.Scene, "scene"))
	if err != nil {
		return err
	}
	if err := validNewName(KindScene, in.Name); err != nil {
		return err
	}
	if err := nameFree(score, KindScene, in.Name, "name"); err != nil {
		return err
	}
	source := st.source(path)
	node, walker, err := declaration(source, []string{"scene_decl"}, in.Scene)
	if err != nil {
		return err
	}
	start, end := int(node.StartByte()), int(node.EndByte())
	name := walker.Field(node, "name")
	clone := string(source[start:name.StartByte()]) + in.Name + string(source[name.EndByte():end])
	newline := Newline(source)
	at := lineAfter(source, end)
	text := clone + newline
	if at == len(source) && (at == 0 || source[at-1] != '\n') {
		text = newline + clone + newline
	}
	st.set(path, replaceMixerSpan(source, at, at, []byte(text)))
	if err := resultValid(st, "name", "duplicating scene "+in.Scene); err != nil {
		return err
	}
	st.commit()
	ctx.SetLabel("Scene " + in.Scene + " duplicated as " + in.Name)
	return nil
}

func deleteScene(ctx *Context, intent Intent) error {
	in := intent.(*DeleteScene)
	score, err := validScore(ctx, "deleting a scene")
	if err != nil {
		return err
	}
	st := newStaged(ctx)
	found, path, err := findEntity(st, KindScene, in.Scene, unknownScene(in.Scene, "scene"))
	if err != nil {
		return err
	}
	isEntry := func(use notation.NameUse) bool { return strings.HasPrefix(use.Use, "song entry ") }
	if blocking := usesOutside(st, found, func(use notation.NameUse) bool { return in.Cascade && isEntry(use) }); len(blocking) > 0 {
		message := fmt.Sprintf("scene %s is still used by %s", in.Scene, strings.Join(blocking, ", "))
		if !in.Cascade {
			message += "; remove those first, or give cascade to remove its song entries with it"
		}
		return &ReasonError{Reason: "scene_in_use", Field: "scene", Uses: blocking, Message: message}
	}
	entries := 0
	for _, file := range found {
		source := st.source(file.path)
		var spans [][2]int
		for _, use := range file.uses {
			if isEntry(use) {
				start, end := innerRemoval(source, int(use.Node.StartByte()), int(use.Node.EndByte()), int(use.Node.Parent().StartByte()))
				spans = append(spans, [2]int{start, end})
				entries++
			}
		}
		if len(spans) > 0 {
			st.set(file.path, removeSpans(source, spans))
		}
	}
	if entries > 0 && entries == len(score.Song) {
		return refuse("song_would_be_empty", "cascade", "every song entry plays scene %s; the song would be left empty", in.Scene)
	}
	updated, err := withoutDeclaration(st.source(path), []string{"scene_decl"}, in.Scene)
	if err != nil {
		return err
	}
	st.set(path, updated)
	if err := resultValid(st, "scene", "deleting scene "+in.Scene); err != nil {
		return err
	}
	if err := checkReseed(st, in.Reseed); err != nil {
		return err
	}
	st.commit()
	label := "Scene " + in.Scene + " deleted"
	if entries > 0 {
		label += fmt.Sprintf(" with %d song entr", entries)
		if entries == 1 {
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
	st := newStaged(ctx)
	found, path, err := findEntity(st, KindPattern, in.Pattern, unknownPattern(in.Pattern, "pattern"))
	if err != nil {
		return err
	}
	if len(score.Patterns) == 1 {
		return refuse("last_pattern", "pattern", "pattern %s is the only pattern, and a score needs one; add another pattern first", in.Pattern)
	}
	isPlacement := func(use notation.NameUse) bool { return strings.HasPrefix(use.Use, "placement ") }
	cascaded := func(use notation.NameUse) bool { return in.Cascade && (use.Role == "binding" || isPlacement(use)) }
	if blocking := usesOutside(st, found, cascaded); len(blocking) > 0 {
		message := fmt.Sprintf("pattern %s is still used by %s", in.Pattern, strings.Join(blocking, ", "))
		if !in.Cascade {
			message += "; remove those first, or give cascade to set its scene bindings to off and remove its placements"
		}
		return &ReasonError{Reason: "pattern_in_use", Field: "pattern", Uses: blocking, Message: message}
	}
	bindings, placements := 0, 0
	for _, file := range found {
		source := st.source(file.path)
		var removals [][2]int
		var offs []Span
		for _, use := range file.uses {
			switch {
			case use.Role == "binding":
				offs = append(offs, Span{use.Start, use.End, "off", file.path})
				bindings++
			case isPlacement(use):
				start, end := innerRemoval(source, int(use.Node.StartByte()), int(use.Node.EndByte()), int(use.Node.Parent().StartByte()))
				removals = append(removals, [2]int{start, end})
				placements++
			}
		}
		if len(offs) == 0 && len(removals) == 0 {
			continue
		}
		// Bindings change in place and placements go: apply both from the end
		// so the offsets hold.
		for _, span := range offs {
			removals = append(removals, [2]int{span.Start, span.End})
		}
		sort.Slice(removals, func(i, j int) bool { return removals[i][0] > removals[j][0] })
		updated := bytes.Clone(source)
		for _, span := range removals {
			text := []byte(nil)
			for _, off := range offs {
				if off.Start == span[0] && off.End == span[1] {
					text = []byte("off")
				}
			}
			updated = replaceMixerSpan(updated, span[0], span[1], text)
		}
		st.set(file.path, updated)
	}
	updated, err := withoutDeclaration(st.source(path), patternTypes, in.Pattern)
	if err != nil {
		return err
	}
	st.set(path, updated)
	if err := resultValid(st, "pattern", "deleting pattern "+in.Pattern); err != nil {
		return err
	}
	if err := checkReseed(st, in.Reseed); err != nil {
		return err
	}
	st.commit()
	label := "Pattern " + in.Pattern + " deleted"
	var parts []string
	if bindings > 0 {
		parts = append(parts, fmt.Sprintf("%d scene binding%s set to off", bindings, plural(bindings)))
	}
	if placements > 0 {
		parts = append(parts, fmt.Sprintf("%d placement%s removed", placements, plural(placements)))
	}
	if len(parts) > 0 {
		label += "; " + strings.Join(parts, ", ")
	}
	ctx.SetLabel(label)
	return nil
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
	st := newStaged(ctx)
	missing := unknownTrack(old, "entity")
	switch kind {
	case KindPattern:
		missing = unknownPattern(old, "entity")
	case KindScene:
		missing = unknownScene(old, "entity")
	}
	found, _, err := findEntity(st, kind, old, missing)
	if err != nil {
		return err
	}
	if in.Name == old {
		return refuse("name_unchanged", "name", "%s %s already has that name", kind, old)
	}
	if err := validNewName(kind, in.Name); err != nil {
		return err
	}
	if err := nameFree(score, kind, in.Name, "name"); err != nil {
		return err
	}
	if kind == KindTrack && old == "master" {
		var paths []string
		for _, file := range found {
			for _, use := range file.uses {
				if use.Role == "path" {
					paths = append(paths, describe(ctx, file.path, use))
				}
			}
		}
		if len(paths) > 0 {
			return &ReasonError{Reason: "name_ambiguous", Field: "entity", Uses: paths, Message: fmt.Sprintf("a track named master shares master.<field> with the master bus, so %s may mean either; rename the track by hand", strings.Join(paths, ", "))}
		}
	}
	references := 0
	for _, file := range found {
		spans := make([]Span, 0, len(file.uses))
		for _, use := range file.uses {
			spans = append(spans, Span{use.Start, use.End, in.Name, file.path})
			if use.Role != "declaration" {
				references++
			}
		}
		updated, err := PatchSpans(st.source(file.path), file.path, spans)
		if err != nil {
			return err
		}
		st.set(file.path, updated)
	}
	ds, err := stagedDiagnostics(st)
	if err != nil {
		return err
	}
	if errs := errorsOf(ds); len(errs) > 0 {
		uses := make([]string, 0, len(errs))
		for _, d := range errs {
			uses = append(uses, diagnosticText(ctx, d))
		}
		return &ReasonError{Reason: "rename_incomplete", Field: "entity", Uses: uses, Message: fmt.Sprintf("renaming %s %s to %s leaves the score invalid: %s", kind, old, in.Name, strings.Join(uses, "; "))}
	}
	st.commit()
	files := ""
	if len(found) > 1 {
		files = fmt.Sprintf(" in %d files", len(found))
	}
	ctx.SetLabel(fmt.Sprintf("%s %s renamed to %s (%d reference%s%s)", strings.ToUpper(kind[:1])+kind[1:], old, in.Name, references, plural(references), files))
	return nil
}

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
