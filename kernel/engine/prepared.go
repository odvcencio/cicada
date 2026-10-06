package engine

import "m31labs.dev/cicada/kernel/seq"

// StereoVoiceFactory holds immutable, host-prepared audio. Each Engine gets a
// separate voice owner. Construction may allocate; voice methods must neither
// allocate nor perform I/O. PCM remains immutable for the engine's lifetime.
// Keeping this boundary in the kernel avoids linking file decoders or resident
// sample interpolation tables into the synth-only WebAssembly image.
type StereoVoiceFactory interface {
	NewStereoVoice(sampleRate int) (StereoVoice, error)
	VoiceCount() int
}

type StereoVoice interface {
	NextStereo() (float32, float32)
	NoteOn(note, velocity uint8) error
	NoteOff()
	Reset()
	SelectSlot(slot uint8, elapsedFrames int64, playing bool) error
	Play() error
}

// Reconstruct a clip's origin across keep, off, and repeated assignments. A
// sample's duration follows its source rate, independent of tempo changes.
func (e *Engine) restorePreparedClips(songIndex int, cycleStart int64) {
	// Seek reanchors the transport clock at its current tick; that clock clamps
	// earlier ticks. Use the score clock to recover elapsed musical duration.
	clock, _ := seq.NewClock(e.sampleRate, e.transport.BPMMilli())
	for track := 0; track < e.tracks; track++ {
		v := &e.voices[track]
		if !v.preparedClip {
			continue
		}
		slot, origin := -1, cycleStart
		for i := 0; i <= songIndex; i++ {
			entry := e.schedule[i]
			binding := e.scenes[entry.Scene].Track[track]
			switch binding.Mode {
			case SceneOff:
				slot = -1
			case SceneSlot:
				slot, origin = int(binding.Slot), cycleStart+entry.Tick
			}
		}
		v.prepared.Reset()
		e.patterns[track].active = int8(slot)
		if slot >= 0 {
			elapsed := clock.SampleAtTick(e.transport.Tick()) - clock.SampleAtTick(origin)
			if v.prepared.SelectSlot(uint8(slot), max(0, elapsed), e.transport.Playing()) != nil {
				e.fault(19)
				return
			}
		}
	}
}
