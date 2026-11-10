// Package analysis measures the master output for display: a 64-band
// spectrum and a stereo phase correlation. The code is pure Go with no
// goroutines, clocks or randomness, and it does not allocate after
// construction. A host feeds it from a measurement thread, never from the
// audio callback. kernel/engine does not import this package, so the kernel
// WASM build is unchanged.
//
// The radix-2 FFT is adapted from host/scoreaudition/fft.go (same
// bit-reversal and butterfly layout), reduced to a single in-place plan with
// a periodic Hann window.
package analysis
