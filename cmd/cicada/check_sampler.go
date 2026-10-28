package main

import (
	"fmt"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"m31labs.dev/cicada/host/instrumentpack"
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

// checkSamplerPacks reads every sampler pack that the score declares, as render
// does, and reports each pack that does not load, the packs that render would
// refuse for their size, and each scored note that falls outside every zone of
// its track's pack. Pack paths resolve from the project folder, where assets
// do. limit is the cap on decoded pack PCM, in bytes; zero is the default. It
// returns nothing for a score without pack samplers. Diagnostics are in source
// order.
func checkSamplerPacks(path string, inspection scoreInspection, limit int64) []notation.Diagnostic {
	p, score := inspection.semantic, inspection.score
	if p == nil || score == nil {
		return nil
	}
	packs, diagnostics := loadSamplerPacks(inspection.root(path), limit, p.Samplers, score.Samplers)
	diagnostics = append(diagnostics, notesOutsideEveryZone(p, score, packs)...)
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
	return diagnostics
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
	sources := map[string]notation.Pattern{}
	for _, pattern := range score.Patterns {
		sources[pattern.Name] = pattern
	}
	phrases := map[string]notation.Phrase{}
	for _, phrase := range score.Phrases {
		phrases[phrase.Name] = phrase
	}
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
			pattern, source := patterns[*id], sources[*id]
			uses := stepPhraseUses(phrases, source)
			for i, step := range pattern.Data {
				// A rest, a tie and a zero velocity start no sound.
				if step == nil || step.Tie || step.Velocity == 0 {
					continue
				}
				note := int(step.Note) + int(pattern.Transpose)
				if note < 0 || note > 127 || admitted.zones.HasAttackZone(uint8(note), step.Velocity) {
					continue
				}
				position, written := source.Position, ""
				var use *notation.PhraseUse
				if i < len(source.Steps) {
					position, written = source.Steps[i].Position, strings.TrimSpace(source.Steps[i].Text)
					if i < len(uses) && uses[i] != nil {
						use = uses[i]
						position = use.Position
					}
				}
				diagnostics = append(diagnostics, notation.Diagnostic{
					Code: samplerZoneCode, Severity: "error", Position: position,
					Message: outOfZoneMessage(admitted, track, *id, use, written, note, step.Velocity),
				})
			}
		}
	}
	return diagnostics
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
