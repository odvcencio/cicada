// Package gosxhost runs a Cicada score as game music inside a GoSX
// AudioWorklet.
//
// GoSX hosts a generic WASM audio kernel (package game/audio/kernel in
// m31labs.dev/gosx); Cicada supplies the kernel (make build-kernel-wasm), the
// score artifact (cicada game-score) and the pieces in this package: the
// kernel's record layout for the host (Backend), the score artifact reader
// (Pack), and a scene and intensity controller (Music). The package imports
// nothing from GoSX, so GoSX does not depend on Cicada and Cicada does not
// depend on GoSX.
//
// This package is TinyGo-safe apart from encoding/json: a game's js/wasm
// build can import it to decode a score pack and drive the music.
package gosxhost

import "encoding/json"

// Backend describes the Cicada kernel to a GoSX worklet host. Its JSON form is
// the same shape as worklet.Backend in m31labs.dev/gosx/game/audio/worklet, so
// a game decodes BackendJSON straight into that type:
//
//	var backend worklet.Backend
//	_ = json.Unmarshal(gosxhost.BackendJSON(), &backend)
type Backend struct {
	ExportPrefix          string             `json:"prefix,omitempty"`
	MaxFrames             int                `json:"maxFrames"`
	Channels              int                `json:"channels"`
	CommandBytes          int                `json:"commandBytes"`
	MessageBytes          int                `json:"messageBytes"`
	MessageCapacity       int                `json:"messageCapacity"`
	SupportedCapabilities uint32             `json:"supportedCapabilities"`
	SharedBank            bool               `json:"sharedBank,omitempty"`
	FaultOpcode           uint8              `json:"faultOpcode,omitempty"`
	FaultArgumentOffset   int                `json:"faultArgumentOffset,omitempty"`
	OpcodeCapabilities    []OpcodeCapability `json:"opcodeCapabilities,omitempty"`
	Transport             *Transport         `json:"transport,omitempty"`
}

// OpcodeCapability names the kernel capability bits an opcode needs.
type OpcodeCapability struct {
	Opcode uint8  `json:"opcode"`
	Mask   uint32 `json:"mask"`
}

// Transport describes the command fields the host needs to swap a score on a
// bar line. Offsets are byte offsets inside a 24-byte command record.
type Transport struct {
	Play               uint8   `json:"play"`
	Stop               uint8   `json:"stop"`
	Seek               uint8   `json:"seek"`
	Target             uint8   `json:"target"`
	TicksPerBeat       int     `json:"ticksPerBeat"`
	TicksPerBar        int     `json:"ticksPerBar"`
	SeekBarOffset      int     `json:"seekBarOffset"`
	SeekTickOffset     int     `json:"seekTickOffset"`
	BoundaryTickOffset int     `json:"boundaryTickOffset"`
	QuantizedOpcodes   []uint8 `json:"quantizedOpcodes,omitempty"`
}

// Kernel capability bits the host negotiates (see docs/manual/engine-host.md).
const (
	capChords         = 1 << 0
	capSpatial        = 1 << 17
	capSharedControls = 1 << 22
	// SupportedCapabilities is every capability bit the host may negotiate.
	SupportedCapabilities = 0x7fffff
)

// Command and message layout of the kernel ABI (kernel/cmd).
const (
	CommandBytes    = 24
	MessageBytes    = 16
	MessageCapacity = 512
	// MaxFrames is the block size the kernel renders per call. The worklet
	// splits each 128-frame browser quantum into blocks of at most this size.
	MaxFrames = 128
	// TicksPerBeat and TicksPerBar are the kernel's transport resolution. The
	// GoSX worklet keeps its own bar clock from TicksPerBar, so it assumes 4/4
	// until the descriptor carries the meter map (time-signatures phase 6).
	TicksPerBeat = 960
	TicksPerBar  = 3840
)

// DefaultBackend returns the layout of the Cicada kernel built by
// make build-kernel-wasm.
func DefaultBackend() Backend {
	return Backend{
		MaxFrames:             MaxFrames,
		Channels:              2,
		CommandBytes:          CommandBytes,
		MessageBytes:          MessageBytes,
		MessageCapacity:       MessageCapacity,
		SupportedCapabilities: SupportedCapabilities,
		SharedBank:            true,
		// Message kind 7 is Fault; the fault code is the 16-bit word at byte 2
		// (field A of the 16-byte record).
		FaultOpcode:         7,
		FaultArgumentOffset: 2,
		OpcodeCapabilities: []OpcodeCapability{
			{Opcode: 22, Mask: capChords},
			{Opcode: 26, Mask: capSpatial},
			{Opcode: 27, Mask: capSpatial},
			{Opcode: 28, Mask: capSpatial},
			{Opcode: 32, Mask: capSharedControls},
			{Opcode: 33, Mask: capSharedControls},
		},
		Transport: &Transport{
			Play: 1, Stop: 2, Seek: 3,
			Target:       255,
			TicksPerBeat: TicksPerBeat, TicksPerBar: TicksPerBar,
			SeekBarOffset: 4, SeekTickOffset: 8, BoundaryTickOffset: 16,
			// OpLaunchScene (10) and OpSelectPattern (9) wait for the next bar
			// while a replacement score is pending.
			QuantizedOpcodes: []uint8{9, 10},
		},
	}
}

// BackendJSON returns DefaultBackend as JSON.
func BackendJSON() []byte {
	data, err := json.Marshal(DefaultBackend())
	if err != nil {
		panic(err) // a fixed struct always marshals
	}
	return data
}
