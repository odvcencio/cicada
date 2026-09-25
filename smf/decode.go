package smf

import (
	"encoding/binary"
	"fmt"
	"io"
)

type openNote struct {
	tick int64
	vel  uint8
}

// Decode reads type 0 or 1 SMF and returns normalized note intervals. It
// preserves non-name metadata, including tempo and key signature events.
func Decode(reader io.Reader) (File, error) {
	var file File
	const limit = 64 << 20
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return file, err
	}
	if len(data) > limit || len(data) < 14 || string(data[:4]) != "MThd" || binary.BigEndian.Uint32(data[4:8]) != 6 {
		return file, fmt.Errorf("invalid MIDI header or file exceeds 64 MiB")
	}
	format := binary.BigEndian.Uint16(data[8:10])
	file.Format = format
	count := int(binary.BigEndian.Uint16(data[10:12]))
	file.PPQ = binary.BigEndian.Uint16(data[12:14])
	if format > 1 || count < 1 || count > 256 || format == 0 && count != 1 || file.PPQ == 0 || file.PPQ&0x8000 != 0 {
		return File{}, fmt.Errorf("unsupported MIDI format, track count, or time division")
	}
	position := 14
	file.Tracks = make([]TrackChunk, count)
	for i := 0; i < count; i++ {
		if position+8 > len(data) || string(data[position:position+4]) != "MTrk" {
			return File{}, fmt.Errorf("missing MIDI track %d", i)
		}
		length := int(binary.BigEndian.Uint32(data[position+4 : position+8]))
		position += 8
		if length > len(data)-position {
			return File{}, fmt.Errorf("truncated MIDI track %d", i)
		}
		track, tempo, err := decodeTrack(data[position:position+length], uint8(i))
		if err != nil {
			return File{}, fmt.Errorf("track %d: %w", i, err)
		}
		file.Tracks[i] = track
		if tempo != 0 && file.TempoMicros == 0 {
			file.TempoMicros = tempo
		}
		position += length
	}
	if position != len(data) {
		return File{}, fmt.Errorf("bytes remain after MIDI tracks")
	}
	return file, nil
}

func decodeTrack(data []byte, trackIndex uint8) (TrackChunk, uint32, error) {
	var track TrackChunk
	var active [16][128][]openNote
	var tick int64
	var running byte
	var tempo uint32
	position := 0
	ended := false
	for position < len(data) {
		delta, err := readVLQ(data, &position)
		if err != nil {
			return track, 0, err
		}
		tick += delta
		if position >= len(data) {
			return track, 0, fmt.Errorf("truncated MIDI event")
		}
		status := data[position]
		if status&0x80 != 0 {
			position++
			if status < 0xf0 {
				running = status
			} else {
				running = 0
			}
		} else {
			if running == 0 {
				return track, 0, fmt.Errorf("data byte without running status")
			}
			status = running
		}
		if status == 0xff {
			if position >= len(data) {
				return track, 0, fmt.Errorf("truncated meta event")
			}
			kind := data[position]
			position++
			length, err := readVLQ(data, &position)
			if err != nil || length > int64(len(data)-position) {
				return track, 0, fmt.Errorf("invalid meta event length")
			}
			payload := append([]byte(nil), data[position:position+int(length)]...)
			position += int(length)
			switch kind {
			case 0x03:
				track.Name = string(payload)
			case 0x2f:
				if len(payload) != 0 || position != len(data) {
					return track, 0, fmt.Errorf("invalid end-of-track event")
				}
				track.EndTick = tick
				ended = true
			case 0x51:
				if len(payload) != 3 {
					return track, 0, fmt.Errorf("invalid tempo event")
				}
				if tempo == 0 {
					tempo = uint32(payload[0])<<16 | uint32(payload[1])<<8 | uint32(payload[2])
				}
				track.Meta = append(track.Meta, MetaEvent{Tick: tick, Type: kind, Data: payload})
			default:
				track.Meta = append(track.Meta, MetaEvent{Tick: tick, Type: kind, Data: payload})
			}
			if ended {
				break
			}
			continue
		}
		if status == 0xf0 || status == 0xf7 {
			length, err := readVLQ(data, &position)
			if err != nil || length > int64(len(data)-position) {
				return track, 0, fmt.Errorf("invalid SysEx length")
			}
			position += int(length)
			continue
		}
		if status < 0x80 || status > 0xef {
			return track, 0, fmt.Errorf("unsupported MIDI status %02x", status)
		}
		kind, channel := status&0xf0, status&0x0f
		needed := 2
		if kind == 0xc0 || kind == 0xd0 {
			needed = 1
		}
		if position+needed > len(data) {
			return track, 0, fmt.Errorf("truncated channel event")
		}
		first := data[position]
		var second byte
		if needed == 2 {
			second = data[position+1]
		}
		if first&0x80 != 0 || needed == 2 && second&0x80 != 0 {
			return track, 0, fmt.Errorf("invalid channel data byte")
		}
		position += needed
		if kind == 0x90 && second != 0 {
			active[channel][first] = append(active[channel][first], openNote{tick: tick, vel: second})
		} else if kind == 0x80 || kind == 0x90 {
			queue := active[channel][first]
			if len(queue) == 0 {
				return track, 0, fmt.Errorf("note-off without note-on")
			}
			on := queue[0]
			active[channel][first] = queue[1:]
			if tick <= on.tick {
				return track, 0, fmt.Errorf("nonpositive note duration")
			}
			track.Notes = append(track.Notes, Note{Track: trackIndex, Tick: on.tick, Dur: tick - on.tick, Note: first, Vel: on.vel, Chan: channel})
		}
	}
	if !ended {
		return track, 0, fmt.Errorf("missing end-of-track event")
	}
	for channel := range active {
		for pitch := range active[channel] {
			if len(active[channel][pitch]) != 0 {
				return track, 0, fmt.Errorf("unclosed note")
			}
		}
	}
	return track, tempo, nil
}

func readVLQ(data []byte, position *int) (int64, error) {
	var value int64
	for i := 0; i < 4; i++ {
		if *position >= len(data) {
			return 0, fmt.Errorf("truncated variable-length quantity")
		}
		b := data[*position]
		*position++
		value = value<<7 | int64(b&0x7f)
		if b&0x80 == 0 {
			return value, nil
		}
	}
	return 0, fmt.Errorf("variable-length quantity exceeds four bytes")
}
