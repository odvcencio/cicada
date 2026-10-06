# Modeled drum kit acceptance targets

The kit synthesizes 15 drum articulations inside Cicada from damped membrane, shell, rim and metallic modes with filtered contact and snare-wire excitation. It contains no samples or impulse responses. The targets below were defined before implementation. Listening acceptance remains the owner's final gate; passing engineering checks does not establish equivalence to an acoustic recording or commercial drum library.

```text
cicada 2
kit acoustic {
  bd = model.kick
  sd = model.snare
  ch = model.hat_closed
  oh = model.hat_open
  cy = model.crash
}
track kit acoustic {
  level = -12dB
  bd_tune = 0.9
  sd_position = 0.4
  cy_decay = 1.2
}
pattern beat drums {
  bd: x8... ...x6 x8... ....
  sd: .... x7... .... x7...
  ch: x4.x5. x4.x5. x4.x5. x4.x5.
  oh: .... .... .... ..x5.
  cy: x6... .... .... ....
}
scene groove { kit = beat }
song { groove*4 }
```

Use the checked examples under `examples/modeled-kit/` for full scores. Any drum source lane may select any `model.` articulation; omitted lanes remain silent.

| Piece | Model suffixes |
| --- | --- |
| Kick | `kick` |
| Snare | `snare`, `rimshot`, `cross_stick`; low snare velocity gives ghost notes |
| Toms | `tom_low`, `tom_mid`, `tom_high` with a pitch fall after the strike |
| Hi-hat | `hat_closed`, `hat_pedal`, `hat_half_open`, `hat_open` |
| Ride | `ride_bow`, `ride_bell` |
| Other cymbals | `crash`, `splash` |

Track controls use the source lane prefix, such as `bd_tune` or `sd_position`. Tune multiplies modal frequency (0.5–2, default 1). Decay multiplies tail time (0.25–2, default 1). Position runs from centre to edge (0–1, default 0.35). Humanize adds bounded strength, timbre and pitch variation (0–0.1, default 0.015); it does not move event times. These synthesis controls are fixed before rendering and affect subsequent strikes. Decay is a unitless multiplier, so `250ms` is invalid. Lane level and pan use the existing mixer and may change live; unsupported live synthesis updates are rejected.

Every bound lane reserves one voice, retains one ringing strike and fades its predecessor for 1 ms on retrigger. The two-track full kit binds 11 main pieces and four alternate articulations, using 15 of the 32 available voices. Score gate ends leave natural tails. A live `OpNoteOff` on a drum lane explicitly chokes it with a 5 ms model fade. Every modeled hat strike chokes the other modeled hats in the same kit track, based on their selected articulation rather than lane name. Hats on separate tracks do not choke each other.

## Behaviours and measurable gates

The behaviour references are [Ableton Drum Rack choke groups](https://www.ableton.com/en/manual/instrument-drum-and-effect-racks/#25-6-drum-racks), [Ableton Sampler repeat variation and velocity zones](https://www.ableton.com/en/manual/live-instrument-reference/#31-10-5-1-round-robin-sample-playback), and [Logic Pro Drum Kit Designer mappings](https://support.apple.com/en-ca/guide/logicpro/lgsia2ae90f1/mac). They establish useful instrument behaviours, not licensed audio comparisons. Numeric thresholds below are Cicada's engineering targets.

| Behaviour | Gate |
| --- | --- |
| Velocity and ghost notes | Strike RMS increases at MIDI velocities 24, 48, 80, and 110. Velocity changes excitation colour as well as gain. Velocity zero produces no strike. |
| Repeat variation | Four deterministic excitation variations produce distinct strikes. Reset reproduces the sequence. No random clock seed. |
| Strike position | Centre and edge settings measurably change spectral balance without changing the selected drum. |
| Articulations | Kick; snare, rimshot and cross-stick; low, mid and high tom; closed, pedal, half-open and open hat; ride bow and bell; crash and splash. Low snare velocity provides ghost notes. |
| Release and choke | Score gate ends leave a natural struck tail. Closing or pedalling the hat chokes its previous open tail within the same kit track; an explicit live drum note-off fades the model within 5 ms. |
| Tails and noise | Reset silence and completed tails reach exact zero. Every accepted control combination produces finite output at 44.1, 48 and 96 kHz. Report tail RMS and DC. |
| Bandwidth and aliasing | Resonances above 0.45 times sample rate are omitted. Compare complete rendered 48 kHz hits against downsampled 96 kHz hits; report the residual honestly because stochastic attack noise and rate-dependent modal omission also contribute. |
| Allocations | Zero allocations in warmed render loops. Setup and score compilation may allocate. |
| CPU | Report actual production WASM 128-frame callbacks at 48 kHz. Target each active articulation below 266.7 microseconds at p99; the full kit remains below the existing 670 microsecond callback gate and 32-voice ceiling. Timing includes shared mixing and limiting. Hardware and runtime affect these numbers. |
| Kernel size | Raw production kernel at most 307200 bytes. Report deltas against the starting modeled-percussion kernel (275517 bytes) and main (266005 bytes); no budget raise. |
| Reference measurements | Pin CC0 recording source, license and checksum. Compare onset-aligned, separately normalized RMS envelopes and spectral bands for kick, snare, hats and cymbals. Report differences without a perceptual pass threshold. |
| Room | A separate audition uses the existing stable, allocation-free algorithmic reverb. It is a synthesized room impression, without a recorded room or IR. Keep the dry comparisons. |
| Listening | Matched reggae one-drop, soca, rock and jazz ride scores use identical hit schedules, velocities, tempo, gain and render settings for before/after. Decode every WAV and verify playlist entries. |

## Listening and evidence

`examples/modeled-kit/grooves.json` is a reusable common hit schedule available to the sampled-kit lane. Each score renders four bars: the 32-cell, two-bar pattern plays twice. Generated score pairs preserve timing and velocity cells; the before scores select Cicada's existing built-in synthetic drums. The after scores select the modeled kit. Dry comparisons preserve the same gain rather than equalizing perceived loudness; the modeled defaults are quieter in these grooves. The built-in ride surrogate is a cowbell, and its pedal surrogate is a clap, so the before pack is an engine regression comparison rather than an acoustic-kit reference.

Four `after-room` scores add the existing algorithmic reverb at a 0.16 send, 0.8 s decay, 12 ms predelay, 6 kHz damping and 120 Hz high-pass. There is no sampled IR. `TestReverbBoundsAndImpulse`, `TestReverbParameterChangesAndAllocationFree`, and `TestReverbDecayAndDC` cover the reused processor's stability, allocations and decay.

Reference audio is fetched separately under the manifest in `assets/reference/drum-kit.json` and never embedded in the kit or WASM kernel. `tools/audio/kit-reference.py` reports spectral and envelope differences. These recordings include microphone, room and capture noise; this kit has no such recorded noise floor.

## Measured candidate result

On 2026-10-06, the production Node V8 WASM kernel at 48 kHz and 128-frame blocks measured 22.4–26.7 microseconds median per active articulation, including the mixer and limiter. Conservative 32-callback process CPU averages at p99 were 34.4–215.0 microseconds per callback, below the 266.7 target. The 15-articulation full-kit probe measured 103.7 microseconds median, 522.3 microseconds wall p99 and 159.1 microseconds per callback at p99 for the batch CPU metric, below the 670 target. The kit's hat choke logic remains active, so all 15 requested articulations are not guaranteed to ring simultaneously. No faults, nonfinite samples or WASM memory growth occurred. [Final timing report](evidence/kit-modeled/cpu-cached-final.json).

Individual-articulation wall p99 still ranged from 135.1 to 920.1 microseconds; several fail the stated wall-time target on this shared host. Initial runs also failed full-kit wall p99, and those reports are retained: [initial run](evidence/kit-modeled/cpu-contended-initial.json), [uncached run](evidence/kit-modeled/cpu-uncached-final.json). Batch CPU time is averaged over 32 callbacks and includes the timing harness, excitation commands and output/message checks; it is not a per-callback deadline guarantee. Device AudioWorklet qualification remains pending. The final harness caches WASM views and preallocates timing arrays to avoid callback allocation artifacts.

The raw kernel is 296459 bytes: +20942 against the parent modeled-percussion kernel and +30454 against main, within 307200 bytes. Brotli is 90345 bytes: +6554 against the parent (83791 bytes). Image version 13 carries capability bit 4 for modeled kit bindings alongside the existing modal capability bit 3. Existing built-in kit bindings keep their encoding and sound.

[DSP tests](../../kernel/voice/modeledkit/voice_test.go) cover all 15 profiles: velocity level and brightness at 24/48/80/110/127, four strike variations/reset, velocity-zero tail preservation, finite output and exact completed-tail silence at 44.1/48/96 kHz, 5 ms choke, bounded retrigger, zero strike/render allocations, tune/decay/position, and stable in-band poles at extreme controls. Linear retained-mode comparisons against 96 kHz agree within numerical precision; these tests exclude contact/noise excitation. Rendered sweeps at velocities 24/48/80/110 are monotonic for kick, snare, closed hat and ride bow. Snare edge position changes the measured power centroid by +998 Hz against centre. Explicit crash and splash choke output ends 6.48 ms after the command, including the existing limiter latency; the model fade itself remains within 5 ms. [Rendered measurements](evidence/kit-modeled/render-measurements.json).

| Separately fetched CC0 comparison | Model minus reference centroid | Normalized RMS envelope error | Complete 48/96 kHz rendered residual |
| --- | ---: | ---: | ---: |
| Bass drum / kick | -47 Hz | 0.088 | -45.6 dB |
| Modern snare | +893 Hz | 0.104 | -4.8 dB |
| Closed hi-hat | -2886 Hz | 0.134 | -2.2 dB |
| Open hi-hat | -950 Hz | 0.060 | -1.5 dB |
| Suspended cymbal stick hit / ride bow | -1397 Hz | 0.074 | -8.0 dB |

The bass drum and suspended cymbal are related references, not dimension-matched drum-set recordings. The model's closed hat is substantially darker in this reference comparison; snare spectrum and cymbal decay also differ. Complete high-rate residuals include rate-dependent random excitation and omitted modes, so broadband alias rejection is not qualified by these numbers. This is a candid difference report, not a realism pass. [Reference measurements and method](evidence/kit-modeled/reference-comparison.json), [license manifest](../../assets/reference/drum-kit.json).

The listening pack has 52 verified WAVs: eight dry groove comparisons, four room auditions, 15 isolated articulations, four velocity sweeps, two position hits, four choke auditions, ten 48/96 kHz measurement hits and five licensed references. `LISTEN.m3u` includes the 37 groove/audition WAVs. Native event auditions carry JSON recipes naming their models, controls, seed and exact command samples. Every WAV is decoded and every playlist entry checked. Seven-second single-hit auditions may end before the longest cymbal lifetime; full-tail tests render until exact silence. [Decode report](evidence/kit-modeled/decode-report.json).

The full WASM integration suite passes, including native/WASM parity for every modeled articulation and four grooves. CLI tests and the existing browser AudioWorklet, render-parity and capture checks pass. The existing browser CPU fallback measured p95 0.121 ms and p99 0.202 ms; that baseline score is separate from modeled-kit device qualification. All 12 candidate groove scores and the four baseline scores validate.

Reproduce with an explicit output directory outside the repository:

```sh
KIT_EVIDENCE=$(mktemp -d)
GOWORK=off go run tools/audio/kit-images.go "$KIT_EVIDENCE/images"
node tools/audio/kit-cpu.cjs build/cicada-kernel.wasm "$KIT_EVIDENCE/images" "$KIT_EVIDENCE/cpu.json"
python3 tools/audio/kit-pack.py --cicada build/cicada --auditions "$KIT_EVIDENCE/images" --output "$KIT_EVIDENCE/pack"
python3 tools/audio/kit-reference.py --models "$KIT_EVIDENCE/images" --references "$KIT_EVIDENCE/pack/references" --output "$KIT_EVIDENCE/pack/reference-comparison.json"
python3 tools/audio/kit-measure.py --auditions "$KIT_EVIDENCE/images" --output "$KIT_EVIDENCE/pack/render-measurements.json"
python3 tools/audio/kit-pack.py --verify-only --output "$KIT_EVIDENCE/pack"
```

The reference and measurement scripts require NumPy. `--generate-only` regenerates the checked score files from the shared schedule. The CPU tool exits nonzero when wall or batch CPU targets fail and writes the measured report before exiting.

## Limits to assess by ear

Cymbals are the hardest part: a compact finite modal/noise model cannot capture the dense, nonlinear flexing and strike-dependent shimmer of a recorded cymbal. A synthesized snare-wire model approximates buzz rather than simulating every wire contact. Repeated hits replace each lane's preceding tail after a 1 ms fade, so cymbal wash accumulates less richly than a polyphonic sample library. No cross-drum sympathetic coupling, microphone bleed, separately captured room microphones, brushes, mallets, continuous hi-hat pedal travel, or drummer limb constraints are claimed. Legato transitions do not apply to one-shot struck kit pieces. Lane-local synthesis controls are not yet live-automatable.

Owner listening acceptance and device deadline qualification remain pending.
