package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net/http"

	"m31labs.dev/cicada/host/capture"
	"m31labs.dev/cicada/host/sampleasset"
	"m31labs.dev/cicada/kernel/voice/sample"
)

type studioBrowserBlock struct {
	Timing   capture.Block `json:"timing"`
	RawFrame uint64        `json:"rawFrame"`
	Offset   int           `json:"offset"`
	Length   int           `json:"length"`
}

type studioBrowserTake struct {
	Rate       int                  `json:"sampleRate"`
	Channels   int                  `json:"channels"`
	PCM        []byte               `json:"pcm"`
	Blocks     []studioBrowserBlock `json:"blocks"`
	RawFrames  uint64               `json:"rawFrames"`
	Incomplete bool                 `json:"incomplete"`
}

type studioSampleRequest struct {
	Root int  `json:"root"`
	Note int  `json:"note"`
	Loop bool `json:"loop"`
}

func (s *studio) importBrowserTake(edit studioEdit) (string, error) {
	take := edit.Capture
	if take == nil || take.Rate < 8000 || take.Rate > 192000 || take.Channels < 1 || take.Channels > 2 ||
		len(take.Blocks) == 0 || take.RawFrames == 0 || take.RawFrames > sampleasset.MaxFrames || len(take.PCM) > 64<<20 {
		return "", errors.New("browser take has an invalid or oversized format")
	}
	// Admit only the worker's committed, contiguous byte prefix. Raw frame gaps
	// remain separate from PCM offsets, and placement is recomputed by the capture adapter.
	offset, cursor := 0, uint64(0)
	for _, block := range take.Blocks {
		b := block.Timing
		if b.Frames < 1 || b.Frames > 2048 || b.SampleRate != take.Rate || int(b.Layout) != take.Channels ||
			b.GapFrames > take.RawFrames || cursor > take.RawFrames-b.GapFrames || block.RawFrame != cursor+b.GapFrames ||
			block.RawFrame > take.RawFrames || uint64(b.Frames) > take.RawFrames-block.RawFrame ||
			block.Offset != offset || block.Length != b.Frames*take.Channels*4 || block.Length > len(take.PCM)-offset {
			return "", errors.New("browser capture journal or PCM prefix is inconsistent")
		}
		offset += block.Length
		cursor = block.RawFrame + uint64(b.Frames)
	}
	if offset != len(take.PCM) {
		return "", errors.New("browser take contains uncommitted PCM")
	}
	for i := 0; i < len(take.PCM); i += 4 {
		value := math.Float32frombits(binary.LittleEndian.Uint32(take.PCM[i:]))
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return "", errors.New("browser PCM contains non-finite samples")
		}
	}
	id, err := s.takes.Begin(edit.Track, edit.Scene, edit.Revision, take.Rate, take.Channels)
	if err != nil {
		return "", err
	}
	for _, block := range take.Blocks {
		pcm := make([][]float32, take.Channels)
		for ch := range pcm {
			pcm[ch] = make([]float32, block.Timing.Frames)
			for frame := range pcm[ch] {
				at := block.Offset + (frame*take.Channels+ch)*4
				pcm[ch][frame] = math.Float32frombits(binary.LittleEndian.Uint32(take.PCM[at:]))
			}
		}
		record := capture.RecordedBlock{Timing: block.Timing, RawFrame: block.RawFrame, Placement: capture.Place(block.Timing)}
		if err := s.takes.Write(id, record, pcm); err != nil {
			return id, err
		}
	}
	// The worker may finish with a lost trailing interval. Save its duration as
	// explicitly invalid silence, so the published asset never closes that gap.
	last := take.Blocks[len(take.Blocks)-1]
	for cursor < take.RawFrames {
		frames := int(min(uint64(2048), take.RawFrames-cursor))
		b := last.Timing
		b.EngineFrame += int64(cursor - last.RawFrame)
		b.DeviceFrame += cursor - last.RawFrame
		b.Frames, b.Period, b.GapFrames, b.Flags = frames, frames, 0, capture.InvalidBlock
		pcm := make([][]float32, take.Channels)
		for ch := range pcm {
			pcm[ch] = make([]float32, frames)
		}
		if err := s.takes.Write(id, capture.RecordedBlock{Timing: b, RawFrame: cursor, Placement: capture.Place(b)}, pcm); err != nil {
			return id, err
		}
		cursor += uint64(frames)
	}
	if err := s.takes.Finalize(id, take.Incomplete || take.RawFrames > last.RawFrame+uint64(last.Timing.Frames)); err != nil {
		return id, err
	}
	if err := s.takes.Publish(id); err != nil {
		return id, err
	}
	return id, s.commitTake(id, edit.Revision, nil)
}

// auditionTake renders one bounded voice on the host. The browser only plays
// these already-pitched samples; B owns pitch conversion and region playback.
func (s *studio) auditionTake(w http.ResponseWriter, edit studioEdit) error {
	if edit.Sample == nil || edit.Sample.Note < 0 || edit.Sample.Note > 127 {
		return errors.New("audition requires a root and MIDI note from 0 to 127")
	}
	take, err := s.takes.Get(edit.TakeID)
	if err != nil {
		return err
	}
	start := take.StartFrame()
	region, err := sampleasset.LoadRegion(takeRoot(s.path), take.Asset, start, 0, edit.Sample.Root, edit.Sample.Loop)
	if err != nil {
		return err
	}
	voice, err := sample.New(48000, region)
	if err != nil {
		return err
	}
	if err := voice.NoteOn(uint8(edit.Sample.Note), 127); err != nil {
		return err
	}
	frames := int(math.Ceil(float64(region.End-region.Start) / voice.Ratio()))
	if !edit.Sample.Loop {
		frames += 48000 / 500 // retain B's two-millisecond one-shot release
	}
	if frames > sampleasset.MaxFrames {
		return fmt.Errorf("audition exceeds %d output frames", sampleasset.MaxFrames)
	}
	data := make([]byte, 44+frames*8)
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:], uint32(len(data)-8))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 3)
	binary.LittleEndian.PutUint16(data[22:], 2)
	binary.LittleEndian.PutUint32(data[24:], 48000)
	binary.LittleEndian.PutUint32(data[28:], 48000*8)
	binary.LittleEndian.PutUint16(data[32:], 8)
	binary.LittleEndian.PutUint16(data[34:], 32)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], uint32(frames*8))
	for i := 0; i < frames; i++ {
		left, right := voice.NextStereo()
		binary.LittleEndian.PutUint32(data[44+i*8:], math.Float32bits(left))
		binary.LittleEndian.PutUint32(data[48+i*8:], math.Float32bits(right))
	}
	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Cache-Control", "no-store")
	_, err = w.Write(data)
	return err
}
