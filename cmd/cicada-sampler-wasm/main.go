// The optional sampler kernel keeps multisample PCM outside the core kernel.
// A host prepares one immutable instrument per instance before rendering.
package main

import (
	"m31labs.dev/cicada/kernel/voice/sample"
	"math"
	"unsafe"
)

const maxAssets = 4096
const maxPCM = 256 << 20

var assets [maxAssets]sample.Region
var descriptors [4096][20]float64
var setup [18]float64
var output [8192]float32
var handles [sample.MaxVoices]sample.Handle
var instrument *sample.Instrument
var assetCount, zoneCount, rate, pcmBytes int
var initialized, sealed bool
var lastSlot int32 = -1

//go:wasmexport sampler_init
func initialize(sampleRate, count int32) int32 {
	if initialized || count < 1 || count > 4096 || (sampleRate != 44100 && sampleRate != 48000 && sampleRate != 96000) {
		return -1
	}
	rate, zoneCount, initialized = int(sampleRate), int(count), true
	return 0
}

//go:wasmexport sampler_config_ptr
func configPtr() uint32 {
	if !initialized || sealed {
		return 0
	}
	return uint32(uintptr(unsafe.Pointer(&setup[0])))
}

//go:wasmexport sampler_zone_ptr
func zonePtr(index int32) uint32 {
	if !initialized || sealed || index < 0 || index >= int32(zoneCount) {
		return 0
	}
	return uint32(uintptr(unsafe.Pointer(&descriptors[index][0])))
}

//go:wasmexport sampler_pcm_alloc
func allocate(frames, channels, sourceRate int32) int32 {
	if !initialized || sealed || frames < 1 || frames > 8<<20 || channels < 1 || channels > 2 || sourceRate < 8000 || sourceRate > 192000 || assetCount >= maxAssets {
		return -1
	}
	bytes := int(frames) * int(channels) * 4
	if bytes > maxPCM-pcmBytes {
		return -1
	}
	r := sample.Region{Left: make([]float32, int(frames)), SampleRate: int(sourceRate), End: int(frames)}
	if channels == 2 {
		r.Right = make([]float32, int(frames))
	}
	index := assetCount
	assets[index] = r
	assetCount++
	pcmBytes += bytes
	return int32(index)
}

//go:wasmexport sampler_pcm_ptr
func pcmPtr(index, channel int32) uint32 {
	if !initialized || sealed || index < 0 || index >= int32(assetCount) || channel < 0 || channel > 1 {
		return 0
	}
	pcm := assets[index].Left
	if channel == 1 {
		pcm = assets[index].Right
	}
	if len(pcm) == 0 {
		return 0
	}
	return uint32(uintptr(unsafe.Pointer(&pcm[0])))
}
func integer(x float64, lo, hi float64) bool {
	return !math.IsNaN(x) && !math.IsInf(x, 0) && math.Trunc(x) == x && x >= lo && x <= hi
}

//go:wasmexport sampler_prepare
func prepare() int32 {
	if !initialized || sealed || assetCount == 0 {
		return -1
	}
	if !integer(setup[0], 1, sample.MaxVoices) || !integer(setup[16], 0, math.MaxUint32) || !integer(setup[17], 0, math.MaxUint32) {
		return -1
	}
	for i := 0; i < assetCount; i++ {
		for _, pcm := range [][]float32{assets[i].Left, assets[i].Right} {
			for _, x := range pcm {
				if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
					return -1
				}
			}
		}
	}
	zones := make([]sample.Zone, zoneCount)
	for i := range zones {
		d := descriptors[i]
		for j := 0; j < 20; j++ {
			if math.IsNaN(d[j]) || math.IsInf(d[j], 0) {
				return -1
			}
		}
		for _, j := range []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 13, 14, 15, 16, 17, 18, 19} {
			if !integer(d[j], 0, 1<<30) {
				return -1
			}
		}
		if int(d[0]) >= assetCount || d[1] > 127 || d[2] > 127 || d[3] > 127 || d[4] > 127 || d[5] > 127 || d[6] > 127 || d[7] > 255 || d[8] > 31 || d[9] > 32 || d[10] > 1 || d[15] > 1 || d[19] > 1023 {
			return -1
		}
		r := assets[int(d[0])]
		r.RootKey = uint8(d[1])
		r.Start = int(d[13])
		r.End = int(d[14])
		if r.End == 0 {
			r.End = len(r.Left)
		}
		r.Loop = d[15] == 1
		r.LoopStart = int(d[16])
		r.LoopEnd = int(d[17])
		r.Crossfade = int(d[18])
		zones[i] = sample.Zone{ChokeGroup: uint8(int(d[19]) & 255), OneShot: int(d[19])&256 != 0, ChokeSustain: int(d[19])&512 != 0, Region: r, KeyLow: uint8(d[2]), KeyHigh: uint8(d[3]), VelocityLow: uint8(d[4]), VelocityHigh: uint8(d[5]), Layer: uint8(d[6]), Group: uint8(d[7]), Position: uint8(d[8]), Count: uint8(d[9]), Release: d[10] == 1, Gain: d[11], TuneCents: d[12]}
	}
	c := sample.InstrumentConfig{Voices: int(setup[0]), Amp: sample.Envelope{Attack: setup[1], Decay: setup[2], Sustain: setup[3], Release: setup[4]}, Filter: sample.Envelope{Attack: setup[5], Decay: setup[6], Sustain: setup[7], Release: setup[8]}, Cutoff: setup[9], FilterDepth: setup[10], Gain: setup[11], TuneCents: setup[12], Humanize: sample.Humanize{DelayMS: setup[13], Velocity: setup[14], Cents: setup[15], Seed: uint64(setup[16]) | uint64(setup[17])<<32}}
	p, err := sample.NewInstrument(rate, zones, c)
	if err != nil {
		return -1
	}
	instrument, sealed = p, true
	return 0
}

//go:wasmexport sampler_note_on
func noteOn(note, velocity int32) uint32 {
	if !sealed || note < 0 || note > 127 || velocity < 0 || velocity > 127 {
		return 0
	}
	h, err := instrument.NoteOn(uint8(note), uint8(velocity))
	if err != nil || h.ID > math.MaxUint32 {
		return 0
	}
	if h.ID != 0 {
		handles[h.Slot] = h
		lastSlot = int32(h.Slot)
	}
	return uint32(h.ID)
}

//go:wasmexport sampler_last_slot
func noteSlot() int32 { return lastSlot }

//go:wasmexport sampler_note_on_seeded
func seededNoteOn(note, velocity int32, seedLow, seedHigh uint32) uint32 {
	if !sealed || note < 0 || note > 127 || velocity < 0 || velocity > 127 {
		return 0
	}
	h, err := instrument.NoteOnSeeded(uint8(note), uint8(velocity), uint64(seedLow)|uint64(seedHigh)<<32)
	if err != nil || h.ID > math.MaxUint32 {
		return 0
	}
	if h.ID != 0 {
		handles[h.Slot] = h
		lastSlot = int32(h.Slot)
	}
	return uint32(h.ID)
}

//go:wasmexport sampler_note_off
func noteOff(id uint32) int32 {
	if !sealed || id == 0 {
		return -1
	}
	for _, h := range handles {
		if h.ID == uint64(id) && instrument.NoteOff(h) {
			return 0
		}
	}
	return -1
}

//go:wasmexport sampler_sustain
func sustain(down int32) int32 {
	if !sealed || down < 0 || down > 1 {
		return -1
	}
	instrument.Sustain(down == 1)
	return 0
}

//go:wasmexport sampler_legato
func legato(id uint32, note int32, cents float64) int32 {
	if !sealed || note < 0 || note > 127 {
		return -1
	}
	for _, h := range handles {
		if h.ID == uint64(id) && instrument.Legato(h, uint8(note), cents) == nil {
			return 0
		}
	}
	return -1
}

//go:wasmexport sampler_reset
func reset() int32 {
	if !sealed {
		return -1
	}
	instrument.Reset()
	handles = [sample.MaxVoices]sample.Handle{}
	return 0
}

//go:wasmexport sampler_render
func render(frames int32) int32 {
	if !sealed || frames < 1 || frames > 4096 {
		return -1
	}
	instrument.Render(output[:frames], output[4096:4096+frames])
	return 0
}

//go:wasmexport sampler_output_ptr
func outputPtr() uint32 { return uint32(uintptr(unsafe.Pointer(&output[0]))) }
func main()             {}
