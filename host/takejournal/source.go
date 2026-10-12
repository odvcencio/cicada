package takejournal

import "m31labs.dev/cicada/edit"

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
	return edit.SelectTakeSource(source, edit.SelectTake{ID: t.ID, Track: t.Track, Scene: t.Scene, Rate: t.Rate, Channels: t.Channels, StartFrame: t.StartFrame(), Asset: edit.TakeAsset{Name: t.Asset.Name, Path: t.Asset.Path, SHA256: t.Asset.SHA256, Frames: t.Asset.Frames, RateHz: t.Asset.RateHz, Channels: t.Asset.Channels}})
}
