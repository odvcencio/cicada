// Six-operator FM keyboard contract
//
// This package supplies original fm_ep, bell_keys and fm_bass patches. fm_ep
// uses three modulator/carrier pairs with a short high-ratio attack and gently
// detuned body carriers. bell_keys uses inharmonic modulation ratios and long
// decays. fm_bass combines a fundamental and suboctave with nested modulators.
// These are new designs; no manufacturer patch data or sample audio is shipped.
//
// Create a patch with Patch("fm_ep"), then call New(48000, params). NoteOn takes
// MIDI notes 21..108 and velocities 1..127; velocity zero calls NoteOff.
// NextStereo returns one frame. Eight slots use deterministic oldest-voice
// stealing, with released slots preferred and a 5 ms filtered steal tail.
// SetVoiceLimit selects 1..8 slots before playback; Reset preserves this limit.
// Retriggering a note restarts one slot. AllNotesOff releases keys; Reset also
// clears the sustain pedal and all filter history immediately. Pedal values
// >= 0.5 retain released notes until the pedal falls below that threshold.
//
// Params.Operators describes six sine operators. Routing[destination][source]
// accepts sources with a higher index, so operator 5 is evaluated first and
// operator 0 last. A route scales its source output in radians. Each operator
// also has a two-sample averaged feedback path. Params.Output selects audible
// carriers. Multiple carriers can be panned separately with Pan and
// StereoSpread. Operator ADSR envelopes have independent times and levels;
// Velocity changes both carrier volume and modulation depth. Decay and release
// times specify a 60 dB exponential fall, rather than a linear fall to zero.
// KeyTracking shortens decay and release smoothly in the upper register.
//
// New prepares all 88 keys, envelopes, a 2048-entry sine table and a symmetric
// 128-tap FIR. The network runs at twice the output sample rate. The decimator
// adds 31.75 output frames of group delay. Operator frequencies above 0.39
// times the output sample rate are muted; modulation and feedback receive a
// conservative Carson-style bandwidth guard. This intentionally reduces upper
// register brightness before the decimator stop band. FM has infinitely many
// mathematical sidebands: the guard and decimator suppress aliasing rather than
// claiming every nonzero sideband has been removed. The standalone quality
// tests measure decimator rejection and clean-carrier image/residual energy.
//
// Output rates are 44100, 48000, 96000 and 192000 Hz. Rendering, note events,
// sustain and Reset allocate nothing and call no transcendental math functions.
// Phase uses wrapping uint32 arithmetic. Products round explicitly to float32
// before additions so native FMA contraction cannot change rounding. All calls
// belong to one audio owner; this package provides no concurrent mutation.
//
// Reproduce native quality and CPU checks with:
//
//	GOWORK=off GOMAXPROCS=2 go test -p 1 ./kernel/voice/fm -v -bench BenchmarkStereo
//
// Reproduce native/TinyGo parity and zero WASM trigger/render allocations with:
//
//	GOWORK=off GOMAXPROCS=2 go test -p 1 -tags fm_wasm ./kernel/voice/fm -run TestFMNativeWASMDeterminism -v
//
// The parity fixture covers all three patches, all four rates, block sizes
// 64/128/256, voice limits 1/2/4/8, sustain, retriggering and stealing. The main native golden also
// uses block size 1. Listening tests and comparisons to recorded reference
// instruments are separate from these numerical tests.
package fm
