//go:build !tinygo

package engine

// sampleChordsEnabled lets sampler and pack tracks start every note of a chord
// step. Portable images refuse sampler chords, so TinyGo builds keep the
// single-note path.
const sampleChordsEnabled = true
