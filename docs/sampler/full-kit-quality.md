# Full kit acceptance targets

Define the following gates before selecting or processing recordings. The reproducible measurements check playback behavior; A/B comparisons assess the supplied sounds.

Behavior references are [Studio Drummer's velocity and microphone controls](https://docs.native-instruments.com/ni-tech-manuals/studio-drummer-manual/en/the-performance-view), [Battery's hi-hat choke groups](https://support.native-instruments.com/support/solutions/articles/69000879959-how-to-set-up-battery-4-cells-for-hi-hat-choking), and [Kontakt's round-robin mapping](https://docs.native-instruments.com/ni-tech-manuals/kontakt-manual/en/classic-view). These describe behaviors to reproduce, not commercial recordings or a claim of equal sound quality.

| Behavior | Gate |
| --- | --- |
| Coverage | Kick, center snare, rimshot, cross-stick, three or more toms, closed/pedal/half-open/open hats, ride bow/bell/crash, crash and splash. Publish exact GM notes and extension notes. |
| Velocity and repetition | Prefer at least six recorded dynamic centers and three distinct recorded takes for main shells, at least four centers and two takes for cymbals. Report source-limited exceptions. Never create layers/takes by duplicating or processing a single hit. Validate complete cycles and every velocity 1–127. |
| Ghosts, flams, rolls | Quiet recorded center hits supply ghosts; score flams/rolls use independently cycled hits. Identify recorded rolls separately if provided. |
| Decay and room | One-shot shells/cymbals ignore note-off; keep the recorded decay and one coherent stereo microphone mix for the principal kit. Fade only the noise-floor ending; report source noise and supplemental-room differences. No drum legato or sustain loop. |
| Choking | Closed/pedal hats end open/half-open hats; explicit cymbal choke notes end their cymbal without choking unrelated pieces. Existing 2 ms bounded choke ramp is tested with this map. |
| Fidelity | At native pitch, after the onset ramp and before any documented tail conditioning, source-to-player residual <= -90 dB; spectral and 20 ms envelope comparisons use licensed source PCM. |
| Noise and resampling | Inactive/reset output is exact zero. Report source tail RMS, DC and peak. Existing sampler tests gate alias rejection >=80 dB, residual <=-90 dB and passband ripple <=0.05 dB at qualified ratios. |
| Runtime | Production sampler WASM timings at 48 kHz/128 frames, one/eight voices and one/two layers; report time per voice and p99, with no render-time memory growth. Node results are measurements, not target-device AudioWorklet qualification. |
| Distribution | All source and output files have SHA-256, source URL, license and attribution where required. External audio, <=256 MiB resident PCM per pack, unchanged core kernel <=300 KiB and optional sampler <=64 KiB. |
| Listening | Identical groove events/velocities/seeds/gain for starter VCSL and full kit; missing starter articulations use explicitly documented nearest-piece substitutions. Include rock, funk, reggae one-drop, soca and articulation/velocity demonstrations, plus a relative M3U. Verify every delivered WAV fully decodes. |

The starter kit has no toms, ride, crash or splash. The A/B substitution makes its restricted palette audible while preserving event timing; it cannot isolate only the sampler DSP. No proprietary DAW audio is used as a reference. The kit uses the prerequisite pinned-pack browser host and does not change Studio routing, modeled percussion, effects or convolution branches.
