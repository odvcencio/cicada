# Percussion and mix acceptance targets

These are engineering gates for candidate sounds. Acoustic evaluation is required before claiming instrument realism. Existing scores and goldens must retain their output unless they opt into a new processor or voice.

Reference behaviours: Ableton Live Collision models mallet hardness, velocity-dependent excitation, resonator decay and note-off damping; Logic Pro Sculpture offers velocity-sensitive excitation and independently controlled damping. These manuals establish behaviours, not a licensed audio reference or a claim of perceptual equivalence.

- https://www.ableton.com/en/manual/live-instrument-reference/#collision
- https://support.apple.com/guide/logicpro/objects-overview-lgsie2998a4f/mac

| Capability | Candidate acceptance gate |
| --- | --- |
| Velocity | RMS increases across velocities 32, 64, 96, 127; hard strikes increase upper-mode energy. Velocity zero is silent. |
| Repeated strikes | At least four deterministic excitation variations; reset reproduces the sequence. Variation must not detune the fundamental by more than 5 cents. |
| Pitch and bandwidth | Modal fundamental within 5 cents where audible; omit resonances above 0.45 times sample rate. Measure residual against a higher-rate reference where practical. |
| Decay and release | Stable decaying impulse tails; pitched percussion keeps a natural tail after key-up except vibraphone pedal damping. Slides preserve resonator energy rather than retriggering. |
| Noise | Reset silence is exactly zero; no unbounded denormal tail; finite output for validated controls. Record tail level and DC. |
| Reverb | Convolution matches direct convolution within 1e-5, block-boundary independent. Algorithmic tails remain stable, diffuse and decay. Real IRs have source/license/checksum manifests. |
| EQ and dynamics | EQ response within 0.25 dB at design frequency, transparent unity settings, linked stereo dynamics; measure gain, THD, silence and latency. |
| Mastering | Target integrated LUFS within 0.5 LU where gain/peak constraints allow; oversampled true peak at or below -1 dBTP. Report unreachable targets rather than hiding error. |
| CPU | No allocations in voice/effect render loops. Record native ns/sample and actual WASM time per voice/instance at 48 kHz, 128-frame blocks. Candidate goal: <10% of one 2.667 ms callback per voice/effect; this is hardware-dependent and must be reported. |
| Kernel | Raw production kernel <=307200 bytes. Report raw/Brotli deltas against main. Large IRs/samples remain fetchable assets, outside kernel. |
| Evidence | Every new voice/effect has a demo score or reproducible score-based audition, tests, and matched before/after WAVs. Reference recordings require redistribution licenses before inclusion. |

Modal profiles are mathematical approximations. Spectral matching to licensed instrument recordings, acoustic evaluation, and device deadline qualification are separate gates and must remain visible when incomplete.

The score voice kinds use the `model_` prefix (for example `model_steelpan`) so ordinary authored names remain available. Each track reserves four strike tails under the existing 32-voice ceiling. Default home octave is 4; octave 0–6 and ordinary track mixer settings are supported. These are struck voices: slide retunes the most recent sounding strike; other strikes continue ringing. They do not implement bowed sustain or a sample-library legato transition.

Image version 13 uses required capability bit 6 and voice kind 6 with a one-byte profile. Voice kind 4 is piano and voice kind 5 is guitar. Chords, delay, piano, neural amp, and PM use separate capability bits. Kernels without this capability reject modeled images before playback. Legacy images retain a zero capability word. Combining open branches must OR their capability masks rather than overwrite them.

A/B auditions use the same note patterns, velocity accents, tempo, seed, mixer gain, render rate and tails. Main has no corresponding modeled percussion: the before tracks use simple authored sine/envelope or filtered-noise/envelope proxies, rendered by the unmodified main binary. They are a reproducible legacy-graph baseline, not recordings of a former modeled voice. The after tracks select the new model. Auditions retain relative levels; no separate peak normalization conceals velocity or mix changes.
