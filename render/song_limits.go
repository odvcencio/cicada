package render

import (
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// maxDirectBars is the longest range the direct WAV mixer renders in one run.
// The scheduled engine has no such limit.
const maxDirectBars = 256

// SongLimit is a limit of the WAV render that a render of the whole song
// exceeds: What is "bars" or "tempo changes", Found the song's count and Max
// the most one render takes.
type SongLimit struct {
	What       string
	Found, Max int
}

// WholeSongLimits lists the limits that a render of the whole song (no --from
// or --bars) stops at before it writes audio: more than 256 bars on the direct
// WAV mixer, or more tempo changes than the WAV timing chunk records. It reads
// only the score, as render reads it with default options; a score that does
// not compile has none. A render of a shorter range can still succeed.
func WholeSongLimits(score *notation.Score) []SongLimit {
	if score == nil {
		return nil
	}
	semantic, _ := project.FromScore(score)
	if semantic == nil {
		return nil
	}
	schedule, err := project.CompileSchedule(semantic)
	if err != nil || len(schedule) == 0 {
		return nil
	}
	meterMap := semantic.MeterMap()
	var bars int
	if scheduledEngineReason(score, Options{}) == "" {
		// As renderWAV counts them.
		end, _ := meterMap.BarAt(schedule[len(schedule)-1].EndTick)
		bars = int(end)
	} else {
		// As renderScheduleWAV counts them.
		var duration int64
		for _, event := range schedule {
			duration = max(duration, event.Tick, event.EndTick)
		}
		bars = int(meterMap.BarsUntil(duration))
	}
	var limits []SongLimit
	if scheduledEngineReason(score, Options{}) == "" && bars > maxDirectBars {
		limits = append(limits, SongLimit{What: "bars", Found: bars, Max: maxDirectBars})
	}
	changes := project.ExcerptTempoChanges(semantic.TempoMilli, project.SongTempoChanges(semantic), 0, bars)
	if len(changes) > maxWAVTempoChanges {
		limits = append(limits, SongLimit{What: "tempo changes", Found: len(changes), Max: maxWAVTempoChanges})
	}
	return limits
}
