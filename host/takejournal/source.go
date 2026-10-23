package takejournal

import (
	"path/filepath"

	"m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/project"
)

// StartFrame is the raw asset offset corresponding to frame zero of the take.
// Initial loss can cross the count-in boundary and leave the first saved block
// with positive placement. Keep the part of that gap after the origin as silence.
func (t Take) StartFrame() int64 {
	if t.FirstBlock == nil {
		return 0
	}
	return max(0, int64(t.FirstBlock.RawFrame)-t.FirstBlock.Placement.EngineFrame)
}

// SelectSource delegates the pure source mutation to the edit service.
func SelectSource(source []byte, t Take) ([]byte, error) {
	return edit.SelectTakeSource(source, takeSelection(t))
}

// SelectProjectSource resolves imports and secondary score files before
// selecting a take. The source override is the exact candidate being edited.
func SelectProjectSource(path string, source []byte, t Take) ([]byte, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	score, ds, err := project.LoadScore(path, map[string][]byte{absolute: source})
	if err != nil {
		return nil, err
	}
	for _, d := range ds {
		if d.Severity == "error" {
			return nil, &project.SourceError{Diagnostic: d}
		}
	}
	return edit.SelectTakeSourceInProject(source, takeSelection(t), score)
}

func takeSelection(t Take) edit.SelectTake {
	return edit.SelectTake{ID: t.ID, Track: t.Track, Scene: t.Scene, Rate: t.Rate, Channels: t.Channels, StartFrame: t.StartFrame(), Asset: edit.TakeAsset{Name: t.Asset.Name, Path: t.Asset.Path, SHA256: t.Asset.SHA256, Frames: t.Asset.Frames, RateHz: t.Asset.RateHz, Channels: t.Asset.Channels}}
}
