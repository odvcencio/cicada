package main

import (
	"fmt"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"m31labs.dev/cicada/host/instrumentpack"
	"m31labs.dev/cicada/host/sampleasset"
	"m31labs.dev/cicada/kernel/voice/sample"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// samplerZoneCode marks a scored note that no zone of its track's sampler pack
// covers. The engine refuses such a note when it plays, which stops a render
// with fault 29 and a browser host's music.
const samplerZoneCode = "CICADA-SAMPLER-ZONE"

// samplerPackCode marks a pack that does not load, or that cannot play at an
// output rate that check admits.
const samplerPackCode = "CICADA-SAMPLER-PACK"

// samplerMemoryCode marks sampler packs whose manifests declare more decoded
// PCM than the native cap allows, so that render, stems and play refuse the
// score.
const samplerMemoryCode = "CICADA-SAMPLER-MEMORY"

// maxReportedKeyRanges bounds the key ranges that a diagnostic spells out.
const maxReportedKeyRanges = 6

// admittedPack is what check keeps of a sampler pack that loaded: the manifest
// path as the score spells it and the zone table. The audio is gone by the time
// the next pack loads.
type admittedPack struct {
	manifest string
	zones    instrumentpack.ZoneMap
}

// checkPackReport names the velocity law a declared pack plays by:
// "recorded" when its sounding zones share one gain, so its recorded layers
// carry its dynamics, or "velocity/127" when their gains differ.
type checkPackReport struct {
	Sampler     string `json:"sampler"`
	Manifest    string `json:"manifest"`
	VelocityLaw string `json:"velocity_law"`
}

// samplerPackReports lists the packs that loaded, in declaration order.
func samplerPackReports(score *notation.Score, packs map[string]admittedPack) []checkPackReport {
	var reports []checkPackReport
	for _, sampler := range score.Samplers {
		admitted, ok := packs[sampler.Name]
		if !ok {
			continue
		}
		law := "velocity/127"
		if sample.RecordedDynamics(admitted.zones) {
			law = "recorded"
		}
		reports = append(reports, checkPackReport{Sampler: sampler.Name, Manifest: admitted.manifest, VelocityLaw: law})
	}
	return reports
}

// checkSamplerPacks reads every sampler pack that the score declares, as render
// does, and reports each pack that does not load, the packs that render would
// refuse for their size, and each scored note that falls outside every zone of
// its track's pack. Pack paths resolve from the project folder, where assets
// do. limit is the cap on decoded pack PCM, in bytes; zero is the default. It
// returns nothing for a score without pack samplers. Diagnostics are in source
// order.
func checkSamplerPacks(path string, inspection scoreInspection, limit int64) ([]notation.Diagnostic, []checkPackReport) {
	p, score := inspection.semantic, inspection.score
	if p == nil || score == nil {
		return nil, nil
	}
	packs, diagnostics := loadSamplerPacks(inspection.root(path), limit, p.Samplers, score.Samplers)
	diagnostics = append(diagnostics, notesOutsideEveryZone(p, score, packs)...)
	diagnostics = append(diagnostics, kitSamplerZoneDiagnostics(p, score, packs)...)
	diagnostics = appendUniqueDiagnostics(nil, diagnostics)
	sort.SliceStable(diagnostics, func(i, j int) bool {
		a, b := diagnostics[i].Position, diagnostics[j].Position
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Column < b.Column
	})
	return diagnostics, samplerPackReports(score, packs)
}

// loadSamplerPacks admits each distinct pack once, however many samplers share
// it, and returns the zone tables by sampler name. It holds one pack's audio at
// a time. First it reads every manifest and totals the PCM that they declare
// against the cap, the figure that render checks before it decodes anything.
// Then it decodes the packs one after another, to prove that each loads at every
// output rate that a host exports, and keeps only the zone table before it lets
// the audio go. A pack that does not load is reported at the declaration of the
// first sampler that names it.
func loadSamplerPacks(root string, limit int64, samplers []project.Sampler, declared []notation.Sampler) (map[string]admittedPack, []notation.Diagnostic) {
	positions := map[string]notation.Position{}
	for _, sampler := range declared {
		positions[sampler.Name] = sampler.Position
	}
	var diagnostics []notation.Diagnostic
	report := func(code, sampler, message string) {
		diagnostics = append(diagnostics, notation.Diagnostic{Code: code, Severity: "error", Position: positions[sampler], Message: message})
	}
	tally, err := instrumentpack.NewResidentCap(limit)
	if err != nil {
		return nil, []notation.Diagnostic{{Code: "CICADA-PARAM", Severity: "error", Message: err.Error(), Position: notation.Position{Line: 1, Column: 1}}}
	}

	type pin struct{ manifest, sha256 string }
	type opened struct {
		pack  *instrumentpack.Pack // nil when the manifest did not load
		first string               // the first sampler that names the pack
	}
	byPin := map[pin]*opened{}
	var order []pin
	samplerPack := map[string]*opened{}
	refused := false
	for _, sampler := range samplers {
		if sampler.Pack == "" {
			continue
		}
		key := pin{sampler.Pack, sampler.SHA256}
		if byPin[key] != nil {
			samplerPack[sampler.Name] = byPin[key]
			continue
		}
		entry := &opened{first: sampler.Name}
		byPin[key], samplerPack[sampler.Name] = entry, entry
		order = append(order, key)
		pack, err := instrumentpack.Open(root, sampler.Pack, sampler.SHA256)
		if err != nil {
			report(samplerPackCode, sampler.Name, fmt.Sprintf("sampler %s: %v", sampler.Name, err))
			continue
		}
		entry.pack = pack
		if refused {
			continue
		}
		// Render counts the same figure, in the same order, before it reads audio.
		if err = tally.Reserve(pack); err != nil {
			refused = true
			report(samplerMemoryCode, sampler.Name, fmt.Sprintf("%v; render, stems and play refuse this score", err))
		}
	}

	zones := map[*opened]instrumentpack.ZoneMap{}
	for _, key := range order {
		entry := byPin[key]
		if entry.pack == nil {
			continue
		}
		table, err := decodeZoneMap(entry.pack)
		if err != nil {
			report(samplerPackCode, entry.first, fmt.Sprintf("sampler %s: %v", entry.first, err))
		} else {
			zones[entry] = table
		}
		// The audio of this pack is garbage now. Collect it before the next pack
		// allocates, so that the check holds one pack at a time and not the sum.
		runtime.GC()
	}

	packs := map[string]admittedPack{}
	for name, entry := range samplerPack {
		if table, ok := zones[entry]; ok {
			packs[name] = admittedPack{manifest: entry.pack.Path(), zones: table}
		}
	}
	return packs, diagnostics
}

// decodeZoneMap decodes the pack to prove that it loads, admits it at every
// output rate that a host exports, and returns its zone table without the audio.
func decodeZoneMap(pack *instrumentpack.Pack) (instrumentpack.ZoneMap, error) {
	prepared, err := pack.DecodeUnadmitted()
	if err != nil {
		return nil, err
	}
	for _, rate := range portableRates {
		if err = prepared.Admit(rate); err != nil {
			return nil, fmt.Errorf("at %d Hz: %w", rate, err)
		}
	}
	return prepared.ZoneMap(), nil
}

// notesOutsideEveryZone lists the notes that tracks of pack samplers would play
// and the pack cannot. It reads the lowered patterns in each track's slots, so
// it sees what the engine plays: the pattern's transpose is applied, a tie
// starts no note, and every pattern a scene, placement, chain or live stinger
// plays on the track is covered. The source steps supply the position; a step
// that a phrase use supplies is reported at the use, because the use may be what
// moved it out of the pack's keys.
func notesOutsideEveryZone(p *project.Project, score *notation.Score, packs map[string]admittedPack) []notation.Diagnostic {
	steps := newStepSources(score)
	patterns := map[string]project.Pattern{}
	for _, pattern := range p.Patterns {
		patterns[pattern.ID] = pattern
	}
	var diagnostics []notation.Diagnostic
	for _, track := range p.Tracks {
		admitted, ok := packs[track.Kind]
		if !ok {
			continue
		}
		for _, id := range track.Slots {
			if id == nil {
				continue
			}
			pattern := patterns[*id]
			for i, step := range pattern.Data {
				// A rest, a tie and a zero velocity start no sound.
				if step == nil || step.Tie || step.Velocity == 0 {
					continue
				}
				pitches := step.Notes
				if len(pitches) == 0 {
					pitches = []int{int(step.Note)}
				}
				for n, pitch := range pitches {
					note := pitch + int(pattern.Transpose)
					if note < 0 || note > 127 {
						continue
					}
					gap := humanizedGap(admitted.zones, uint8(note), step.Velocity, pattern.Humanize)
					if gap == 0 {
						continue
					}
					position, written, use := steps.locate(*id, i, n, len(step.Notes))
					message := outOfZoneMessage(admitted, track, *id, use, written, note, gap)
					if gap != step.Velocity {
						message += fmt.Sprintf("; the step is written at velocity %d and humanize = %d can move it to %d", step.Velocity, pattern.Humanize, gap)
					}
					diagnostics = append(diagnostics, notation.Diagnostic{
						Code: samplerZoneCode, Severity: "error", Position: position,
						Message: message,
					})
				}
			}
		}
	}
	return diagnostics
}

// humanizedGap returns a velocity that a hit written at velocity can play
// under pattern humanize but no attack zone of note covers, or 0 when every
// such velocity is covered. Humanize moves a velocity by at most humanize
// either way, within 1..127 (seq.HumanizeVelocity).
func humanizedGap(zones instrumentpack.ZoneMap, note, velocity, humanize uint8) uint8 {
	if !zones.HasAttackZone(note, velocity) {
		return velocity
	}
	for v := max(1, int(velocity)-int(humanize)); v <= min(127, int(velocity)+int(humanize)); v++ {
		if !zones.HasAttackZone(note, uint8(v)) {
			return uint8(v)
		}
	}
	return 0
}

// stepPhraseUses lines up the steps of a pattern after its phrases expand with
// the phrase uses that supplied them: nil for a step written in the pattern.
// It follows notation's expansion, which this check reads after the score has
// parsed without errors.
func stepPhraseUses(phrases map[string]notation.Phrase, pattern notation.Pattern) []*notation.PhraseUse {
	var uses []*notation.PhraseUse
	for _, part := range pattern.Parts {
		switch {
		case part.Step != nil:
			uses = append(uses, nil)
		case part.Use != nil:
			for range part.Use.Repeat {
				for range phrases[part.Use.Name].Steps {
					uses = append(uses, part.Use)
				}
			}
		}
	}
	if len(uses) != len(pattern.Steps) {
		return nil
	}
	return uses
}

// stepSources finds where the steps of the lowered patterns were written.
type stepSources struct {
	sources map[string]notation.Pattern
	phrases map[string]notation.Phrase
	uses    map[string][]*notation.PhraseUse
}

func newStepSources(score *notation.Score) *stepSources {
	s := &stepSources{sources: map[string]notation.Pattern{}, phrases: map[string]notation.Phrase{}, uses: map[string][]*notation.PhraseUse{}}
	for _, pattern := range score.Patterns {
		s.sources[pattern.Name] = pattern
	}
	for _, phrase := range score.Phrases {
		s.phrases[phrase.Name] = phrase
	}
	return s
}

// locate returns the source position and text of step index of pattern and,
// when a phrase use supplied the step, that use, whose position it returns
// instead: the use may be what moved the step's pitch. For pitch chord of a
// chord of size pitches it returns that pitch's word, not the whole chord.
// A step it cannot find is placed at the pattern, with no text.
func (s *stepSources) locate(pattern string, index, chord, pitches int) (notation.Position, string, *notation.PhraseUse) {
	source := s.sources[pattern]
	if index >= len(source.Steps) {
		return source.Position, "", nil
	}
	uses, ok := s.uses[pattern]
	if !ok {
		uses = stepPhraseUses(s.phrases, source)
		s.uses[pattern] = uses
	}
	position, written := source.Steps[index].Position, strings.TrimSpace(source.Steps[index].Text)
	var use *notation.PhraseUse
	if index < len(uses) && uses[index] != nil {
		use = uses[index]
		position = use.Position
	}
	if pitches > 0 {
		// Name the chord pitch, not the whole chord.
		if words := strings.Fields(strings.Trim(written, "[]")); len(words) == pitches {
			written = words[chord]
			if cut := strings.IndexAny(written, "^?!~"); cut > 0 {
				written = written[:cut]
			}
		}
	}
	return position, written, use
}

// samplerRangeCode marks a note that a single-asset sampler cannot play: the
// sample voice cannot pitch the sampler's asset that far from its root at an
// output rate that a host plays at (44.1, 48 or 96 kHz). Render at such a rate
// refuses the score before it writes audio, and the engine faults when it
// plays the note.
const samplerRangeCode = "CICADA-SAMPLER-RANGE"

// samplerRangeDiagnostics reports each note that a track of a single-asset
// sampler plays and that the sample voice cannot play at one of
// sampleasset.OutputRates, at the note's source position: an error when the
// note fails at 48 kHz, where render renders by default and Studio checks
// edits, and a warning when it fails only at 44.1 or 96 kHz, where render
// --rate and a host at that rate refuse it. It reads only the score: the
// asset's declared rate, the sampler's root and the notes.
func samplerRangeDiagnostics(p *project.Project, score *notation.Score) []notation.Diagnostic {
	if p == nil || score == nil {
		return nil
	}
	findings := sampleasset.SamplerRangeFindings(p, sampleasset.OutputRates)
	if len(findings) == 0 {
		return nil
	}
	steps := newStepSources(score)
	patterns := map[string]project.Pattern{}
	for _, pattern := range p.Patterns {
		patterns[pattern.ID] = pattern
	}
	diagnostics := make([]notation.Diagnostic, 0, len(findings))
	for _, finding := range findings {
		// A performed note has no step; it is reported at its pattern.
		position, written, use := steps.sources[finding.Pattern].Position, "", (*notation.PhraseUse)(nil)
		if !finding.Performed {
			position, written, use = steps.locate(finding.Pattern, finding.Step, finding.Chord, len(patterns[finding.Pattern].Data[finding.Step].Notes))
		}
		diagnostics = append(diagnostics, notation.Diagnostic{
			Code: samplerRangeCode, Severity: rangeSeverity(finding.Rates), Position: position,
			Message:  samplerRangeMessage(finding, written, use),
			Expected: samplerRangeExpected(finding),
			Found:    fmt.Sprintf("MIDI %d", finding.Note),
		})
	}
	return diagnostics
}

// samplerRangeExpected is the range a finding's note must fall in, as
// Diagnostic.Expected spells it.
func samplerRangeExpected(finding sampleasset.RangeFinding) string {
	if finding.Low > finding.High {
		return "no MIDI note: the asset plays at no note at every output rate"
	}
	return fmt.Sprintf("MIDI %d to %d", finding.Low, finding.High)
}

// samplerRangeMessage names the note, its track and pattern, the sampler, its
// asset and root, the notes the sampler does play, and the output rates and
// playback ratio at which this one fails.
func samplerRangeMessage(finding sampleasset.RangeFinding, written string, use *notation.PhraseUse) string {
	pitch := fmt.Sprintf("MIDI %d", finding.Note)
	switch {
	case finding.Performed:
		pitch = fmt.Sprintf("performed note %d (MIDI %d)", finding.Step+1, finding.Note)
	case written != "":
		pitch = fmt.Sprintf("pitch %s (MIDI %d)", written, finding.Note)
	}
	where := fmt.Sprintf("on track %s, pattern %s", finding.Track, finding.Pattern)
	if use != nil {
		where += ", from phrase " + use.Name
		if use.Transpose != 0 {
			where += fmt.Sprintf(" used with transpose %+d", use.Transpose)
		}
	}
	plays := "no note at every output rate"
	if finding.Low <= finding.High {
		plays = fmt.Sprintf("MIDI %d to %d (%s to %s) at 44.1, 48 and 96 kHz", finding.Low, finding.High, midiPitchName(finding.Low), midiPitchName(finding.High))
	}
	var fails []string
	for i, rate := range finding.Rates {
		fails = append(fails, fmt.Sprintf("%.3g at %s kHz", finding.Ratios[i], strconv.FormatFloat(float64(rate)/1000, 'f', -1, 64)))
	}
	return fmt.Sprintf("%s %s, is out of range for sampler %s: with root %s (MIDI %d), its %d Hz asset %s plays %s; this note needs playback ratio %s, and the sample voice plays 0.125 to 8",
		pitch, where, finding.Sampler, midiPitchName(finding.Root), finding.Root, finding.AssetRate, finding.Asset, plays, strings.Join(fails, ", "))
}

// midiPitchName spells a MIDI note as the score writes it: c4 is MIDI 60.
func midiPitchName(note int) string {
	names := [...]string{"c", "c#", "d", "d#", "e", "f", "f#", "g", "g#", "a", "a#", "b"}
	return names[note%12] + strconv.Itoa(note/12-1)
}

// outOfZoneMessage names the note, its track and pattern, the pack and the keys
// that the pack does cover. A note whose key has a zone but none at its velocity
// says so. A note that a phrase use supplies names the phrase and the use's
// transpose, since the written pitch alone would not explain the key.
func outOfZoneMessage(admitted admittedPack, track project.Track, pattern string, use *notation.PhraseUse, written string, note int, velocity uint8) string {
	pitch := "pitch " + strconv.Itoa(note)
	if written != "" {
		pitch = fmt.Sprintf("pitch %s (MIDI %d)", written, note)
	}
	for _, zone := range admitted.zones {
		if !zone.Release && note >= int(zone.KeyLow) && note <= int(zone.KeyHigh) {
			pitch += fmt.Sprintf(" at velocity %d", velocity)
			break
		}
	}
	where := fmt.Sprintf("on track %s, pattern %s", track.ID, pattern)
	if use != nil {
		where += ", from phrase " + use.Name
		if use.Transpose != 0 {
			where += fmt.Sprintf(" used with transpose %+d", use.Transpose)
		}
	}
	return fmt.Sprintf("%s %s, is outside every zone of the pack for sampler %s (%s, which covers MIDI %s)",
		pitch, where, track.Kind, admitted.manifest, attackKeyRanges(admitted.zones))
}

// attackKeyRanges spells the keys that some attack zone of the pack covers, as
// ranges such as "23-66" or "22-26, 29-38".
func attackKeyRanges(zones instrumentpack.ZoneMap) string {
	var covered [128]bool
	for _, zone := range zones {
		if zone.Release {
			continue
		}
		for key := int(zone.KeyLow); key <= int(zone.KeyHigh) && key < len(covered); key++ {
			covered[key] = true
		}
	}
	var ranges []string
	for key := 0; key < len(covered); key++ {
		if !covered[key] {
			continue
		}
		end := key
		for end+1 < len(covered) && covered[end+1] {
			end++
		}
		if end == key {
			ranges = append(ranges, strconv.Itoa(key))
		} else {
			ranges = append(ranges, fmt.Sprintf("%d-%d", key, end))
		}
		key = end
	}
	if len(ranges) == 0 {
		return "no keys"
	}
	if len(ranges) > maxReportedKeyRanges {
		ranges = append(ranges[:maxReportedKeyRanges], "...")
	}
	return strings.Join(ranges, ", ")
}
