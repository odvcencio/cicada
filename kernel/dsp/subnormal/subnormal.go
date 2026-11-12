// Package subnormal keeps decaying recursive state out of subnormal numbers.
//
// A recursive filter, resonator or envelope whose state decays by a factor
// close to 1 each sample reaches float32's subnormal range (magnitudes below
// 2^-126) and, with round-to-nearest, often stops decaying there: x*d rounds
// back to x once x is a few dozen subnormal steps. A smoothed control value
// (a pedal, a mix, a gain) that glides to a target of zero stops the same way.
// Most processors handle a subnormal operand or result many times slower than
// a normal one (an Intel core needs a microcode assist for each), so a ringing
// voice or an effect's tail gets slower the longer it rings, and stays slow in
// silence. WebAssembly has no flush-to-zero mode, and setting one on a native
// processor would make native and WebAssembly output differ, so this package
// flushes in code: Flush replaces a subnormal value with a zero of the same
// sign, the same way on every target.
//
// # What a flush can change
//
// That a flush leaves 16-bit and 24-bit output unchanged is measured and
// checked; it is not proven. Below are an argument that holds only under
// stated assumptions, the measurement, and the check.
//
// The argument, for one flush. Let v be the flushed value (|v| < 2^-126) and G
// the largest gain from that value to the processor's output through its
// linear and bounded nonlinear stages. In exact arithmetic the output moves by
// at most |v|*G < G*2^-126. Float32 addition absorbs a difference below half a
// unit in the last place of the other operand, so a value of magnitude
// 2^-102*G or more usually comes out identical. Two things break this:
//
//   - Rounding ties. When an exact sum lies halfway between two float32
//     values, a difference far below half a unit decides the rounding and
//     becomes a whole unit: 2^-86 + 2^-110 rounds to 2^-86 (to even), but
//     2^-86 + (2^-110 + 2^-133) rounds to 2^-86 + 2^-109. Each tie on the
//     path can multiply the size of a difference by up to 2^24.
//   - Float64 state. A value carried in float64 (the reverb, the delay, the
//     compressor, the drive, the pro limiter) absorbs a difference only below
//     2^-53 of its size, so the float32 magnitude bound does not carry through
//     those stages.
//
// After the processor the difference meets more gain: track and bus levels,
// the master input gain and the master gain (+24 dB each), insert and
// master-chain effects (EQ bands to +24 dB, kernel/fx/pro/eq.go; compressor
// makeup to +24 dB, kernel/fx/comp.go; drive to +36 dB, kernel/fx/drive.go),
// and peak normalization, whose gain is 0.89/peak (render.WAV refuses a range
// whose peak is below 2^-23, so that gain stays below 2^23). Call the whole
// factor G'. If G' <= 2^24 and no rounding tie lies on the path, a changed
// output sample is below 2^-78, and then:
//
//   - 16-bit and 24-bit PCM, as the WAV encoder writes them, are unchanged.
//     The encoder rounds x*(2^23-1) + d in float64, d being the dither. A
//     changed x below 2^-78 scales to below 2^-55. When |d| >= 1/4 that is
//     under half a unit in the last place of d (at least 2^-55), so the sum is
//     d either way; when |d| < 1/4, or there is no dither, both sums lie within
//     1/4 + 2^-55 of zero and round to zero.
//   - float32 output (32-bit WAV, stems, the live engine's float buffers) can
//     differ only in samples smaller than 2^-78, about -470 dBFS.
//
// Neither assumption is guaranteed: stacked inserts at their limits, the two
// master gains and normalization together exceed 2^24, a resonator is flushed
// again each time it re-enters the subnormal range, and no threshold can rule
// out a tie. So the argument shows why the margin is wide, not that it holds.
//
// The measurement, on amd64: the 146 example scores that need no sample packs,
// rendered with and without the flushes, gave byte-identical 24-bit and
// -14 LUFS WAVs, and the largest float32 difference was 1.8e-36 (2^-118.8),
// about 2^40 below the 2^-78 a 24-bit sample needs to move.
//
// The check: with the build tag cicada_noflush, Flush and Flush64 return
// their argument (flush_off.go). `make test-flush-identity` renders the WAV
// parity subset and three hostile scores (a sustain pedal pressed and
// released, a reverb send from a hard-left track, a normalized range that
// ends in a tail) with and without the tag, and fails unless every 24-bit
// file is byte-identical. CI runs it in the amd64 parity job.
//
// # Where the flushes run
//
// Flush adds a compare and a branch per call, which on arm64 (no subnormal
// penalty) is pure cost: a flush at every store made the electric piano 21%
// slower there and the reverb 11%. So a processor flushes its recursive
// states in a sweep every 256 of its own samples, at positions that depend
// only on its own sample count and input, the same on every target and at
// every block size; a decaying state then stays subnormal for at most 256
// samples. A delay line is swept over the slots written since the previous
// sweep, which works when every slot is read later than 256 samples after it
// is written. The reverb sweeps its delay lines during runs of silent input on
// both channels, since that state cannot decay that far while there is input,
// and each channel's input diffusers on that channel's own silent samples.
package subnormal

import "math"

// exponentMask selects a float32's exponent bits; they are all zero for zero
// and for subnormal numbers.
const exponentMask = 0x7f800000

// Is reports whether x is subnormal: nonzero with a zero exponent.
func Is(x float32) bool {
	bits := math.Float32bits(x)
	return bits&exponentMask == 0 && bits&0x7fffffff != 0
}
