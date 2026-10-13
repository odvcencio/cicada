package main

import (
	"encoding/hex"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"

	"m31labs.dev/cicada/host/liveplay"
	"m31labs.dev/cicada/host/schedule"
	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/project"
)

// livePlan is compiled from committed source, without constructing an engine.
// Its indices belong only to cfg; patch publication resolves stable entities
// against Player's playing snapshot and retains their incarnation tokens.
type livePlan struct {
	revision       string
	project        *project.Project
	cfg            engine.Config
	tracks         map[string]uint8
	kinds          map[string]string
	patterns       map[string]patternRef
	placements     map[string]placementRef
	scenes         map[string]uint16
	params         map[paramKey]float32
	parameterPaths map[string]paramKey
	tempoMilli     int64
}

// studioFingerprintKey names playback content (entry, every source file and
// cicada.mod). It detects change and keys the recording handoff; it is not an
// offer identity, because the same content can be published more than once.
func studioFingerprintKey(fingerprint [32]byte) string { return hex.EncodeToString(fingerprint[:]) }

func (plan *livePlan) prepareScore(score *liveplay.Score) {
	score.SceneParameters = make([][]liveplay.ParameterValue, len(plan.cfg.Scenes))
	for i, scene := range plan.cfg.Scenes {
		for _, setting := range scene.Settings {
			// Musical delay divisions remain structural settings; their numeric
			// value depends on tempo and is not a free-time parameter command.
			if setting.Division != 0 {
				continue
			}
			score.SceneParameters[i] = append(score.SceneParameters[i], liveplay.ParameterValue{Track: setting.Track, ID: setting.ID, Value: setting.Value})
		}
	}
}

type patternRef struct{ track, slot uint8 }
type placementRef struct {
	id    uint32
	index int
	event engine.ScheduleEvent
}
type paramKey struct {
	entity string
	id     kernel.ParamID
}
type paramChange struct {
	entity string
	track  uint8
	id     kernel.ParamID
	value  float32
}

// stepChange sets one step of one pattern slot. chord is the additive payload
// of the new step, or nil. A drum step carries its lane in packed.Note.
type stepChange struct {
	track, slot, index uint8
	packed             uint32
	chord              *seq.ChordStep
}
type lenChange struct{ track, slot, length uint8 }
type metaChange struct {
	track, slot uint8
	grid, swing uint16
	transpose   int8
}

// bindingChange rewrites one track's binding in one scene for its next launch.
type bindingChange struct {
	scene   uint16
	track   uint8
	binding engine.SceneBinding
}

type planDelta struct {
	structural bool
	reason     string
	params     []paramChange
	// edits holds stepChange, lenChange and metaChange values in the order that
	// keeps every intermediate pattern valid; the engine applies them in order
	// at one step boundary.
	edits    []any
	bindings []bindingChange
	// effective holds the pattern banks as the engine will hold them after the
	// edits land. It differs from a fresh compile in steps beyond a shortened
	// length, which the engine keeps. adopt installs it in the next plan so the
	// following diff replays from the engine's state, not from zeros.
	effective []engine.PatternBank
}

func buildLivePlan(path string, source []byte, sampleRate int) (*livePlan, error) {
	p, err := compileStudioSource(path, source)
	if err != nil {
		return nil, err
	}
	root, err := studioProjectRoot(path)
	if err != nil {
		return nil, err
	}
	cfg, err := schedule.Compile(p, root, sampleRate, liveBlockFrames)
	if err != nil {
		return nil, err
	}
	cfg.LoopSong = true
	validators, err := engine.PrepareParamValidators(&cfg)
	if err != nil {
		return nil, err
	}
	plan := &livePlan{revision: studioRevision(source), project: p, cfg: cfg, tempoMilli: int64(p.TempoMilli), tracks: make(map[string]uint8), kinds: make(map[string]string), patterns: make(map[string]patternRef), placements: make(map[string]placementRef), scenes: make(map[string]uint16), params: make(map[paramKey]float32), parameterPaths: make(map[string]paramKey)}
	for i, track := range p.Tracks {
		plan.tracks[track.ID], plan.kinds[track.ID] = uint8(i), track.Kind
		for slot, pattern := range track.Slots {
			if pattern != nil {
				plan.patterns[*pattern] = patternRef{track: uint8(i), slot: uint8(slot)}
			}
		}
	}
	for i, scene := range p.Scenes {
		plan.scenes[scene.ID] = uint16(i)
	}
	if p.Arrange != nil {
		for index, placement := range p.Arrange.Placements {
			id := uint32(index + 1)
			for _, event := range cfg.Schedule {
				if event.ID == id && (event.Kind == engine.SchedulePattern || event.Kind == engine.ScheduleClip) {
					plan.placements[placement.ID] = placementRef{id: id, index: index, event: event}
					break
				}
			}
		}
	}
	for _, address := range project.ParamAddresses(p) {
		descriptor, ok := project.LookupParamDescriptor(address.Param)
		id, known := kernel.FindParam(address.Param)
		if !ok || !known || !descriptor.Live {
			continue
		}
		entity, track := "global", uint8(0xff)
		if address.Track != "" {
			entity, track = "track:"+address.Track, plan.tracks[address.Track]
		}
		var numeric float64
		switch value := address.Value.(type) {
		case float64:
			numeric = value
		case float32:
			numeric = float64(value)
		case int:
			numeric = float64(value)
		case nil:
			if !descriptor.Off {
				continue
			}
			numeric = math.Inf(-1)
		default:
			continue
		}
		var validator engine.PreviewParamValidator
		if track != 0xff {
			validator = validators[track]
			if effective, ok := validator.CommittedValue(id); ok {
				numeric = effective
			}
		}
		value := float32(numeric)
		if !descriptor.Off || !math.IsInf(numeric, -1) {
			value, err = project.ParameterFloat32Value(descriptor.Min, descriptor.Max, numeric)
			if err != nil {
				continue
			}
		}
		if validator.Validate(id, value) == nil {
			key := paramKey{entity: entity, id: id}
			plan.params[key] = value
			plan.parameterPaths[address.Address] = key
		}
	}
	return plan, nil
}

// planDiff patches live parameter values, pattern steps, chords, lengths and
// metadata, and scene bindings. Every other compiled difference goes through
// Offer until the later table milestones support it. Comparing the rest of cfg
// also catches non-live settings and asset changes.
func planDiff(old, next *livePlan) planDelta {
	structural := func(reason string) planDelta { return planDelta{structural: true, reason: reason} }
	if old == nil || next == nil {
		return structural("playing plan unavailable")
	}
	for _, track := range next.project.Tracks {
		if _, ok := old.tracks[track.ID]; !ok {
			return structural("track added: " + track.ID)
		}
	}
	for _, track := range old.project.Tracks {
		if _, ok := next.tracks[track.ID]; !ok {
			return structural("track removed: " + track.ID)
		}
	}
	if !reflect.DeepEqual(old.tracks, next.tracks) || !reflect.DeepEqual(old.kinds, next.kinds) {
		return structural("track order or kind changed")
	}
	if old.tempoMilli != next.tempoMilli {
		return structural("tempo changed")
	}
	if (old.project.Arrange == nil) != (next.project.Arrange == nil) {
		return structural("song authority changed")
	}
	if !reflect.DeepEqual(old.patterns, next.patterns) || !reflect.DeepEqual(old.scenes, next.scenes) {
		return structural("pattern or scene identity changed")
	}
	a, b := old.cfg, next.cfg
	for i := 0; i < a.Tracks; i++ {
		// Prepared voice factories carry callback state and cannot be compared
		// or reused as though they were immutable structural descriptions.
		if a.Track[i].Prepared != nil || b.Track[i].Prepared != nil {
			return structural("prepared voice requires replacement")
		}
		if !reflect.DeepEqual(staticTrackParams(old, i), staticTrackParams(next, i)) {
			return structural("non-live track settings changed")
		}
		for _, cfg := range []*engine.Config{&a, &b} {
			track := &cfg.Track[i]
			track.GainDB, track.Pan, track.SendA, track.SendB = 0, 0, 0, 0
			track.GainSet, track.Mute, track.Solo = false, false, false
			var empty engine.TrackConfig
			track.Acid, track.Guitar, track.Drums, track.PianoSustain = empty.Acid, empty.Guitar, empty.Drums, 0
		}
	}
	if !reflect.DeepEqual(staticEffects(old), staticEffects(next)) {
		return structural("non-live effect settings changed")
	}
	// Automatic makeup couples several compressor values and has no command
	// for restoring automatic mode. Preserve it through a full replacement.
	if a.CompMusic != nil && b.CompMusic != nil && (a.CompMusic.MakeupAuto || b.CompMusic.MakeupAuto) && !reflect.DeepEqual(a.CompMusic, b.CompMusic) {
		return structural("automatic compressor makeup changed")
	}
	for _, cfg := range []*engine.Config{&a, &b} {
		if cfg.DelayA != nil {
			cfg.DelayA = &fx.DelayParams{}
		}
		if cfg.ReverbB != nil {
			cfg.ReverbB = &fx.ReverbParams{}
		}
		if cfg.CompMusic != nil {
			cfg.CompMusic = &fx.CompParams{}
		}
	}
	delta := planDelta{}
	if len(a.Patterns) != len(b.Patterns) || len(a.Scenes) != len(b.Scenes) {
		return structural("pattern or scene tables changed size")
	}
	for track := range a.Patterns {
		bank, reason := diffPatternBank(&delta, uint8(track), &a.Patterns[track], &b.Patterns[track], &a.Track[track])
		delta.effective = append(delta.effective, bank)
		if reason != "" {
			return structural(reason)
		}
	}
	for scene := range a.Scenes {
		for track := 0; track < engine.MaxTracks; track++ {
			if a.Scenes[scene].Track[track] != b.Scenes[scene].Track[track] {
				delta.bindings = append(delta.bindings, bindingChange{scene: uint16(scene), track: uint8(track), binding: b.Scenes[scene].Track[track]})
			}
		}
	}
	// Patterns and scene bindings were compared above; blank them for the rest.
	for _, cfg := range []*engine.Config{&a, &b} {
		cfg.Patterns = nil
		scenes := make([]engine.Scene, len(cfg.Scenes))
		for i := range scenes {
			scenes[i].Settings = cfg.Scenes[i].Settings
		}
		cfg.Scenes = scenes
	}
	if !reflect.DeepEqual(a, b) {
		return structural("compiled engine structure changed")
	}
	for key, value := range next.params {
		previous, ok := old.params[key]
		if ok && math.Float32bits(previous) == math.Float32bits(value) {
			continue
		}
		if !ok {
			return structural(fmt.Sprintf("parameter requires replacement: %s", key.entity))
		}
		track := uint8(0xff)
		for id, index := range next.tracks {
			if key.entity == "track:"+id {
				track = index
				break
			}
		}
		delta.params = append(delta.params, paramChange{entity: key.entity, track: track, id: key.id, value: value})
	}
	for key := range old.params {
		if _, ok := next.params[key]; !ok {
			return structural("parameter address removed")
		}
	}
	sort.Slice(delta.params, func(i, j int) bool {
		a, b := delta.params[i], delta.params[j]
		if a.track != b.track {
			return a.track < b.track
		}
		return a.id < b.id
	})
	if len(delta.params)+len(delta.bindings)+editCommandCount(delta.edits) > liveplay.PatchCapacity {
		return structural("edit is larger than one patch")
	}
	return delta
}

// adopt makes next model the engine after this delta lands: edits leave steps
// beyond a shortened length in place, which a fresh compile would zero.
func (d planDelta) adopt(next *livePlan) {
	if d.effective != nil {
		next.cfg.Patterns = d.effective
	}
}

func editCommandCount(edits []any) int {
	n := 0
	for _, edit := range edits {
		n++
		if step, ok := edit.(stepChange); ok && step.chord != nil {
			n++
		}
	}
	return n
}

// diffPatternBank appends the edits that turn the old bank into the new one. It
// returns a reason when some difference has no command: the chain, a slot that
// appears or disappears, expression, gate or seed.
func diffPatternBank(delta *planDelta, track uint8, old, next *engine.PatternBank, config *engine.TrackConfig) (engine.PatternBank, string) {
	effective := *next
	if next.Drums != nil {
		lanes := *next.Drums
		effective.Drums = &lanes
	}
	if !reflect.DeepEqual(old.Chain, next.Chain) || (old.Drums == nil) != (next.Drums == nil) {
		return effective, "pattern chain or bank kind changed"
	}
	for slot := range old.Slots {
		var oldLanes, nextLanes *[drum.LaneCount]seq.Pattern
		if old.Drums != nil {
			oldLanes, nextLanes = &old.Drums[slot], &next.Drums[slot]
		}
		if old.Slots[slot].Len == 0 || next.Slots[slot].Len == 0 {
			if !reflect.DeepEqual(old.Slots[slot], next.Slots[slot]) || oldLanes != nil && *oldLanes != *nextLanes {
				return effective, "pattern slot appeared or disappeared"
			}
			continue
		}
		edits, final, finalLanes, reason := diffSlot(track, uint8(slot), &old.Slots[slot], &next.Slots[slot], oldLanes, nextLanes, config)
		if reason != "" {
			return effective, reason
		}
		delta.edits = append(delta.edits, edits...)
		effective.Slots[slot] = final
		if finalLanes != nil {
			effective.Drums[slot] = *finalLanes
		}
	}
	return effective, ""
}

// diffSlot also returns the slot as the engine will hold it after the edits.
func diffSlot(track, slot uint8, old, next *seq.Pattern, oldLanes, nextLanes *[drum.LaneCount]seq.Pattern, config *engine.TrackConfig) ([]any, seq.Pattern, *[drum.LaneCount]seq.Pattern, string) {
	if old.GatePercent != next.GatePercent || old.Seed != next.Seed || !reflect.DeepEqual(old.Expression, next.Expression) {
		return nil, *old, oldLanes, "pattern gate, seed or expression changed"
	}
	var lenEdit, metaEdit any
	if old.Len != next.Len {
		lenEdit = lenChange{track: track, slot: slot, length: next.Len}
	}
	if old.StepTicks != next.StepTicks || old.SwingPermille != next.SwingPermille || old.Transpose != next.Transpose {
		metaEdit = metaChange{track: track, slot: slot, grid: next.StepTicks, swing: next.SwingPermille, transpose: next.Transpose}
	}
	var steps []any
	// Steps beyond the new length cannot sound and a shorter pattern keeps its
	// old ones; a longer pattern takes every new step so none is stale.
	for index := uint8(0); index < next.Len; index++ {
		grown := index >= old.Len
		if oldLanes != nil {
			for lane := drum.Lane(0); lane < drum.LaneCount; lane++ {
				if o, n := oldLanes[lane].Steps[index], nextLanes[lane].Steps[index]; grown || o != n {
					steps = append(steps, stepChange{track: track, slot: slot, index: index, packed: n&^0x7f | uint32(lane)})
				}
			}
			continue
		}
		if grown || old.Steps[index] != next.Steps[index] || old.Chords[index] != next.Chords[index] {
			change := stepChange{track: track, slot: slot, index: index, packed: next.Steps[index]}
			if chord := next.Chords[index]; chord.Count > 0 {
				change.chord = &chord
			}
			steps = append(steps, change)
		}
	}
	if lenEdit == nil && metaEdit == nil && len(steps) == 0 {
		return nil, *old, oldLanes, ""
	}
	if oldLanes != nil {
		for lane := range nextLanes {
			shared := nextLanes[lane]
			if shared.Len != next.Len || shared.StepTicks != next.StepTicks || shared.SwingPermille != next.SwingPermille || shared.Transpose != next.Transpose || shared.GatePercent != next.GatePercent {
				return nil, *old, oldLanes, "drum lane differs from its slot"
			}
		}
	}
	// Try a few orders; keep the first for which the engine would accept every
	// intermediate pattern. A length or transpose change can invalidate a pattern
	// before the steps that fix it have landed, in one direction or the other.
	reversed := make([]any, len(steps))
	for i := range steps {
		reversed[len(steps)-1-i] = steps[i]
	}
	for _, order := range [][][]any{
		{{lenEdit, metaEdit}, steps}, {{metaEdit, lenEdit}, steps}, {steps, {lenEdit, metaEdit}}, {steps, {metaEdit, lenEdit}},
		{{lenEdit, metaEdit}, reversed}, {{metaEdit, lenEdit}, reversed}, {reversed, {lenEdit, metaEdit}}, {reversed, {metaEdit, lenEdit}},
	} {
		var edits []any
		for _, group := range order {
			for _, edit := range group {
				if edit != nil {
					edits = append(edits, edit)
				}
			}
		}
		if final, lanes, ok := replayEdits(edits, old, next, oldLanes, nextLanes, config); ok {
			return edits, final, lanes, ""
		}
	}
	return nil, *old, oldLanes, "pattern edit has no valid order"
}

// editCommand is the kernel command that carries one edit; a step with a chord
// also sends the chord command from chordCommand.
func editCommand(edit any) (cmd.Command, bool) {
	switch edit := edit.(type) {
	case stepChange:
		return cmd.Command{Op: cmd.OpSetStep, Track: edit.track, Index: uint16(edit.index), Arg0: edit.packed, Arg1: uint32(edit.slot)}, true
	case lenChange:
		return cmd.Command{Op: cmd.OpSetPatternLen, Track: edit.track, Index: uint16(edit.length), Arg1: uint32(edit.slot)}, true
	case metaChange:
		return cmd.Command{Op: cmd.OpSetPatternMeta, Track: edit.track, Index: edit.grid, Arg0: uint32(edit.swing) | uint32(uint16(int16(edit.transpose)))<<16, Arg1: uint32(edit.slot)}, true
	}
	return cmd.Command{}, false
}

func chordCommand(edit stepChange) (cmd.Command, error) {
	return cmd.ChordStepCommand(edit.track, edit.slot, edit.index, edit.chord.Notes, edit.chord.Count)
}

// replayEdits applies the edits to copies of the old slot with the kernel's own
// edit rules and reports whether every step is accepted and the result is the
// new slot. It returns the slot the edits leave behind.
func replayEdits(edits []any, old, next *seq.Pattern, oldLanes, nextLanes *[drum.LaneCount]seq.Pattern, config *engine.TrackConfig) (seq.Pattern, *[drum.LaneCount]seq.Pattern, bool) {
	slot := *old
	var lanes *[drum.LaneCount]seq.Pattern
	if oldLanes != nil {
		copied := *oldLanes
		lanes = &copied
	}
	apply := func(c cmd.Command) bool {
		return c.Validate(engine.MaxTracks) == nil && engine.ApplyPatternEdit(&slot, lanes, c, config) == nil
	}
	for _, edit := range edits {
		c, _ := editCommand(edit)
		if !apply(c) {
			return seq.Pattern{}, nil, false
		}
		if step, ok := edit.(stepChange); ok && step.chord != nil {
			chord, err := chordCommand(step)
			if err != nil || !apply(chord) {
				return seq.Pattern{}, nil, false
			}
		}
	}
	if slot.Len != next.Len || slot.StepTicks != next.StepTicks || slot.SwingPermille != next.SwingPermille || slot.Transpose != next.Transpose {
		return seq.Pattern{}, nil, false
	}
	for i := uint8(0); i < next.Len; i++ {
		if lanes != nil {
			for lane := range lanes {
				want := nextLanes[lane].Steps[i]
				if got := lanes[lane].Steps[i]; got != want && got != want&^0x7f|uint32(lane) {
					return seq.Pattern{}, nil, false
				}
			}
		} else if slot.Steps[i] != next.Steps[i] || slot.Chords[i] != next.Chords[i] {
			return seq.Pattern{}, nil, false
		}
	}
	return slot, lanes, true
}

// Remove only numeric controls represented in the validated parameter map.
// Static enums and recipe values without a command representation stay in the
// structural comparison even when they share a synthesis config with live knobs.
func staticTrackParams(plan *livePlan, index int) map[string]project.Value {
	track := plan.project.Tracks[index]
	params := make(map[string]project.Value)
	for source, value := range track.Params {
		params[source] = value
	}
	for key := range plan.params {
		if key.entity == "track:"+track.ID {
			spec := kernel.Params[key.id]
			if value, ok := params[spec.Source]; ok && (value.Number != nil || spec.Curve == "toggle" || spec.Off && value.Text == "off") {
				delete(params, spec.Source)
			}
		}
	}
	return params
}

func staticEffects(plan *livePlan) []project.Effect {
	effects := append([]project.Effect(nil), plan.project.Effects...)
	addresses := project.ParamAddresses(plan.project)
	for i := range effects {
		params := make(map[string]project.Value)
		for source, value := range effects[i].Params {
			params[source] = value
		}
		for _, address := range addresses {
			if address.Track != "" || !strings.HasPrefix(address.Address, effects[i].ID+".") {
				continue
			}
			id, ok := kernel.FindParam(address.Param)
			if !ok {
				continue
			}
			if _, ok := plan.params[paramKey{entity: "global", id: id}]; ok {
				spec := kernel.Params[id]
				if value, ok := params[spec.Source]; ok && (value.Number != nil || spec.Curve == "toggle" || spec.Off && value.Text == "off") {
					delete(params, spec.Source)
				}
			}
		}
		effects[i].Params = params
	}
	return effects
}
