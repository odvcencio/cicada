package edit

import (
	"fmt"
	"maps"
	"sort"
	"strings"

	"m31labs.dev/cicada/notation"
)

// staged holds a structural edit's new bytes for each source file it
// changes: the entry (under ctx.Options.Path) and other files of the
// project. Library files are never staged.
type staged struct {
	ctx   *Context
	files map[string][]byte
}

func newStaged(ctx *Context) *staged { return &staged{ctx: ctx, files: map[string][]byte{}} }

// source returns a file's bytes as the edit has left them so far.
func (s *staged) source(path string) []byte {
	if data, ok := s.files[path]; ok {
		return data
	}
	data, _ := s.ctx.FileSource(path)
	return data
}

func (s *staged) set(path string, data []byte) { s.files[path] = data }

func (s *staged) entry() []byte { return s.source(s.ctx.Options.Path) }

// overrides are the files a compile of the staged edit reads beside the entry.
func (s *staged) overrides() map[string][]byte {
	files := s.ctx.fileOverrides()
	for path, data := range s.files {
		if path != s.ctx.Options.Path {
			files[path] = data
		}
	}
	return files
}

// commit writes the staged bytes into the context: the entry as its source,
// other files as staged files of the batch.
func (s *staged) commit() {
	paths := make([]string, 0, len(s.files))
	for path := range s.files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if path == s.ctx.Options.Path {
			s.ctx.Source = s.files[path]
			continue
		}
		before, _ := s.ctx.FileSource(path)
		s.ctx.AddFile(File{Path: path, Before: before, After: s.files[path]})
	}
	s.ctx.plan = nil
}

// sourceFile is one source of the project, the entry first.
type sourceFile struct {
	path    string
	library bool
}

func projectFiles(ctx *Context) []sourceFile {
	files := []sourceFile{{path: ctx.Options.Path}}
	for _, file := range ctx.Options.Sources {
		if file.Path != ctx.Options.Path {
			files = append(files, sourceFile{path: file.Path, library: file.Library != ""})
		}
	}
	return files
}

// fileUses is one source file's uses of a name.
type fileUses struct {
	sourceFile
	uses []notation.NameUse
}

// projectUses finds a track's, a pattern's or a scene's declaration and uses
// in every source file of the project, as the staged edit has left them.
// Library files cannot name a project's tracks, patterns or scenes, and an
// entity they declare has a qualified name, so they are left out.
func projectUses(st *staged, kind, name string) ([]fileUses, error) {
	var found []fileUses
	for _, file := range projectFiles(st.ctx) {
		if file.library {
			continue
		}
		all, err := notation.NameUses(st.source(file.path))
		if err != nil {
			return nil, err
		}
		if uses := notation.UsesOf(all, kind, name); len(uses) > 0 {
			found = append(found, fileUses{sourceFile: file, uses: uses})
		}
	}
	return found, nil
}

// declarationIn names the file that declares the entity among found.
func declarationIn(found []fileUses) (string, bool) {
	for _, file := range found {
		for _, use := range file.uses {
			if use.Role == "declaration" {
				return file.path, true
			}
		}
	}
	return "", false
}

// describe names a use for a refusal, with its file when it is not the entry.
func describe(ctx *Context, path string, use notation.NameUse) string {
	if path == ctx.Options.Path {
		return use.Use
	}
	return strings.Replace(use.Use, fmt.Sprintf("(line %d)", use.Line), fmt.Sprintf("(%s line %d)", path, use.Line), 1)
}

// diagnosticText names a diagnostic for a refusal: its message without the
// code, its file when it is not the entry, and its line.
func diagnosticText(ctx *Context, d notation.Diagnostic) string {
	switch {
	case d.Position.Line == 0:
		return d.Text()
	case d.Position.File != "" && d.Position.File != ctx.Options.Path:
		return fmt.Sprintf("%s (%s line %d)", d.Text(), d.Position.File, d.Position.Line)
	}
	return fmt.Sprintf("%s (line %d)", d.Text(), d.Position.Line)
}

// stagedDiagnostics returns the project's diagnostics with the staged bytes:
// with the host's Diagnose hook every one cicada check gives, else the parse
// diagnostics and, when they have no error, a compile error.
func stagedDiagnostics(st *staged) ([]notation.Diagnostic, error) {
	ctx := st.ctx
	if ctx.Options.Diagnose != nil {
		return ctx.Options.Diagnose(st.entry(), st.overrides())
	}
	working := *ctx
	working.Source, working.plan = st.entry(), nil
	working.files = append([]File(nil), ctx.files...)
	working.fileIndex = maps.Clone(ctx.fileIndex)
	for path, data := range st.files {
		if path != ctx.Options.Path {
			before, _ := ctx.FileSource(path)
			working.AddFile(File{Path: path, Before: before, After: data})
		}
	}
	_, ds, err := working.ParseProject()
	if err != nil {
		return nil, err
	}
	if !hasErrors(ds) {
		if _, err := ctx.Options.Compiler.Compile(st.entry(), st.overrides()); err != nil {
			ds = append(ds, notation.Diagnostic{Severity: "error", Message: err.Error()})
		}
	}
	return ds, nil
}

func errorsOf(ds []notation.Diagnostic) []notation.Diagnostic {
	var errs []notation.Diagnostic
	for _, d := range ds {
		if d.Severity == "error" {
			errs = append(errs, d)
		}
	}
	return errs
}

// resultValid refuses a structural edit whose staged result would not
// validate, with reason result_invalid and every problem in uses.
func resultValid(st *staged, field, what string) error {
	ds, err := stagedDiagnostics(st)
	if err != nil {
		return err
	}
	errs := errorsOf(ds)
	if len(errs) == 0 {
		return nil
	}
	uses := make([]string, 0, len(errs))
	for _, d := range errs {
		uses = append(uses, diagnosticText(st.ctx, d))
	}
	return &ReasonError{Reason: "result_invalid", Field: field, Uses: uses, Message: fmt.Sprintf("%s leaves the score invalid: %s", what, strings.Join(uses, "; "))}
}

// checkReseed compares the compiled plan before and after a staged edit.
// The engine seeds chance (x?50) and humanize by a track's position and a
// pattern's slot on its track (kernel/seq/schedule.go, ProbabilityHit and
// HumanizeVelocity), so an edit that moves a track, or a pattern's slot,
// of a track that plays such a pattern changes those random choices. Without
// reseed the edit is refused with reason reseeds_random_choices and the
// tracks and patterns in uses; with reseed it goes ahead and the result's
// warnings name them.
func checkReseed(st *staged, reseed bool) error {
	ctx := st.ctx
	before, err := ctx.CurrentPlan()
	if err != nil || before == nil {
		return nil
	}
	after, err := ctx.Options.Compiler.Compile(st.entry(), st.overrides())
	if err != nil || after == nil {
		return nil
	}
	score, _, _ := ctx.ParseProject()
	seeded := seededPatterns(after, score)
	beforeIndex := map[string]int{}
	for i, track := range before.Tracks {
		beforeIndex[track.ID] = i
	}
	var moved []string
	for i, track := range after.Tracks {
		j, ok := beforeIndex[track.ID]
		if !ok {
			continue
		}
		for slot, pattern := range track.Slots {
			if pattern == nil || !seeded[*pattern] {
				continue
			}
			old := -1
			for k, earlier := range before.Tracks[j].Slots {
				if earlier != nil && *earlier == *pattern {
					old = k
				}
			}
			switch {
			case i != j:
				moved = append(moved, fmt.Sprintf("track %s, pattern %s: the track moves from position %d to %d", track.ID, *pattern, j+1, i+1))
			case old >= 0 && old != slot:
				moved = append(moved, fmt.Sprintf("track %s, pattern %s: the pattern moves from slot %d to %d", track.ID, *pattern, old+1, slot+1))
			}
		}
	}
	if len(moved) == 0 {
		return nil
	}
	if !reseed {
		return &ReasonError{Reason: "reseeds_random_choices", Field: "reseed", Uses: moved, Message: fmt.Sprintf("this edit changes the random choices (chance and humanize) of %s, because the engine seeds them by track position and pattern slot; give reseed to accept that", strings.Join(moved, "; "))}
	}
	for _, change := range moved {
		ctx.warn("random choices (chance and humanize) change: %s", change)
	}
	return nil
}

// seededPatterns are the patterns whose playing depends on a seed: a step
// with a chance between 0 and 100, or humanize.
func seededPatterns(plan *Plan, score *notation.Score) map[string]bool {
	seeded := map[string]bool{}
	chance := func(steps []*Step) bool {
		for _, step := range steps {
			if step != nil && step.Probability > 0 && step.Probability < 100 {
				return true
			}
		}
		return false
	}
	for _, pattern := range plan.Patterns {
		if chance(pattern.Data) {
			seeded[pattern.ID] = true
		}
		for _, lane := range pattern.Lanes {
			if chance(lane) {
				seeded[pattern.ID] = true
			}
		}
	}
	if score != nil {
		for _, pattern := range score.Patterns {
			for _, attr := range pattern.Attrs {
				if attr.Name == "humanize" && strings.TrimSpace(attr.Value) != "0" && strings.TrimSpace(attr.Value) != "0%" {
					seeded[pattern.Name] = true
				}
			}
		}
	}
	return seeded
}

// patternsOf lists, in source order without repeats, the patterns a track
// plays: its scene bindings, its chain, its placements, its live stingers and
// its MIDI launch-slot mappings.
func patternsOf(score *notation.Score, track string) []string {
	var names []string
	add := func(name string) {
		if name != "" && !containsString(names, name) {
			if _, ok := findPattern(score, name); ok {
				names = append(names, name)
			}
		}
	}
	for _, scene := range score.Scenes {
		for _, binding := range scene.Bindings {
			if binding.Track == track {
				add(binding.Pattern)
			}
		}
	}
	for _, t := range score.Tracks {
		if t.Name == track {
			for _, token := range t.Chain {
				add(token.Text)
			}
		}
	}
	if score.Arrange != nil {
		for _, placement := range score.Arrange.Placements {
			if placement.Track == track {
				add(placement.Content)
			}
		}
	}
	if score.Live != nil {
		for _, stinger := range score.Live.Stingers {
			if stinger.Track == track {
				add(stinger.Pattern)
			}
		}
	}
	if score.MIDI != nil {
		for _, mapping := range score.MIDI.Mappings {
			if fields := strings.Fields(mapping.Target); len(fields) == 3 && fields[0] == "launch-slot" && fields[1] == track {
				add(fields[2])
			}
		}
	}
	return names
}

// trackSetting is a setting of a track's declaration, in the declaring file.
type trackSetting struct {
	name       string
	start, end int
	line       int
}

// voiceChange is what changing a track's voice would do.
type voiceChange struct {
	voiceInvalid string         // the new voice is not one the score can play: why
	unsupported  []trackSetting // the track's own settings the new voice lacks
	invalid      []string       // problems the language refuses
	changes      []string       // changes to the sound with no error
}

// checkVoiceChange compares the project before and after the staged voice
// change of track (declared in path, whose declaration starts at declStart
// and names the voice up to kindEnd, with the track's settings) and lists
// every problem: the diagnostics the change adds (with the host's Diagnose
// hook, all of cicada check's), every parameter path of the track the new
// voice does not take, the drum lanes of its patterns the new voice does not
// play, and the rows and marks (notation.VoiceReads) the old voice played and
// the new one ignores. Without diagnose the diagnostics are left to
// the compile that ends the batch, for a voice from a library the batch is
// importing, whose declarations the parse does not have yet.
func checkVoiceChange(st *staged, score *notation.Score, track, oldKind, newKind, path string, declStart, kindEnd int, settings []trackSetting, diagnose bool) (voiceChange, error) {
	ctx := st.ctx
	var change voiceChange
	var before, after []notation.Diagnostic
	if diagnose {
		var err error
		if before, err = stagedDiagnostics(newStaged(ctx)); err != nil {
			return change, err
		}
		if after, err = stagedDiagnostics(st); err != nil {
			return change, err
		}
	}
	seen := map[string]int{}
	for _, d := range before {
		seen[d.Severity+"|"+d.Code+"|"+d.Text()]++
	}
	source := st.source(path)
	inFile := func(d notation.Diagnostic) bool {
		return d.Position.File == "" || d.Position.File == path
	}
	lines := map[string]bool{} // file and line of the problems already listed
	// Parameter paths first: validation stops at some of them, and each
	// one the new voice lacks is a problem.
	found, err := projectUses(st, KindTrack, track)
	if err != nil {
		return change, err
	}
	for _, file := range found {
		for _, use := range file.uses {
			if use.Role != "path" {
				continue
			}
			field, _, _ := strings.Cut(strings.TrimPrefix(use.Path, track+"."), ".")
			// A voice the score does not hold yet (a library this batch
			// imports) is left to the compile that ends the batch.
			if !notation.VoiceDeclared(score, newKind) || notation.TrackParamKnown(score, newKind, field) {
				continue
			}
			change.invalid = append(change.invalid, fmt.Sprintf("%s: %s has no setting %s", describe(ctx, file.path, use), newKind, field))
			lines[fmt.Sprintf("%s:%d", file.path, use.Line)] = true
		}
	}
	for _, d := range after {
		key := d.Severity + "|" + d.Code + "|" + d.Text()
		if seen[key] > 0 {
			seen[key]--
			continue
		}
		if inFile(d) && d.Position.Line > 0 {
			if at, err := Offset(source, d.Position.File, d.Position); err == nil {
				// An error at the declaration's head is about the voice itself.
				if d.Severity == "error" && at >= declStart && at <= kindEnd {
					if change.voiceInvalid == "" {
						change.voiceInvalid = d.Text()
					}
					continue
				}
				matched := false
				for _, setting := range settings {
					if setting.start <= at && at < setting.end {
						matched = true
						if !containsSetting(change.unsupported, setting) {
							change.unsupported = append(change.unsupported, setting)
						}
					}
				}
				if matched {
					continue
				}
			}
		}
		file := d.Position.File
		if file == "" {
			file = ctx.Options.Path
		}
		if lines[fmt.Sprintf("%s:%d", file, d.Position.Line)] {
			continue
		}
		if d.Severity == "error" {
			change.invalid = append(change.invalid, diagnosticText(ctx, d))
		} else {
			change.changes = append(change.changes, diagnosticText(ctx, d))
		}
	}
	// Changes the language accepts but that change what plays: drum lanes the
	// new voice does not play, and rows and marks it ignores that the old
	// voice played (notation.VoiceReads, checked against renders).
	oldLanes, newLanes := notation.VoiceLanes(score, oldKind), notation.VoiceLanes(score, newKind)
	oldReads, newReads := notation.VoiceReads(score, oldKind), notation.VoiceReads(score, newKind)
	for _, name := range patternsOf(score, track) {
		pattern, ok := findPattern(score, name)
		if !ok {
			continue
		}
		if pattern.Kind == "drums" {
			if newLanes == nil {
				continue
			}
			for _, lane := range pattern.Lanes {
				if containsString(newLanes, lane.Name) || !laneHasHits(lane) || oldLanes != nil && !containsString(oldLanes, lane.Name) {
					continue
				}
				change.changes = append(change.changes, fmt.Sprintf("pattern %s lane %s (line %d): %s does not play lane %s, so its hits go silent", name, lane.Name, lane.Position.Line, newKind, lane.Name))
			}
			continue
		}
		if newReads == nil {
			continue
		}
		for _, input := range notation.PatternInputs(pattern) {
			read, described := newReads[input]
			switch {
			case !described && !diagnose:
				change.invalid = append(change.invalid, fmt.Sprintf("pattern %s (line %d): %s refuses the %s rows or marks", name, pattern.Position.Line, newKind, input))
			case described && !read && (oldReads == nil || oldReads[input]):
				change.changes = append(change.changes, fmt.Sprintf("pattern %s (line %d): %s no effect on %s", name, pattern.Position.Line, inputName(input), newKind))
			}
		}
	}
	return change, nil
}

// inputName names a row or mark for a message, with its verb.
func inputName(input string) string {
	switch input {
	case notation.InputAccent, notation.InputSlide, notation.InputRatchet, notation.InputChance:
		return "its " + input + " marks have"
	}
	return "its " + input + " row has"
}

// findPattern finds a pattern by the name a track binds, an import alias's
// name included.
func findPattern(score *notation.Score, name string) (notation.Pattern, bool) {
	resolved := notation.ResolveVoice(score, name)
	for _, pattern := range score.Patterns {
		if pattern.Name == name || pattern.Name == resolved {
			return pattern, true
		}
	}
	return notation.Pattern{}, false
}

func containsSetting(settings []trackSetting, setting trackSetting) bool {
	for _, s := range settings {
		if s.start == setting.start {
			return true
		}
	}
	return false
}

func laneHasHits(lane notation.Lane) bool {
	for _, hit := range lane.Hits {
		if text := strings.TrimSpace(hit.Text); text != "" && text != "." && text != "|" {
			return true
		}
	}
	return false
}
