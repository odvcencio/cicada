//go:build tinygo

package engine

import "m31labs.dev/cicada/kernel/voice/sample"

type StreamingFactory interface{}
type StreamingRuntime struct{}

func (e *Engine) initStreaming(cfg *Config) error {
	if cfg.Streaming != nil {
		return engineError("native streamed assets cannot run in the audio worklet")
	}
	return nil
}
func (e *Engine) prepareStreamWindows(frames int) int { return frames }
func (e *Engine) copyClipAssets(*Config)              {}
func prepareClipVoice(cfg *Config, a AudioAsset, c ClipConfig, region sample.Region) (*sample.Voice, error) {
	if a.StreamID != 0 {
		return nil, engineError("audio worklet requires resident clip PCM")
	}
	return sample.New(cfg.SampleRate, region)
}
func (e *Engine) bindClipSource(*sample.Voice, int, uint16) error { return nil }
func (e *Engine) resetClipReaders()                               {}
func (e *Engine) finishClipVoice(*clipPlayback)                   {}
