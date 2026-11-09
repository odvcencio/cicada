// Package measure is Cicada's engine measurement suite (roadmap item E1).
//
// Everything that does work lives in _test.go files, so nothing here links
// into a build. Run the suite with "make test-engine-measurements". It
// renders every voice family and effect through its public constructors at
// 48 kHz with fixed seeds, writes build/engine-measurements.json, and fails
// when a gated metric is worse than its limit in testdata/tolerances.json.
// README.md explains how to read the JSON and how to change a limit.
//
// # Metrics
//
// All levels are in decibels. A value of -200 means "zero, or too small to
// matter"; JSON cannot hold minus infinity, so every dB result is clamped
// to -200. For every gated metric, a lower number is better.
//
// aliasing_db: take a sustained high note (C7 = MIDI 96 and C8 = MIDI 108),
// skip the first 0.25 s, and take 32768 samples (0.68 s) through a
// four-term Blackman-Harris window. Find the fundamental, then mark the
// bins within 8 bins of each expected partial (for a harmonic voice these
// are multiples of the fundamental; the piano uses its own stretched-partial
// formula). The metric is the total power in all other bins from 20 Hz up,
// divided by the total power in the marked bins. Energy outside the partial
// set is what a folded harmonic looks like. It also counts designed
// detune, drift, and noise, so voices with those are measured in a
// documented "alias stress" setting with them switched off. Voices whose
// spectrum is not harmonic by design (drums, noise, struck bars, tine
// bells) record "not_applicable" with the reason. The worst note is gated.
//
// thdn_db: total harmonic distortion plus noise. Feed a 1 kHz sine at -18,
// -6 and 0 dBFS (peak) into the effect with its default controls, let it
// settle, then fit a sine of that exact frequency over 400 whole periods
// (19200 samples) by least squares. The metric is the power of everything
// left (harmonics, noise, DC, modulation sidebands) divided by the power of
// the fitted fundamental. The worst of the three levels is gated; each level
// is in the detail map. For modulated effects (chorus, tape, reverb) the
// metric includes the designed modulation sidebands.
//
// frequency_response_error_db: for filters and the equalizer, drive a sine
// at documented points, fit the output amplitude by least squares, and
// compare the gain in dB with the published design curve (for example the
// RBJ cookbook biquad, or the exact discrete response of a one-pole
// section). The metric is the largest absolute difference. Each point is in
// the "points" list.
//
// noise_floor_dbfs: feed digital silence for 1 s (idle voices: trigger no
// note) and report the RMS of the output over both channels, in dB relative
// to full scale (1.0).
//
// cpu_ns_per_block: native time to render or process one 128-frame stereo
// block of one voice or effect instance, median of 9 runs of 200 blocks.
// Voices play MIDI note 60 at velocity 100. Effects process a -12 dBFS
// 1 kHz sine. This metric is informational and never gated, because it
// depends on the machine and its load.
package measure
