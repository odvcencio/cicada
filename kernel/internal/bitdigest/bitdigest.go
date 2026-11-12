// Package bitdigest builds exact test signals and SHA-256 digests of
// floating-point bits. Tests use it to pin a processor's output so that amd64,
// arm64 and WebAssembly must produce the same bits. Only tests import it.
package bitdigest

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"math"
)

// Noise is a 32-bit xorshift generator of exact float32 noise.
type Noise struct{ state uint32 }

// NewNoise returns a generator; a zero seed is replaced so the sequence is
// never stuck at zero.
func NewNoise(seed uint32) *Noise {
	if seed == 0 {
		seed = 0x9e3779b9
	}
	return &Noise{state: seed}
}

// Next returns noise in [-0.5, 0.5) scaled by 2^-level, for level 0 to 23. The
// value is a 24-bit integer divided by a power of two, so it is exact on every
// target.
func (n *Noise) Next(level int) float32 {
	x := n.state
	x ^= x << 13
	x ^= x >> 17
	x ^= x << 5
	n.state = x
	return float32(int32(x)>>(8+level)) / (1 << 24)
}

// programLevels is the level, in steps of 6 dB below -6 dBFS, of each block of
// Program: loud, quiet, silent and rising passages, so dynamics processors
// attack, hold and release.
var programLevels = [...]int{0, 0, 3, 8, 24, 1, 0, 6, 12, 2, 24, 24, 4, 1, 0, 0, 9, 5, 3, 1}

// ProgramBlock is the length in frames of one level of Program.
const ProgramBlock = 2048

// Program returns frame i of an exact stereo test programme: independent noise
// in each channel whose level changes every ProgramBlock frames, with silent
// blocks (level 24).
func Program(left, right *Noise, i int) (float32, float32) {
	level := programLevels[(i/ProgramBlock)%len(programLevels)]
	if level >= 24 {
		left.Next(0)
		right.Next(0)
		return 0, 0
	}
	return left.Next(level), right.Next(level + i/ProgramBlock%2)
}

// Digest accumulates the exact bits of float32 and float64 values.
type Digest struct {
	h    hash.Hash
	word [8]byte
}

// New returns an empty digest.
func New() *Digest { return &Digest{h: sha256.New()} }

// Float32 adds each value's bits.
func (d *Digest) Float32(values ...float32) {
	for _, v := range values {
		binary.LittleEndian.PutUint32(d.word[:4], math.Float32bits(v))
		d.h.Write(d.word[:4])
	}
}

// Float64 adds each value's bits.
func (d *Digest) Float64(values ...float64) {
	for _, v := range values {
		binary.LittleEndian.PutUint64(d.word[:], math.Float64bits(v))
		d.h.Write(d.word[:])
	}
}

// Int adds each value.
func (d *Digest) Int(values ...int) {
	for _, v := range values {
		binary.LittleEndian.PutUint64(d.word[:], uint64(v))
		d.h.Write(d.word[:])
	}
}

// Uint32 adds each value as 8 little-endian bytes, zero-extended: the bytes
// Int writes for the same value where int has 64 bits. Use it for uint32
// state, which int(v) would turn negative where int has 32 bits
// (WebAssembly built with TinyGo).
func (d *Digest) Uint32(values ...uint32) {
	for _, v := range values {
		binary.LittleEndian.PutUint64(d.word[:], uint64(v))
		d.h.Write(d.word[:])
	}
}

// Hex returns the digest so far as lowercase hexadecimal.
func (d *Digest) Hex() string { return hex.EncodeToString(d.h.Sum(nil)) }
