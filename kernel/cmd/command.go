// Package cmd defines the fixed command and message ABI shared by native and
// WASM hosts. It has no host dependencies and performs no allocation.
package cmd

import (
	"math"
)

const (
	CommandSize = 24
	MessageSize = 16
)

type Error string

func (e Error) Error() string { return string(e) }

type Op uint8

const (
	OpPlay Op = iota + 1
	OpStop
	OpSeek
	OpSetTempo
	OpSetParam
	OpSetStep
	OpSetPatternLen
	OpSetPatternMeta
	OpSelectPattern
	OpLaunchScene
	OpSetChain
	OpNoteOn
	OpNoteOff
	OpCue
	OpSetLayerMask
	OpMeterRate
)

type Quantize uint32

type Command struct {
	Op    Op
	Track uint8
	Index uint16
	Arg0  uint32
	Arg1  uint32
	Pad   uint32
	Tick  int64
}

type Kind uint8

const (
	Playhead Kind = iota + 1
	Meter
	NoteOn
	NoteOff
	Switched
	Overload
	Fault
	Late
)

type Message struct {
	Kind  Kind
	Track uint8
	A     uint16
	B     uint32
	Tick  int64
}

// EncodeCommand returns the exact 24-byte little-endian wire record.
func EncodeCommand(c Command, tracks uint8) ([CommandSize]byte, error) {
	var data [CommandSize]byte
	if err := c.Validate(tracks); err != nil {
		return data, err
	}
	data[0], data[1] = byte(c.Op), c.Track
	put16(data[2:4], c.Index)
	put32(data[4:8], c.Arg0)
	put32(data[8:12], c.Arg1)
	put64(data[16:24], uint64(c.Tick))
	return data, nil
}

// DecodeCommand rejects malformed records before the engine receives them.
func DecodeCommand(data []byte, tracks uint8) (Command, error) {
	if len(data) != CommandSize {
		return Command{}, Error("command must be exactly 24 bytes")
	}
	c := Command{
		Op: Op(data[0]), Track: data[1], Index: get16(data[2:4]),
		Arg0: get32(data[4:8]), Arg1: get32(data[8:12]),
		Pad: get32(data[12:16]), Tick: int64(get64(data[16:24])),
	}
	return c, c.Validate(tracks)
}

// DecodeCommands validates a whole batch before writing any command to dst.
func DecodeCommands(data []byte, tracks uint8, dst []Command) (int, error) {
	if len(data)%CommandSize != 0 {
		return 0, Error("command batch has a partial record")
	}
	count := len(data) / CommandSize
	if count > len(dst) {
		return 0, Error("command batch exceeds destination capacity")
	}
	for i := 0; i < count; i++ {
		if _, err := DecodeCommand(data[i*CommandSize:(i+1)*CommandSize], tracks); err != nil {
			return 0, err
		}
	}
	for i := 0; i < count; i++ {
		dst[i], _ = DecodeCommand(data[i*CommandSize:(i+1)*CommandSize], tracks)
	}
	return count, nil
}

// Validate checks the opcode, routing, reserved bytes, and structural bounds.
// Parameter-specific ranges are checked by the host parameter registry.
func (c Command) Validate(tracks uint8) error {
	if tracks < 1 || tracks > 16 {
		return Error("track count must be 1 to 16")
	}
	if c.Op < OpPlay || c.Op > OpMeterRate {
		return Error("unknown command opcode")
	}
	if c.Pad != 0 || c.Tick < 0 {
		return Error("nonzero command padding or negative tick")
	}
	if globalOp(c.Op) {
		if c.Track != 0xff {
			return Error("global command requires track 255")
		}
	} else if c.Track >= tracks && !(c.Op == OpSetParam && c.Track == 0xff) {
		return Error("command track is out of range")
	}
	switch c.Op {
	case OpPlay, OpStop:
		if c.Index != 0 || c.Arg0 != 0 || c.Arg1 != 0 {
			return Error("transport command has unexpected payload")
		}
	case OpSeek:
		if c.Arg1 >= 3840 {
			return Error("seek tick must be within one bar")
		}
	case OpSetTempo:
		if c.Arg0 < 20_000 || c.Arg0 > 300_000 {
			return Error("tempo must be 20 to 300 BPM")
		}
	case OpSetParam:
		if math.IsNaN(float64(math.Float32frombits(c.Arg0))) || math.IsInf(float64(math.Float32frombits(c.Arg0)), 0) {
			return Error("parameter must be finite")
		}
	case OpSetStep:
		if c.Index >= 64 || c.Arg1 >= 16 || c.Arg0>>28 != 0 || c.Arg0>>14&0x7f > 100 || c.Arg0&(1<<10) != 0 && (c.Arg0&(1<<9) == 0 || c.Arg0>>11&7 != 0) {
			return Error("step or slot is out of range")
		}
	case OpSetPatternLen:
		if c.Index < 1 || c.Index > 64 || c.Arg1 >= 16 {
			return Error("pattern length or slot is out of range")
		}
	case OpSetPatternMeta:
		transpose := int16(c.Arg0 >> 16)
		if c.Arg0&0xffff > 500 || transpose < -24 || transpose > 24 || c.Arg1 >= 16 {
			return Error("pattern metadata is out of range")
		}
	case OpSelectPattern:
		if c.Index >= 16 || !validQuantize(c.Arg0) || c.Arg1 > 1 {
			return Error("pattern slot or quantize is out of range")
		}
	case OpLaunchScene, OpCue:
		if !validQuantize(c.Arg0) {
			return Error("quantize is out of range")
		}
	case OpSetChain:
		if c.Index >= 32 || c.Arg0>>16 != 0 || c.Arg0&0xff >= 16 || c.Arg0>>8&0xff == 0 {
			return Error("chain entry is out of range")
		}
	case OpNoteOn:
		if c.Arg0&0xff >= 128 || c.Arg0>>8&0xff >= 128 || c.Arg0>>18 != 0 {
			return Error("live note is out of range")
		}
	case OpSetLayerMask:
		if c.Arg0>>tracks != 0 {
			return Error("layer mask exceeds track count")
		}
	case OpMeterRate:
		if c.Arg0 == 0 {
			return Error("meter rate must be positive")
		}
	}
	return nil
}

func globalOp(op Op) bool {
	switch op {
	case OpPlay, OpStop, OpSeek, OpSetTempo, OpLaunchScene, OpCue, OpSetLayerMask, OpMeterRate:
		return true
	}
	return false
}

func validQuantize(value uint32) bool { return value <= 3 || value >= 5 && value <= 20 }

func EncodeMessage(m Message) [MessageSize]byte {
	var data [MessageSize]byte
	data[0], data[1] = byte(m.Kind), m.Track
	put16(data[2:4], m.A)
	put32(data[4:8], m.B)
	put64(data[8:16], uint64(m.Tick))
	return data
}

func DecodeMessage(data []byte) (Message, error) {
	if len(data) != MessageSize {
		return Message{}, Error("message must be exactly 16 bytes")
	}
	m := Message{Kind: Kind(data[0]), Track: data[1], A: get16(data[2:4]), B: get32(data[4:8]), Tick: int64(get64(data[8:16]))}
	if m.Kind < Playhead || m.Kind > Late {
		return Message{}, Error("unknown message kind")
	}
	return m, nil
}

func put16(dst []byte, value uint16) { dst[0], dst[1] = byte(value), byte(value>>8) }
func put32(dst []byte, value uint32) {
	for i := range 4 {
		dst[i] = byte(value >> (8 * i))
	}
}
func put64(dst []byte, value uint64) {
	for i := range 8 {
		dst[i] = byte(value >> (8 * i))
	}
}
func get16(src []byte) uint16 { return uint16(src[0]) | uint16(src[1])<<8 }
func get32(src []byte) uint32 {
	return uint32(src[0]) | uint32(src[1])<<8 | uint32(src[2])<<16 | uint32(src[3])<<24
}
func get64(src []byte) uint64 { return uint64(get32(src)) | uint64(get32(src[4:]))<<32 }
