package main

import (
	"fmt"
	"net/http"
	"os"

	"m31labs.dev/cicada/host/liveplay"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

type studioTransportInput struct {
	Action, Scene, Revision, Track, Pattern string
	Entry                                   int
	Quantize                                uint32
	Placement, Section, Start, End, Mode    string
	Loop                                    bool
}

type studioListening struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Track     string `json:"track,omitempty"`
	Mode      string `json:"mode,omitempty"`
	Entry     int    `json:"entry,omitempty"`
	StartTick int64  `json:"startTick"`
	EndTick   int64  `json:"endTick"`
	Loop      bool   `json:"loop"`
	Completed bool   `json:"completed"`
	Cycles    uint64 `json:"cycles"`
}

func (v studioListening) playbackRange() liveplay.PlaybackRange {
	return liveplay.PlaybackRange{StartTick: v.StartTick, EndTick: v.EndTick, Loop: v.Loop}
}

func listeningPosition(tick int64) string {
	if tick%seq.TicksPerStep != 0 {
		return fmt.Sprintf("%dticks", tick)
	}
	return fmt.Sprintf("@%d.%d.%d", tick/seq.TicksPerBar+1, tick%seq.TicksPerBar/seq.PPQ+1, tick%seq.PPQ/seq.TicksPerStep+1)
}

func (v studioListening) label() string {
	action := "Audition"
	if v.Loop {
		action = "Loop"
	}
	mode := ""
	if v.Kind == "placement" {
		mode = " · " + v.Mode
	}
	if v.Completed {
		action = "Finished"
	}
	return fmt.Sprintf("%s: %s%s · %s → %s", action, v.Name, mode, listeningPosition(v.StartTick), listeningPosition(v.EndTick))
}

func studioArrangementEnd(p *project.Project) int64 {
	var end int64
	if p.Arrange != nil {
		for _, place := range p.Arrange.Placements {
			end = max(end, place.AtTick+place.LengthTicks)
		}
	} else {
		for _, entry := range p.Song {
			end += int64(entry.Bars) * seq.TicksPerBar
		}
	}
	return end
}

// Listening is a transport view of the complete canonical project. Filtering
// a solo audition keeps every track, asset, effect, and pattern identity intact
// and never writes source, mute/solo controls, or Undo history.
func prepareStudioListening(p *project.Project, v studioListening) (*project.Project, *studioListening, error) {
	end := studioArrangementEnd(p)
	switch v.Kind {
	case "pattern":
		var pattern *project.Pattern
		for i := range p.Patterns {
			if p.Patterns[i].ID == v.Name {
				pattern = &p.Patterns[i]
				break
			}
		}
		if pattern == nil {
			return nil, nil, fmt.Errorf("choose a pattern to audition")
		}
		track := v.Track
		for _, t := range p.Tracks {
			if track != "" {
				break
			}
			for _, slot := range t.Slots {
				if slot != nil && *slot == v.Name {
					track = t.ID
					break
				}
			}
			if track != "" {
				break
			}
		}
		if track == "" {
			return nil, nil, fmt.Errorf("choose a track to hear this pattern")
		}
		found := false
		for _, candidate := range p.Tracks {
			if candidate.ID != track {
				continue
			}
			found = true
			if !project.PatternTrackCompatible(p, *pattern, candidate) {
				return nil, nil, fmt.Errorf("choose a compatible instrument or drum track")
			}
		}
		if !found {
			return nil, nil, fmt.Errorf("the preview track is no longer in the song")
		}
		length := pattern.DurationTicks()
		names := project.EditPlan(p, nil).Names
		name := "studio-performance-audition"
		for names[name] {
			name += "_"
		}
		copy := *p
		copy.Format, copy.Version, copy.Edition = project.FormatID2, 2, 2
		copy.Song, copy.Automation = []project.SongEntry{}, nil
		copy.Scenes = []project.Scene{}
		copy.Tracks = append([]project.Track(nil), p.Tracks...)
		for i := range copy.Tracks {
			copy.Tracks[i].Chain = nil
			if copy.Tracks[i].ID == track {
				copy.Tracks[i].Slots = [16]*string{&v.Name}
			}
		}
		copy.Arrange = &project.Arrangement{Markers: []project.Marker{}, Placements: []project.Placement{{ID: name, Track: track, Content: v.Name, LengthTicks: length}}}
		p = &copy
		v.Track, v.StartTick, v.EndTick = track, 0, length
		end = v.EndTick
	case "placement":
		if p.Arrange == nil {
			return nil, nil, fmt.Errorf("named placement audition requires an arrange block")
		}
		if v.Mode != "solo" && v.Mode != "context" {
			return nil, nil, fmt.Errorf("audition mode must be solo or context")
		}
		found := false
		for _, place := range p.Arrange.Placements {
			if place.ID != v.Name {
				continue
			}
			found = true
			v.StartTick, v.EndTick = place.AtTick, place.AtTick+place.LengthTicks
			break
		}
		if !found {
			return nil, nil, fmt.Errorf("placement %q is no longer in the score", v.Name)
		}
	case "section":
		if p.Arrange == nil {
			return nil, nil, fmt.Errorf("this score has song blocks instead of section markers")
		}
		found := false
		for _, marker := range p.Arrange.Markers {
			if marker.ID == v.Name {
				v.StartTick, found = marker.AtTick, true
				break
			}
		}
		if !found {
			return nil, nil, fmt.Errorf("section marker %q is no longer in the score", v.Name)
		}
		v.EndTick = end
		for _, marker := range p.Arrange.Markers {
			if marker.AtTick > v.StartTick {
				v.EndTick = min(v.EndTick, marker.AtTick)
			}
		}
	case "song":
		if p.Arrange != nil || v.Entry < 0 || v.Entry >= len(p.Song) || p.Song[v.Entry].Scene != v.Name {
			return nil, nil, fmt.Errorf("song block is no longer in this position")
		}
		v.StartTick = 0
		for i := 0; i < v.Entry; i++ {
			v.StartTick += int64(p.Song[i].Bars) * seq.TicksPerBar
		}
		v.EndTick = v.StartTick + int64(p.Song[v.Entry].Bars)*seq.TicksPerBar
	case "range":
	default:
		return nil, nil, fmt.Errorf("unknown listening target")
	}
	if err := v.playbackRange().Validate(); err != nil || v.EndTick > end {
		return nil, nil, fmt.Errorf("choose a start before the end and an end within the arrangement")
	}
	v.Completed, v.Cycles = false, 0
	return p, &v, nil
}

func (s *studio) startListening(input studioTransportInput) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startListeningLocked(input)
}

func (s *studio) startListeningLocked(input studioTransportInput) (int, error) {
	source, err := os.ReadFile(s.path)
	if err == nil && (input.Revision == "" || input.Revision != studioRevision(source)) {
		return http.StatusConflict, fmt.Errorf("score changed on disk; refresh Session before listening")
	}
	var p *project.Project
	if err == nil {
		p, err = compileStudioSource(s.path, source)
	}
	if err != nil {
		return http.StatusUnprocessableEntity, err
	}
	v := studioListening{Loop: true}
	switch input.Action {
	case "patternAudition":
		v.Kind, v.Name, v.Track, v.Loop = "pattern", input.Pattern, input.Track, input.Loop
	case "audition":
		v.Kind, v.Name, v.Mode, v.Loop = "placement", input.Placement, input.Mode, input.Loop
		if v.Mode == "" {
			v.Mode = "solo"
		}
	case "loopSection":
		v.Kind, v.Name = "section", input.Section
		if p.Arrange == nil {
			v.Kind, v.Entry = "song", input.Entry
			if input.Entry >= 0 && input.Entry < len(p.Song) {
				v.Name = p.Song[input.Entry].Scene
			}
		}
	case "loop":
		v.Kind, v.Name = "range", "Selection"
		v.StartTick, err = notation.MusicalTicks(input.Start, true)
		if err == nil {
			v.EndTick, err = notation.MusicalTicks(input.End, true)
		}
	}
	if err != nil {
		return http.StatusUnprocessableEntity, err
	}
	p, listening, err := prepareStudioListening(p, v)
	if err != nil {
		return http.StatusUnprocessableEntity, err
	}
	rate, err := s.transport.ensureSampleRate()
	if err != nil {
		return http.StatusServiceUnavailable, err
	}
	prepared, err := compileStudioListeningScore(s.path, p, listening, rate)
	if err != nil {
		return http.StatusUnprocessableEntity, err
	}
	preparedTransferred := false
	defer func() {
		if !preparedTransferred {
			_ = prepared.Engine.Close()
		}
	}()
	r := listening.playbackRange()
	prepared.Range = &r
	fingerprint, err := playSourceHashBytes(s.path, source)
	if err != nil {
		return http.StatusServiceUnavailable, err
	}
	latest, err := os.ReadFile(s.path)
	if err != nil || studioRevision(latest) != input.Revision {
		return http.StatusConflict, fmt.Errorf("score changed on disk; refresh Session before listening")
	}
	preparedTransferred = true
	if err := s.transport.startPrepared(-1, "", &prepared, fingerprint, listening); err != nil {
		s.transport.setError(err)
		return http.StatusServiceUnavailable, err
	}
	return http.StatusOK, nil
}
