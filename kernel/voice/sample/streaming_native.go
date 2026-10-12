//go:build !tinygo

package sample

import "m31labs.dev/cicada/kernel/stream"

type streamSource = stream.Reader

func (v *Voice) hasStream() bool { return v.source != nil }

// NewStreaming prepares the same SRC banks and clip envelopes as New, using
// immutable asset dimensions instead of resident PCM. BindStream runs only on
// the audio owner before starting this voice.
func NewStreaming(sampleRate, sourceRate int, start, end, frames int64) (*Voice, error) {
	if err := validateRate(sampleRate); err != nil {
		return nil, err
	}
	if sourceRate < 8000 || sourceRate > 192000 || start < 0 || end <= start || end > frames || end > 1<<30 {
		return nil, Error("stream sample bounds are invalid")
	}
	prepareBanks()
	v := &Voice{}
	v.configure(sampleRate, Region{SampleRate: sourceRate, RootKey: 60, Start: int(start), End: int(end)})
	return v, nil
}
func (v *Voice) BindStream(reader *stream.Reader) {
	v.source = reader
	reader.SetOutputRate(v.rate)
	reader.ResetOutput()
}
