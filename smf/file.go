// Package smf reads and writes Standard MIDI Files for Cicada interchange.
package smf

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"sort"
)

type Note struct {
	Track uint8
	Tick  int64
	Dur   int64
	Note  uint8
	Vel   uint8
	Chan  uint8 // zero-based MIDI channel; drums use 9
}

type MetaEvent struct {
	Tick int64
	Type byte
	Data []byte
}

type TrackChunk struct {
	Name    string
	Notes   []Note
	Meta    []MetaEvent
	EndTick int64
}

type File struct {
	Format      uint16
	PPQ         uint16
	TempoMicros uint32
	Tracks      []TrackChunk
}

type wireEvent struct {
	tick     int64
	priority int
	data     []byte
}

// Encode writes SMF type 1 with explicit note-off messages and one chunk per
// File track. Equal-time note-offs precede note-ons.
func Encode(file File, writer io.Writer) error {
	if file.PPQ == 0 || file.PPQ > 0x7fff || len(file.Tracks) == 0 || len(file.Tracks) > 0xffff {
		return fmt.Errorf("invalid MIDI header")
	}
	var output bytes.Buffer
	output.WriteString("MThd")
	binary.Write(&output, binary.BigEndian, uint32(6))
	binary.Write(&output, binary.BigEndian, uint16(1))
	binary.Write(&output, binary.BigEndian, uint16(len(file.Tracks)))
	binary.Write(&output, binary.BigEndian, file.PPQ)
	for trackIndex, track := range file.Tracks {
		events := make([]wireEvent, 0, len(track.Notes)*2+len(track.Meta)+1)
		if track.Name != "" {
			events = append(events, wireEvent{tick: 0, priority: 0, data: metaBytes(0x03, []byte(track.Name))})
		}
		for _, meta := range track.Meta {
			if meta.Tick < 0 || meta.Type == 0x2f || len(meta.Data) > 0x0fffffff {
				return fmt.Errorf("track %d has invalid meta event", trackIndex)
			}
			events = append(events, wireEvent{tick: meta.Tick, priority: 0, data: metaBytes(meta.Type, meta.Data)})
		}
		for _, note := range track.Notes {
			if note.Tick < 0 || note.Dur < 1 || note.Tick > (1<<62)-note.Dur || note.Note > 127 || note.Vel < 1 || note.Vel > 127 || note.Chan > 15 {
				return fmt.Errorf("track %d has invalid note", trackIndex)
			}
			events = append(events,
				wireEvent{tick: note.Tick, priority: 2, data: []byte{0x90 | note.Chan, note.Note, note.Vel}},
				wireEvent{tick: note.Tick + note.Dur, priority: 1, data: []byte{0x80 | note.Chan, note.Note, 0}},
			)
		}
		sort.SliceStable(events, func(i, j int) bool {
			if events[i].tick != events[j].tick {
				return events[i].tick < events[j].tick
			}
			return events[i].priority < events[j].priority
		})
		var chunk bytes.Buffer
		var previous int64
		for _, event := range events {
			if err := appendVLQ(&chunk, event.tick-previous); err != nil {
				return fmt.Errorf("track %d: %w", trackIndex, err)
			}
			chunk.Write(event.data)
			previous = event.tick
		}
		end := max(previous, track.EndTick)
		if err := appendVLQ(&chunk, end-previous); err != nil {
			return fmt.Errorf("track %d: %w", trackIndex, err)
		}
		chunk.Write([]byte{0xff, 0x2f, 0})
		if uint64(chunk.Len()) > uint64(^uint32(0)) {
			return fmt.Errorf("MIDI track exceeds 4 GiB")
		}
		output.WriteString("MTrk")
		binary.Write(&output, binary.BigEndian, uint32(chunk.Len()))
		output.Write(chunk.Bytes())
	}
	n, err := writer.Write(output.Bytes())
	if err != nil {
		return err
	}
	if n != output.Len() {
		return io.ErrShortWrite
	}
	return nil
}

func metaBytes(kind byte, data []byte) []byte {
	var out bytes.Buffer
	out.WriteByte(0xff)
	out.WriteByte(kind)
	appendVLQ(&out, int64(len(data)))
	out.Write(data)
	return out.Bytes()
}

func appendVLQ(out *bytes.Buffer, value int64) error {
	if value < 0 || value > 0x0fffffff {
		return fmt.Errorf("MIDI delta exceeds variable-length quantity range")
	}
	var encoded [4]byte
	n := 1
	encoded[3] = byte(value & 0x7f)
	for value >>= 7; value != 0; value >>= 7 {
		n++
		encoded[4-n] = byte(value&0x7f) | 0x80
	}
	out.Write(encoded[4-n:])
	return nil
}
