// Package modulate evaluates the control sources of spec 24, lane 1: LFOs,
// sway, drawn contours and envelope followers. Every function here is pure
// integer arithmetic on the song position, so the same position gives the same
// value on amd64, arm64 and wasm, whatever the host buffer, and a seek needs no
// replay. The package imports no math library; the sine table is a checked
// constant.
//
// Values are Q30 fixed point: One (1<<30) is +1.0 and -One is -1.0. Phases are
// Q32 fractions of a cycle. A source's position is the whole cycle count and
// the phase inside that cycle.
package modulate

import "math/bits"

// GridFrames is the distance in song samples between control grid points.
const GridFrames = 128

// One is +1.0 in Q30.
const One = 1 << 30

// Position is an exact point on a source's own time axis: how many whole
// cycles have passed and how far into the current one, as a Q32 fraction.
type Position struct {
	Cycle uint64
	Phase uint32
}

// PositionOf returns floor(n/d) and the Q32 fraction of n/d, where n is the
// 128-bit value hi:lo. It reports false if the quotient does not fit 64 bits.
func PositionOf(hi, lo, d uint64) (Position, bool) {
	if d == 0 {
		return Position{}, false
	}
	if hi/d != 0 {
		return Position{}, false
	}
	q, rem := bits.Div64(hi%d, lo, d)
	return Position{Cycle: q, Phase: frac32(rem, d)}, true
}

// frac32 returns floor(rem * 2^32 / d) for rem < d.
func frac32(rem, d uint64) uint32 {
	hi, lo := bits.Mul64(rem, 1<<32)
	q, _ := bits.Div64(hi, lo, d)
	return uint32(q)
}

// SyncedPosition places a tempo-synced source. The song position is the exact
// tick (the quotient of kernel/seq.Clock.TickAt) plus remNum/remDen of the next
// tick (its remainder), and period is the source's cycle length in ticks.
// remDen is the clock's 60000*sampleRate. The remainder is truncated to a Q32
// fraction of a tick before the phase is taken, so the phase is
// floor((tick mod period)*2^32 + floor(2^32*remNum/remDen)) / period).
func SyncedPosition(tick, remNum, remDen, period uint64) Position {
	if period == 0 || remDen == 0 {
		return Position{}
	}
	cycle, inside := tick/period, tick%period
	fraction := uint64(frac32(remNum%remDen, remDen))
	numerator := inside<<32 | fraction // inside < 2^31 by the period cap
	return Position{Cycle: cycle, Phase: uint32(numerator / period)}
}

// FreePosition places a free-rate source from the song sample. microHz is the
// rate in millionths of a hertz.
func FreePosition(sample uint64, sampleRate uint32, microHz uint64) Position {
	hi, lo := bits.Mul64(sample, microHz)
	p, _ := PositionOf(hi, lo, uint64(sampleRate)*1_000_000)
	return p
}

// FitPosition places a source whose cycles are fitted to a loop: the loop of
// length loopLen (in any unit) holds exactly cycles cycles, and at is the
// position inside the loop in the same unit, with the fraction atNum/atDen.
// The position at loopLen is cycles whole cycles from 0. Periodic waveforms
// therefore repeat; random waveforms must also wrap their hash indices using
// LFOFitted or SwayFitted to repeat at the song boundary.
func FitPosition(at, atNum, atDen, loopLen, cycles uint64) Position {
	if loopLen == 0 || atDen == 0 {
		return Position{}
	}
	f := uint64(frac32(atNum%atDen, atDen))
	// (at + f/2^32) * cycles / loopLen
	hi, lo := bits.Mul64(at, cycles)
	fh, fl := bits.Mul64(f, cycles)
	// f*cycles is a Q32 amount: add (fh:fl)>>32 into the integer part and keep the low 32 bits.
	carry := fl>>32 | fh<<32
	lo2, c := bits.Add64(lo, carry, 0)
	hi += c
	lowFraction := uint64(uint32(fl))
	// Divide the integer part, then bring the leftover and the sub-unit fraction along.
	q, rem := bits.Div64(hi%loopLen, lo2, loopLen)
	// phase = (rem + lowFraction/2^32) / loopLen as Q32
	h2, l2 := bits.Mul64(rem, 1<<32)
	l2, c = bits.Add64(l2, lowFraction, 0)
	h2 += c
	phase, _ := bits.Div64(h2%loopLen, l2, loopLen)
	return Position{Cycle: q + hi/loopLen, Phase: uint32(phase)}
}

// Offset returns the position advanced by a Q32 phase offset, carrying into
// the cycle count.
func (p Position) Offset(phase uint32) Position {
	sum := uint64(p.Phase) + uint64(phase)
	return Position{Cycle: p.Cycle + sum>>32, Phase: uint32(sum)}
}

// Scale returns the position on an axis that runs 2^octave times faster.
func (p Position) Scale(octave uint) Position {
	if octave == 0 {
		return p
	}
	return Position{Cycle: p.Cycle<<octave | uint64(p.Phase)>>(32-octave), Phase: p.Phase << octave}
}

// Hash is the stateless SplitMix64 family of seq.ProbabilityHit with a domain
// byte in the free top bits (spec 24, "Seeding"). Domain 0 with a 32-bit seed
// reproduces the chance decision's input exactly.
func Hash(seed uint32, domain, track, slot, step uint8, index uint64) uint64 {
	x := uint64(seed) ^ uint64(domain)<<56 ^ uint64(track)<<32 ^ uint64(slot)<<40 ^ uint64(step)<<48
	x ^= index * 0x9e3779b97f4a7c15
	x += 0x9e3779b97f4a7c15
	x = (x ^ x>>30) * 0xbf58476d1ce4e5b9
	x = (x ^ x>>27) * 0x94d049bb133111eb
	return x ^ x>>31
}

// draw maps a hash to a symmetric Q30 value in [-One+1, One-1].
func draw(h uint64) int32 {
	u := int64(h >> 34) // 30 bits
	return int32(2*u - (One - 1))
}

// smooth is smoothstep on a Q32 fraction; the result is a Q32 fraction.
func smooth(t uint32) uint64 {
	tt := uint64(t)
	s := tt * tt >> 32
	return s * (3<<32 - 2*tt) >> 32
}

// lerp returns a + (b-a)*t for a Q32 fraction t in [0, 2^32].
func lerp(a, b int32, t uint64) int32 {
	return a + int32((int64(b)-int64(a))*int64(t)>>32)
}
