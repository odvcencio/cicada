# Full kit validation and listening results

The full kit is ready for owner listening review. Automated validation passes; owner sound acceptance and target-device realtime deadlines remain pending.

## Measured results — 2026-10-06

- All 322 compressed assets pass their compressed/WAV/source checksums and decode with the pinned host. Shells have 206 zones; metals have 168. Metadata-only CI also admits complete cycles, exercises velocities 0–127, checks all GM kit notes, validates five demo scores and checks their manifest pins.
- All 322 native-pitch recordings match prepared PCM exactly after the first 128 frames and before the final 5 ms fade. The same exclusion applied to 24 representative unmodified licensed-source takes gives zero measured residual, spectral error and 20 ms RMS-envelope error. `measurements.json` encodes exact-zero residual as -600 dB, a numerical floor rather than a measured noise claim.
- Original source frame counts are preserved; selected ride tails reach 29.113 s. Per-file source peak, DC and final-100-ms RMS are reported. Tail RMS includes remaining music/room decay and cannot isolate environmental or microphone noise.
- Source power-ranked hits supply six centers/three takes for kicks and four physical toms; center snare has eight/three. Main closed/half-open/open hats have six/three. Cymbals have four centers and two or three takes. VCSL cross-stick has one/two; ghosts and hard-bow ride mappings reuse documented subsets. No recorded edge/crash-ride, flam or roll asset is claimed.
- Native fixture tests prove overlapping cymbals, per-group 2 ms choking without damping other cymbals, retained exclusive hats, and unchanged one-shot note-off behavior. Production optional-sampler ABI parity exercises the new flag and overlapping note slots. Existing resampling gates pass >=80 dB rejection, <=-90 dB residual and <=0.05 dB ripple at qualified ratios.
- Actual banks pass the browser loader and production WASM preparation. Node/V8 render measurements at 48 kHz/128 frames, one/eight/sixteen voices and one/two layers report 30.49–43.50 ns/sample/voice and a worst p99 of 374.610 microseconds per bank. Repeated notes include bounded steal tails; note-command setup is outside each timed interval. Memory growth during rendering is zero. These timings are machine/load dependent and do not qualify Windows or mobile AudioWorklet deadlines.
- Real Chromium 153.0.8010.12 prepares each full bank while suspended, produces nonzero worklet output, reuses its complete compressed cache offline, and resets to exact zero. The requested-bank fetch counts are 165 (shells) and 159 (metals), including manifests. The fixture browser gate separately rejects a corrupt pin and preserves exact uint64 event seeds.
- Core kernel: 266,005 raw / 80,800 Brotli bytes, **zero delta**, below its unchanged 300 KiB raw gate. Core processor: 5,112 B, below 5 KiB. Optional sampler: 31,621 B, **+68 B** from its prerequisite, below 64 KiB. New audio stays external.

## Checks run

Native `go test ./...`, `go vet ./...`, formatting, grammar, goldens, allocations and timing pass. `make test-kernel-wasm test-sampler-wasm budget-size` passes, including core/stream parity and the hour-long core memory fixture. Existing M2 browser tests and `budget-browser` pass; that CPU report uses its disclosed Node fallback. Real sampler-bank worklet admission/output is separately verified above. The initial inherited temporary/cache filesystem caused startup and sparse-file errors; using local temporary/cache storage resolved them without a source workaround.

Rebuild and verification use Python 3/NumPy/FFmpeg/bsdtar. The checked builder regenerates byte-identical catalog/manifests/audio and refuses mismatches. Python diagnostics pass.

```sh
python3 tools/sampler/build_full_kit.py --out build/kit-final/packs --cache build/kit-source-cache --verify assets/sampler/full-kit/catalog.json
go run tools/sampler/full_kit_audit.go --packs build/kit-final/packs --out build/kit-reports/reference-final
python3 tools/sampler/full_kit_metrics.py --packs build/kit-final/packs --renders build/kit-reports/reference-final
node --expose-gc tools/sampler/full_kit_wasm.mjs build/kit-final/packs
node tools/sampler/full_kit_browser.mjs build/kit-final/packs
```

Use `GOWORK=off`. Browser checks require an installed Playwright entrypoint in `PLAYWRIGHT_MODULE` and an executable Chromium in `CHROME_BIN`. Set the temporary/cache environment to a filesystem suitable for Go executables and sparse WAV tests.

## Listening protocol and limits

The listening pack has 24 complete 48 kHz PCM24 stereo WAVs: five groove pairs (rock, funk, reggae one-drop, soca and jazz ride), their optional -18 LUFS/-1 dBTP `loudnorm` versions, and two MIDI-event score pairs (34 articulations and a 74-event velocity/RR/flam/roll/choke audition). All before/after pairs use identical musical events, frame timing, velocity, seed and gain. Source score notation has velocity 100; explicit event scores exercise velocities 8–127 and a 35 ms flam.

Before uses the old VCSL three-piece kit with explicit nearest-piece substitutions. Toms use its kick; extra snare strokes use its snare; ride uses its closed hat; crashes/splashes/china use its open hat. `SUBSTITUTIONS.json` records every note. This comparison changes the recorded palette; it is not a claim of rendering the score on main or isolating sampler-DSP improvements. Raw pairs preserve levels; loudness-processed versions serve convenient listening and must not be used to judge velocity dynamics. All full-kit groove renders report zero clipping/ceiling samples. The diagnostic scores leave 32 s for natural decay.

Only 5 ms of each source ending is faded; there is no loop, decay trimming, per-take normalization, generated room or convolution. The source stereo mix already contains EQ/panning/room microphones. The VCSL cross-stick comes from a different recording session. Linear blending of unrelated velocity recordings and finite voice stealing still require ears. `ride_crash` is a hard-bow subset rather than separately recorded edge hits. Mobile memory use is substantial: 435.41 MiB PCM across both banks, plus host admission copies and SRC tables. Production Studio/native live pack routing remains a separate integration task.
