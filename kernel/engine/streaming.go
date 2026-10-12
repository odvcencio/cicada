//go:build !tinygo

package engine

import (
	"context"
	"m31labs.dev/cicada/kernel/stream"
	"m31labs.dev/cicada/kernel/voice/sample"
)

// StreamingFactory is a native host preparation plan. Config contains no open
// files or workers; New opens an independently owned, bounded runtime after
// all engine validation succeeds. Portable kernel images use resident assets.
type StreamingFactory interface {
	New(readers int, outputRate, maxBlock int) (StreamingRuntime, error)
}
type StreamingRuntime interface {
	Reader(int) *stream.Reader
	WaitReady(context.Context, *stream.Reader) error
	Stats() stream.Stats
	LastError() error
	Retire()
	Close() error
}

type StreamingStats struct {
	ArenaBytes    int64  `json:"arenaBytes"`
	Loaded        uint64 `json:"loaded"`
	Evicted       uint64 `json:"evicted"`
	Cancelled     uint64 `json:"cancelled"`
	ReadErrors    uint64 `json:"readErrors"`
	Misses        uint64 `json:"misses"`
	MissingFrames uint64 `json:"missingFrames"`
	Recoveries    uint64 `json:"recoveries"`
	PageLookups   uint64 `json:"pageLookups"`
}

func (e *Engine) initStreaming(cfg *Config) error {
	if cfg.Streaming == nil {
		return nil
	}
	if e.clipLimit < 1 {
		return Error("streamed clips have no voice capacity")
	}
	runtime, err := cfg.Streaming.New(e.clipLimit*2, e.sampleRate, e.maxBlock)
	if err != nil {
		return err
	}
	e.streaming = runtime
	// Prime current and upcoming arrangement clips before native audio begins.
	e.prepareStreamWindows(e.maxBlock)
	return nil
}

// Retire is callback-safe. Asset cancellation and file closure happen on the
// runtime's existing monitor worker. A retired engine must never render again.
func (e *Engine) Retire() {
	e.retireParallel()
	// A retired job may still own clip/readers. Host Close joins it first.
	if e.parallelAvailable() && !e.parallelQuiescent() {
		return
	}
	if e.streaming != nil {
		e.stopClips(-1)
		for i := 0; i < e.clipLimit*2; i++ {
			e.streaming.Reader(i).Stop()
		}
		e.streaming.Retire()
	}
}

// Close releases files and joins asset workers after rendering has stopped.
// It is host-only; never invoke it from an audio callback.
func (e *Engine) Close() error {
	e.closeParallel()
	if e.streaming == nil || e.streamClosed {
		return nil
	}
	// Keep the runtime immutable for telemetry readers holding a retired engine.
	e.streamClosed = true
	runtime := e.streaming
	e.stopClips(-1)
	for i := 0; i < e.clipLimit*2; i++ {
		runtime.Reader(i).Stop()
	}
	return runtime.Close()
}

func (e *Engine) StreamError() error {
	if e.streaming == nil {
		return nil
	}
	return e.streaming.LastError()
}

// StreamStats is safe to poll while rendering. It reports output-frame misses,
// so a storage interruption remains visible even if transport keeps advancing.
func (e *Engine) StreamStats() StreamingStats {
	var out StreamingStats
	if e.streaming == nil {
		return out
	}
	stats := e.streaming.Stats()
	out.ArenaBytes, out.Loaded, out.Evicted, out.Cancelled, out.ReadErrors = stats.ArenaBytes, stats.Loaded, stats.Evicted, stats.Cancelled, stats.ReadErrors
	for i := 0; i < e.clipLimit; i++ {
		r := e.streaming.Reader(i).Stats()
		out.Misses += r.Misses
		out.MissingFrames += r.MissingFrames
		out.Recoveries += r.Recoveries
		out.PageLookups += r.PageLookups
	}
	return out
}

// PrepareRender waits for demanded pages outside rendering. It may shorten a
// block with more upcoming clip starts than the prefetch table can hold. Hosts
// render exactly the returned length, then call again. Commands are consumed
// with the same ordering as Render; no audio samples or transport frames pass.
func (e *Engine) PrepareRender(ctx context.Context, frames int) (int, error) {
	if frames < 1 || frames > e.maxBlock {
		return 0, Error("invalid streamed render block")
	}
	if e.streaming == nil {
		return frames, nil
	}
	e.drainCommands()
	if e.faulted {
		return 0, Error("streamed render engine fault")
	}
	frames = e.prepareStreamWindows(frames)
	for i := 0; i < e.clipLimit; i++ {
		if e.clipVoices[i].active {
			asset := e.clipAssets[e.clipSpecs[e.clipVoices[i].clip].Asset]
			if asset.StreamID != 0 {
				if err := e.streaming.WaitReady(ctx, e.streaming.Reader(i)); err != nil {
					return 0, err
				}
			}
		}
	}
	for i := 0; i < e.streamPrefetchCount; i++ {
		if err := e.streaming.WaitReady(ctx, e.streaming.Reader(e.clipLimit+i)); err != nil {
			return 0, err
		}
	}
	return frames, nil
}

// prepareStreamWindows belongs to the audio owner. It announces current
// cursors and upcoming clip starts, without I/O, locks, waiting or allocation.
func (e *Engine) prepareStreamWindows(frames int) int {
	if e.streaming == nil {
		return frames
	}
	for i := 0; i < e.clipLimit; i++ {
		reader := e.streaming.Reader(i)
		reader.BeginBlock()
		v := &e.clipVoices[i]
		if v.active && e.clipAssets[e.clipSpecs[v.clip].Asset].StreamID != 0 {
			reader.AdvanceFrame(e.clipSpecs[v.clip].StartFrame + int64(v.sourceFrame))
		}
	}
	e.streamPrefetchCount = 0
	clock := e.transport.Clock()
	sample := e.transport.Sample()
	horizon := sample + int64(max(frames, e.sampleRate/4))
	// Arrangement indices are immutable and monotonic. Native playback requests
	// a quarter-second horizon; offline waits for exactly those bounded windows.
	index := max(0, e.songIndex+1)
	cycle := e.cycleStart
	if !e.placementSchedule && e.songMode {
		index = e.songIndex + 1
	}
	// Before first Play, initial song starts must also be ready.
	if !e.songMode {
		index = 0
		cycle = 0
	}
	for len(e.schedule) > 0 {
		if index >= len(e.schedule) {
			if !e.loopSong || e.scheduleDuration <= 0 {
				break
			}
			index = 0
			cycle += e.scheduleDuration
		}
		event := e.schedule[index]
		at := clock.SampleAtTick(event.Tick + cycle)
		if at > horizon {
			break
		}
		if event.Kind == ScheduleClip {
			frames = e.prefetchStreamClip(event.Index, at-sample, frames)
		} else if event.Kind == ScheduleScene {
			for track, binding := range e.scenes[event.Scene].Track {
				if track >= e.tracks {
					break
				}
				if binding.Mode == SceneClip {
					frames = e.prefetchStreamClip(binding.Clip, at-sample, frames)
				}
			}
		}
		index++
		if e.streamPrefetchCount >= e.clipLimit && at >= sample+int64(frames) {
			break
		}
	}
	for i := e.streamPrefetchCount; i < e.clipLimit; i++ {
		e.streaming.Reader(e.clipLimit + i).Stop()
	}
	return max(1, frames)
}
func (e *Engine) prefetchStreamClip(clip uint16, until int64, frames int) int {
	if int(clip) >= len(e.clipSpecs) {
		return frames
	}
	spec := e.clipSpecs[clip]
	asset := e.clipAssets[spec.Asset]
	if asset.StreamID == 0 {
		return frames
	}
	if e.streamPrefetchCount >= e.clipLimit {
		if until > 0 && until < int64(frames) {
			return int(until)
		}
		return frames
	}
	reader := e.streaming.Reader(e.clipLimit + e.streamPrefetchCount)
	// Seek overwrites intent in constant space; ready pages remain reusable.
	_ = reader.SetAsset(asset.StreamID)
	_ = reader.PrefetchFrame(spec.StartFrame)
	e.streamPrefetchCount++
	return frames
}

func (e *Engine) copyClipAssets(cfg *Config) { e.clipAssets = append([]AudioAsset(nil), cfg.Assets...) }
func prepareClipVoice(cfg *Config, a AudioAsset, c ClipConfig, region sample.Region) (*sample.Voice, error) {
	if a.StreamID != 0 {
		if cfg.Streaming == nil || len(a.Left) != 0 || len(a.Right) != 0 {
			return nil, Error("streamed clip requires a native streaming factory")
		}
		return sample.NewStreaming(cfg.SampleRate, a.SampleRate, c.StartFrame, c.EndFrame, a.Frames)
	}
	return sample.New(cfg.SampleRate, region)
}
func (e *Engine) bindClipSource(voice *sample.Voice, i int, clip uint16) error {
	asset := e.clipAssets[e.clipSpecs[clip].Asset]
	if asset.StreamID != 0 {
		reader := e.streaming.Reader(i)
		if err := reader.SetAsset(asset.StreamID); err != nil {
			return err
		}
		voice.BindStream(reader)
	}
	return nil
}
func (e *Engine) resetClipReaders()               { e.stopClips(-1) }
func (e *Engine) finishClipVoice(v *clipPlayback) { v.voice.Reset() }
