package main

import (
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"

	"m31labs.dev/cicada/host/liveplay"
	"m31labs.dev/cicada/host/schedule"
	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/fx"
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

func (plan *livePlan) prepareScore(score *liveplay.Score) {
	score.Revision = plan.revision
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
type planDelta struct {
	structural bool
	reason     string
	params     []paramChange
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

// M3 patches live parameter values. Every remaining compiled difference goes through
// Offer until the later step, scene-binding and table milestones support it.
// Comparing the rest of cfg also catches non-live settings and asset changes.
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
	if !reflect.DeepEqual(a, b) {
		return structural("compiled engine structure changed")
	}
	delta := planDelta{}
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
	return delta
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
