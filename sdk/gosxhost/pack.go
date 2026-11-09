package gosxhost

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"

	"m31labs.dev/cicada/sdk/director"
)

// Kernel flavors. A score needs the smallest kernel that has its features;
// cicada game-score picks one and records it in the pack.
const (
	KernelCore        = 0 // make build-kernel-wasm
	KernelVocals      = 1 // make build-vocals-wasm (track effects)
	KernelKeys        = 2 // make build-keys-wasm
	KernelModular     = 3 // make build-modular-wasm
	KernelKeysModular = 4 // make build-keys-modular-wasm
)

// KernelName returns the file name make writes for a kernel flavor.
func KernelName(flavor uint8) string {
	switch flavor {
	case KernelCore:
		return "cicada-kernel.wasm"
	case KernelVocals:
		return "cicada-vocals.wasm"
	case KernelKeys:
		return "cicada-keys.wasm"
	case KernelModular:
		return "cicada-modular.wasm"
	case KernelKeysModular:
		return "cicada-keys-modular.wasm"
	}
	return ""
}

const (
	packMagic   = "CICSCORE"
	packVersion = 1
	packHeader  = 32
)

// Pack is the score artifact a game ships: a compiled kernel image plus the
// control manifest that names the score's scenes (states), intensity macros
// and stingers. Both are tied to one sample rate.
type Pack struct {
	// Kernel is the flavor of kernel module the image needs.
	Kernel uint8
	// SampleRate is the AudioContext rate the pack was compiled for.
	SampleRate int
	// TempoMilli is the score's opening tempo in thousandths of a BPM.
	TempoMilli uint32
	// RequiredCapabilities is the kernel capability mask the image needs.
	RequiredCapabilities uint32
	// Image is the compiled kernel image (CIC1).
	Image []byte
	// Surface is the Game Director control manifest.
	Surface director.Surface
}

// Info returns the metadata the GoSX host needs next to the image. Its fields
// match worklet.ImageInfo.
func (p *Pack) Info() (tempoMilli int, requiredCapabilities uint32) {
	return int(p.TempoMilli), p.RequiredCapabilities
}

// Marshal encodes the pack: a 32-byte little-endian header, the manifest JSON,
// the image, and a CRC-32 (IEEE) of everything before it.
//
//	0  magic "CICSCORE"      8  version u16     10 kernel u8   11 reserved
//	12 sample rate u32       16 tempo milli u32 20 capabilities u32
//	24 manifest length u32   28 image length u32
func (p *Pack) Marshal() ([]byte, error) {
	if len(p.Image) < 32 || p.SampleRate <= 0 {
		return nil, errors.New("gosxhost: pack needs an image and a sample rate")
	}
	manifest, err := json.Marshal(p.Surface)
	if err != nil {
		return nil, err
	}
	out := make([]byte, packHeader, packHeader+len(manifest)+len(p.Image)+4)
	copy(out, packMagic)
	binary.LittleEndian.PutUint16(out[8:], packVersion)
	out[10] = p.Kernel
	binary.LittleEndian.PutUint32(out[12:], uint32(p.SampleRate))
	binary.LittleEndian.PutUint32(out[16:], p.TempoMilli)
	binary.LittleEndian.PutUint32(out[20:], p.RequiredCapabilities)
	binary.LittleEndian.PutUint32(out[24:], uint32(len(manifest)))
	binary.LittleEndian.PutUint32(out[28:], uint32(len(p.Image)))
	out = append(out, manifest...)
	out = append(out, p.Image...)
	return binary.LittleEndian.AppendUint32(out, crc32.ChecksumIEEE(out)), nil
}

// ParsePack decodes and checks a pack. The returned Image aliases data.
func ParsePack(data []byte) (*Pack, error) {
	if len(data) < packHeader+4 || string(data[:8]) != packMagic {
		return nil, errors.New("gosxhost: not a Cicada score pack")
	}
	if v := binary.LittleEndian.Uint16(data[8:]); v != packVersion {
		return nil, fmt.Errorf("gosxhost: unsupported pack version %d", v)
	}
	body, sum := data[:len(data)-4], binary.LittleEndian.Uint32(data[len(data)-4:])
	if crc32.ChecksumIEEE(body) != sum {
		return nil, errors.New("gosxhost: score pack is corrupt (checksum mismatch)")
	}
	manifestLen := uint64(binary.LittleEndian.Uint32(data[24:]))
	imageLen := uint64(binary.LittleEndian.Uint32(data[28:]))
	if packHeader+manifestLen+imageLen != uint64(len(body)) {
		return nil, errors.New("gosxhost: score pack lengths do not match its size")
	}
	p := &Pack{
		Kernel:               data[10],
		SampleRate:           int(binary.LittleEndian.Uint32(data[12:])),
		TempoMilli:           binary.LittleEndian.Uint32(data[16:]),
		RequiredCapabilities: binary.LittleEndian.Uint32(data[20:]),
	}
	if err := json.Unmarshal(data[packHeader:packHeader+manifestLen], &p.Surface); err != nil {
		return nil, fmt.Errorf("gosxhost: manifest: %w", err)
	}
	if err := p.Surface.Validate(); err != nil {
		return nil, err
	}
	if p.Surface.SampleRate != p.SampleRate {
		return nil, errors.New("gosxhost: manifest and pack sample rates differ")
	}
	p.Image = data[packHeader+manifestLen : len(body)]
	if len(p.Image) < 32 || binary.BigEndian.Uint32(p.Image) != 0x43494331 {
		return nil, errors.New("gosxhost: pack image has no CIC1 header")
	}
	return p, nil
}
