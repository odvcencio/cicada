package main

import (
	"fmt"
	"path/filepath"
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

// packCheckRate is the output rate at which check admits a pack. It is the rate
// that render uses unless --rate says otherwise.
const packCheckRate = 48_000

// maxReportedKeyRanges bounds the key ranges that a diagnostic spells out.
const maxReportedKeyRanges = 6

// admittedPack is a sampler pack that check loaded, with the manifest path as
// the score spells it.
type admittedPack struct {
	manifest string
	pack     *instrumentpack.Prepared
}

// checkSamplerPacks loads every sampler pack that the score declares, as render
// does, and reports each pack that does not load and each scored note that falls
// outside every zone of its track's pack. It returns nothing for a score without
// pack samplers. Diagnostics are in source order.
func checkSamplerPacks(path string, inspection scoreInspection) []notation.Diagnostic {
	p, score := inspection.semantic, inspection.score
	if p == nil || score == nil {
		return nil
	}
	packs, diagnostics := loadSamplerPacks(filepath.Dir(path), p.Samplers, score.Samplers)
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
// it, and returns the packs by sampler name. A pack that does not load is
// reported at the declaration of the first sampler that names it.
func loadSamplerPacks(root string, samplers []project.Sampler, declared []notation.Sampler) (map[string]admittedPack, []notation.Diagnostic) {
	positions := map[string]notation.Position{}
	for _, sampler := range declared {
		positions[sampler.Name] = sampler.Position
	}
	type pin struct{ manifest, sha256 string }
	loaded, failed := map[pin]*instrumentpack.Prepared{}, map[pin]bool{}
	packs := map[string]admittedPack{}
	var diagnostics []notation.Diagnostic
	for _, sampler := range samplers {
		key := pin{sampler.Pack, sampler.SHA256}
		if sampler.Pack == "" || failed[key] {
			continue
		}
		if loaded[key] == nil {
			prepared, err := instrumentpack.LoadAtRate(root, sampler.Pack, sampler.SHA256, packCheckRate)
			if err != nil {
				failed[key] = true
				diagnostics = append(diagnostics, notation.Diagnostic{
					Code: "CICADA-SAMPLER-PACK", Severity: "error", Position: positions[sampler.Name],
					Message: fmt.Sprintf("sampler %s: %v", sampler.Name, err),
				})
				continue
			}
			loaded[key] = prepared
		}
		packs[sampler.Name] = admittedPack{manifest: sampler.Pack, pack: loaded[key]}
	}
	return packs, diagnostics
}

// notesOutsideEveryZone lists the notes that tracks of pack samplers would play
// and the pack cannot. It reads the lowered patterns in each track's slots, so
// it sees what the engine plays: the pattern's transpose is applied, a tie
// starts no note, and every pattern a scene, placement, chain or live stinger
// plays on the track is covered. The source steps supply the position.
func notesOutsideEveryZone(p *project.Project, score *notation.Score, packs map[string]admittedPack) []notation.Diagnostic {
	sources := map[string]notation.Pattern{}
	for _, pattern := range score.Patterns {
		sources[pattern.Name] = pattern
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
			for i, step := range pattern.Data {
				// A rest, a tie and a zero velocity start no sound.
				if step == nil || step.Tie || step.Velocity == 0 {
					continue
				}
				note := int(step.Note) + int(pattern.Transpose)
				if note < 0 || note > 127 || admitted.pack.HasAttackZone(uint8(note), step.Velocity) {
					continue
				}
				position, written := source.Position, ""
				if i < len(source.Steps) {
					position, written = source.Steps[i].Position, strings.TrimSpace(source.Steps[i].Text)
				}
				diagnostics = append(diagnostics, notation.Diagnostic{
					Code: samplerZoneCode, Severity: "error", Position: position,
					Message: outOfZoneMessage(admitted, track, *id, written, note, step.Velocity),
				})
			}
		}
	}
	return diagnostics
}

// outOfZoneMessage names the note, its track and pattern, the pack and the keys
// that the pack does cover. A note whose key has a zone but none at its velocity
// says so.
func outOfZoneMessage(admitted admittedPack, track project.Track, pattern, written string, note int, velocity uint8) string {
	pitch := "pitch " + strconv.Itoa(note)
	if written != "" {
		pitch = fmt.Sprintf("pitch %s (MIDI %d)", written, note)
	}
	for _, zone := range admitted.pack.Zones {
		if !zone.Release && note >= int(zone.KeyLow) && note <= int(zone.KeyHigh) {
			pitch += fmt.Sprintf(" at velocity %d", velocity)
			break
		}
	}
	return fmt.Sprintf("%s on track %s, pattern %s, is outside every zone of the pack for sampler %s (%s, which covers MIDI %s)",
		pitch, track.ID, pattern, track.Kind, admitted.manifest, attackKeyRanges(admitted.pack))
}

// attackKeyRanges spells the keys that some attack zone of the pack covers, as
// ranges such as "23-66" or "22-26, 29-38".
func attackKeyRanges(pack *instrumentpack.Prepared) string {
	var covered [128]bool
	for _, zone := range pack.Zones {
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
