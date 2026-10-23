package project

import "m31labs.dev/cicada/kernel/engine"

// NoteSchedule is validated timing data for interchange. It uses playback's
// pattern, scene and placement lowering without loading audio samples. Audio
// clip events remain in Schedule so consumers can preserve the song's length.
// This type has no voice factories or PCM and cannot start an audio engine.
type NoteSchedule struct {
	TrackKinds []engine.VoiceKind
	Patterns   []engine.PatternBank
	Scenes     []engine.Scene
	Song       []engine.SongEntry
	Schedule   []engine.ScheduleEvent
}

// CompileNoteSchedule checks the complete project, including asset metadata,
// routing and performance bounds. Only playback's decoded-PCM requirement is
// omitted; file contents are unnecessary for exporting notes and timing.
func CompileNoteSchedule(p *Project) (NoteSchedule, error) {
	cfg, err := compileEngine(p, 48_000, 128, nil, nil, compileNotes)
	if err != nil {
		return NoteSchedule{}, err
	}
	kinds := make([]engine.VoiceKind, len(p.Tracks))
	for i := range kinds {
		kinds[i] = cfg.Track[i].Kind
	}
	return NoteSchedule{TrackKinds: kinds, Patterns: cfg.Patterns, Scenes: cfg.Scenes, Song: cfg.Song, Schedule: cfg.Schedule}, nil
}
