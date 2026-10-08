package edit

import (
	"fmt"
	"strconv"
	"strings"
)

// EntityID names a semantic entity: "<kind>" or "<kind>:<name>[/<segment>...]".
// Name-based IDs are stable across recompiles. Index-based IDs (song entry,
// step) are tied to one revision; Apply enforces the envelope revision.
type EntityID string

const (
	KindProject   = "project"
	KindTrack     = "track"
	KindPattern   = "pattern"
	KindStep      = "step"
	KindScene     = "scene"
	KindSong      = "song"
	KindPlacement = "placement"
	KindMarker    = "marker"
	KindClip      = "clip"
	KindParam     = "param"
	KindFX        = "fx"
	KindBus       = "bus"
	KindMaster    = "master"
	KindSetting   = "setting"
)

func (id EntityID) Kind() string { kind, _, _ := strings.Cut(string(id), ":"); return kind }

// Name is everything after the kind; for step and setting IDs it includes segments.
func (id EntityID) Name() string { _, rest, _ := strings.Cut(string(id), ":"); return rest }

func (id EntityID) segments() []string {
	if id.Name() == "" {
		return nil
	}
	return strings.Split(id.Name(), "/")
}

func ParseEntityID(text string) (EntityID, error) {
	id := EntityID(text)
	segments := id.segments()
	switch id.Kind() {
	case KindProject, KindMaster:
		if len(segments) != 0 {
			return "", fmt.Errorf("%s takes no name", id.Kind())
		}
	case KindTrack, KindPattern, KindScene, KindPlacement, KindMarker, KindClip, KindFX, KindBus, KindParam:
		if len(segments) != 1 || segments[0] == "" {
			return "", fmt.Errorf("%s needs one name", id.Kind())
		}
	case KindStep:
		if len(segments) != 2 && len(segments) != 3 {
			return "", fmt.Errorf("step needs pattern[/lane]/index")
		}
		if _, err := strconv.Atoi(segments[len(segments)-1]); err != nil {
			return "", fmt.Errorf("step index must be an integer")
		}
	case KindSong:
		if len(segments) != 1 {
			return "", fmt.Errorf("song needs an entry index")
		}
		if _, err := strconv.Atoi(segments[0]); err != nil {
			return "", fmt.Errorf("song entry index must be an integer")
		}
	case KindSetting:
		if len(segments) != 2 {
			return "", fmt.Errorf("setting needs scene/path")
		}
	default:
		return "", fmt.Errorf("unknown entity kind %q", id.Kind())
	}
	return id, nil
}

// Entity is an ID resolved against one plan. Index is the plan (engine)
// index of the owning slice element; it is only valid for that plan.
type Entity struct {
	ID         EntityID
	Kind, Name string
	Index      int
	Track      string
	Lane       string
	Step       int
	Revision   string
}

func Resolve(plan *Plan, id EntityID) (Entity, error) {
	if _, err := ParseEntityID(string(id)); err != nil {
		return Entity{}, err
	}
	if plan == nil {
		return Entity{}, fmt.Errorf("cannot resolve %s without a plan", id)
	}
	entity := Entity{ID: id, Kind: id.Kind(), Name: id.Name(), Index: -1, Revision: plan.Revision}
	find := func(n int, name func(int) string, label string) error {
		for i := 0; i < n; i++ {
			if name(i) == entity.Name {
				entity.Index = i
				return nil
			}
		}
		return fmt.Errorf("unknown %s %q", label, entity.Name)
	}
	switch entity.Kind {
	case KindProject, KindMaster, KindSetting:
		return entity, nil
	case KindParam:
		if plan.ResolveParam == nil {
			return Entity{}, fmt.Errorf("cannot resolve %s: the plan has no parameter resolver", id)
		}
		if _, err := plan.ResolveParam(entity.Name); err != nil {
			return Entity{}, err
		}
		return entity, nil
	case KindFX, KindBus:
		if !plan.Names[entity.Name] {
			return Entity{}, fmt.Errorf("unknown %s %q", entity.Kind, entity.Name)
		}
		return entity, nil
	case KindTrack:
		return entity, find(len(plan.Tracks), func(i int) string { return plan.Tracks[i].ID }, "track")
	case KindPattern:
		return entity, find(len(plan.Patterns), func(i int) string { return plan.Patterns[i].ID }, "pattern")
	case KindScene:
		return entity, find(len(plan.Scenes), func(i int) string { return plan.Scenes[i].ID }, "scene")
	case KindPlacement:
		return entity, find(len(plan.Placements), func(i int) string { return plan.Placements[i].ID }, "placement")
	case KindMarker:
		return entity, find(len(plan.Markers), func(i int) string { return plan.Markers[i].ID }, "marker")
	case KindClip:
		return entity, find(len(plan.Clips), func(i int) string { return plan.Clips[i].ID }, "clip")
	case KindSong:
		index, _ := strconv.Atoi(id.segments()[0])
		if index < 0 || index >= len(plan.Song) {
			return Entity{}, fmt.Errorf("song entry %d is out of range", index)
		}
		entity.Index = index
		return entity, nil
	case KindStep:
		segments := id.segments()
		entity.Name = segments[0]
		if len(segments) == 3 {
			entity.Lane = segments[1]
		}
		entity.Step, _ = strconv.Atoi(segments[len(segments)-1])
		if err := find(len(plan.Patterns), func(i int) string { return plan.Patterns[i].ID }, "pattern"); err != nil {
			return Entity{}, err
		}
		pattern := plan.Patterns[entity.Index]
		count := len(pattern.Data)
		if entity.Lane != "" {
			count = len(pattern.Lanes[entity.Lane])
		}
		if entity.Step < 0 || entity.Step >= count {
			return Entity{}, fmt.Errorf("pattern %s has no step %d in lane %s", entity.Name, entity.Step+1, entity.Lane)
		}
		return entity, nil
	}
	return Entity{}, fmt.Errorf("unknown entity kind %q", entity.Kind)
}
