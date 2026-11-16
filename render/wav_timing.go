package render

import (
	"encoding/binary"
	"fmt"
	"io"

	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/project"
)

// maxWAVTempoChanges bounds the tempo steps one cica chunk records. The writer
// refuses a range that needs more, and the reader refuses a chunk that holds more.
const maxWAVTempoChanges = 256

// framesMatch compares a file's length with the recorded timing. An excerpt
// that starts after tempo changes cannot record the sub-sample position of its
// first bar, so its length may differ by one frame from the full-song count.
func framesMatch(got, want int64, from int, changes []project.TempoChange) bool {
	if got == want {
		return true
	}
	diff := got - want
	return from > 0 && len(changes) > 0 && diff >= -1 && diff <= 1
}

// wavTiming is the content of a cica chunk, version 1, 2 or 3.
type wavTiming struct {
	tempoMilli, bars, tailFrames uint32
	changes                      []project.TempoChange
	beats, unit                  int64 // version 3's meter; 0 is 4/4
	bytes                        int64 // size of the whole chunk, header included
}

// readWAVTiming reads the cica chunk that follows the PCM data and checks that
// it ends the file. Version 1 files hold one tempo. Version 2 adds the tempo
// steps of a song whose scenes change tempo. Version 3 adds the song's meter
// before the steps.
func readWAVTiming(file io.ReaderAt, at, fileSize int64) (wavTiming, error) {
	var timing wavTiming
	var head [12]byte
	if _, err := file.ReadAt(head[:], at); err != nil {
		return timing, fmt.Errorf("missing Cicada timing metadata")
	}
	size, version := int64(binary.LittleEndian.Uint32(head[4:8])), binary.LittleEndian.Uint32(head[8:12])
	fixed := int64(16)
	if version == 3 {
		fixed = 20
	}
	if string(head[:4]) != "cica" || version < 1 || version > 3 || size < fixed || (size-fixed)%8 != 0 || version == 1 && size != 16 || version == 2 && size == 16 || (size-fixed)/8 > maxWAVTempoChanges {
		return timing, fmt.Errorf("missing Cicada timing metadata")
	}
	if fileSize != at+8+size {
		return timing, fmt.Errorf("WAV size or frame alignment is invalid")
	}
	body := make([]byte, size-4)
	if _, err := file.ReadAt(body, at+12); err != nil {
		return timing, err
	}
	timing.bytes = 8 + size
	timing.tempoMilli = binary.LittleEndian.Uint32(body[0:4])
	timing.bars = binary.LittleEndian.Uint32(body[4:8])
	timing.tailFrames = binary.LittleEndian.Uint32(body[8:12])
	steps := 12
	if version == 3 {
		timing.beats, timing.unit, steps = int64(body[12]), int64(body[13]), 16
		if _, err := timing.meterMap(); err != nil || body[14] != 0 || body[15] != 0 {
			return timing, fmt.Errorf("invalid meter in Cicada timing metadata")
		}
	}
	previous := -1
	for i := steps; i < len(body); i += 8 {
		bar, tempo := int(binary.LittleEndian.Uint32(body[i:])), int(binary.LittleEndian.Uint32(body[i+4:]))
		if bar <= previous || bar > 1<<20 || tempo < 20_000 || tempo > 300_000 {
			return timing, fmt.Errorf("invalid tempo steps in Cicada timing metadata")
		}
		previous = bar
		timing.changes = append(timing.changes, project.TempoChange{Bar: bar, BPMMilli: tempo})
	}
	return timing, nil
}

// frames is the length a render of the recorded bars must have: the samples
// from the first to the last bar across every tempo step, plus the tail.
func (t wavTiming) frames(sampleRate, from int) (int64, error) {
	meterMap, err := t.meterMap()
	if err != nil {
		return 0, err
	}
	tempo, err := project.NewTempoMap(meterMap, sampleRate, int64(t.tempoMilli), t.changes)
	if err != nil {
		return 0, fmt.Errorf("invalid WAV tempo metadata: %w", err)
	}
	return tempo.SampleAtTick(meterMap.BarStart(int64(from+int(t.bars)))) - tempo.SampleAtTick(meterMap.BarStart(int64(from))) + int64(t.tailFrames), nil
}

func sameTempoChanges(a, b []project.TempoChange) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// meterMap places the recorded bars: version 3's meter, or 4/4 for versions
// 1 and 2, which record none.
func (t wavTiming) meterMap() (seq.MeterMap, error) {
	var m seq.MeterMap
	if t.beats == 0 {
		return m, nil
	}
	return seq.NewMeterMap([]seq.MeterChange{{Meter: seq.Meter{Beats: t.beats, Unit: t.unit}}})
}
