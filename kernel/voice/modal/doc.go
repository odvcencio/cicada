// Package modal provides bounded, struck-resonator percussion voices.
//
// The profiles are mathematical approximations of bars, membranes and plates,
// rather than sampled instruments. A finite contact excites damped complex
// resonators. Velocity changes contact time and upper-mode balance; four
// repeatable strike positions vary timbre without changing the modal poles.
// Modes at or above 0.45 times the sample rate are omitted. The render path is
// linear after excitation and allocates no memory.
//
// One Voice holds at most MaxVoices ringing strikes. NoteOff leaves a natural
// tail except for Vibraphone, whose damper shortens the tail. A slide retunes
// the latest strike while preserving its state and without another contact.
// These behaviours are candidates for listening evaluation, not a claim of
// spectral or perceptual equivalence to recorded instruments.
package modal
