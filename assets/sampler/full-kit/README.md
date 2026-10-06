# Full acoustic drum kit

Build the external audio banks and copy a groove score beside them:

```sh
python3 tools/sampler/build_full_kit.py --out demo/packs --cache sample-source-cache --verify assets/sampler/full-kit/catalog.json
cp assets/sampler/full-kit/demos/reggae-one-drop.cicada demo/
cicada render demo/reggae-one-drop.cicada -o demo/reggae.wav --tail 10s
```

Python 3, NumPy, FFmpeg and bsdtar are required. The builder verifies the pinned official source archive, records every source WAV checksum, and checks the exact regenerated catalog, manifests and compressed WAV hashes. It stages output and refuses to replace different or corrupt banks. Files are fetched only when the browser host requests their bank; PCM is admitted before playback. Only metadata and demo scores are committed.

The distribution has **322 distinct recordings**, **34 note mappings**, and **252,612,372 compressed audio bytes** (240.91 MiB). Four physical toms cover all six GM tom notes. Shells retain 203,902,040 B (194.46 MiB) of resident float32 PCM; metals retain 252,660,624 B (240.96 MiB). Each bank stays below the existing 256 MiB cap. Together they retain 435.41 MiB PCM, plus SRC tables; browser admission temporarily holds host PCM and WASM PCM. Whole-bank loading is substantial for phones.

## Recorded coverage and MIDI routing

Cicada articulation names below are host routing labels and suggested track names. Scores use the listed MIDI pitches through `sampler` declarations; this adds no articulation keyword to the language. Dispatch a note to its listed bank. Both banks can be connected to the same bus. Extension notes below GM 35 have the pack-specific meanings below; unsupported GM percussion is not silently substituted.

| Articulation | MIDI | Bank | Layers × takes | Material |
| --- | ---: | --- | --- | --- |
| `hat_loose` | 22 | metals | 4 × 3 | HihatClosedNoPedal |
| `hat_half_open` | 23 | metals | 6 × 3 | HihatSemiOpen |
| `crash_left_stopped` | 24 | metals | 4 × 2 | CrashLStopped |
| `crash_right_stopped` | 25 | metals | 4 × 2 | CrashRStopped |
| `crash_left_choke` | 26 | metals | control | silent choke control |
| `crash_right_choke` | 27 | metals | control | silent choke control |
| `splash_choke` | 28 | metals | control | silent choke control |
| `splash_right_choke` | 29 | metals | control | silent choke control |
| `china_choke` | 30 | metals | control | silent choke control |
| `snare_rim` | 31 | shells | 6 × 3 | SnareRim |
| `splash_right` | 32 | metals | 4 × 2 | SplashR |
| `ride_choke` | 33 | metals | control | silent choke control |
| `snare_ghost` | 34 | shells | 2 × 3 | mapped subset |
| `kick_left` | 35 | shells | 6 × 3 | KDrumL |
| `kick` | 36 | shells | 6 × 3 | KDrumR |
| `snare_cross_stick` | 37 | shells | 1 × 2 | VCSL Snare4 Xstick |
| `snare_center` | 38 | shells | 8 × 3 | Snare |
| `snare_rimshot` | 40 | shells | 4 × 3 | SnareRimShot |
| `tom_low_floor` | 41 | shells | 6 × 3 | FTom2 |
| `hat_closed` | 42 | metals | 6 × 3 | HihatClosed |
| `tom_floor` | 43 | shells | 6 × 3 | FTom1 |
| `hat_pedal` | 44 | metals | 4 × 2 | HihatPedal |
| `tom_mid` | 45 | shells | 6 × 3 | Tom2 |
| `hat_open` | 46 | metals | 6 × 3 | HihatOpen |
| `tom_mid_gm` | 47 | shells | 6 × 3 | GM alias |
| `tom_high` | 48 | shells | 6 × 3 | Tom1 |
| `crash_left` | 49 | metals | 4 × 3 | CrashL |
| `tom_high_gm` | 50 | shells | 6 × 3 | GM alias |
| `ride_bow` | 51 | metals | 4 × 2 | RideR |
| `china` | 52 | metals | 4 × 3 | ChinaR |
| `ride_bell` | 53 | metals | 4 × 2 | RideRBell |
| `splash` | 55 | metals | 4 × 2 | SplashL |
| `crash_right` | 57 | metals | 4 × 3 | CrashR |
| `ride_crash` | 59 | metals | 2 × 2 | mapped subset |

Distinct hits are ranked by the source XML power and distributed into dynamic bands and complete take cycles. The builder preserves recorded amplitude and does not synthesize new layers or normalize each take. Ghosts reuse the quietest center hits. `ride_crash` reuses the hardest ride-bow hits; the source does **not** separately label a ride edge/crash articulation. The two GM tom aliases reuse their physical drum without pitch shifting or additional claimed recordings.

One-shot hits ignore note-off. Hats share exclusive choke group 1. Cymbals use `ChokeSustain` so repeated strikes overlap; their dedicated silent choke notes damp all active strikes in that group in 2 ms. Stopped crash strikes are separately recorded articulations, not silent choke controls. Snare flams/rolls are successive independently cycled center/ghost hits; no recorded flam or roll asset is claimed. Dense repeats can exhaust the bounded voice pool and use its 2 ms steal ramp.

The principal kit uses the official coherent stereo mix, with its existing EQ, panning and room microphones. No artificial room or IR is added. Source durations are preserved, including selected ride tails up to 29.113 s. Only the final 5 ms is faded to zero. VCSL cross-stick retains its different recorded room and only one recorded dynamic with two takes.

## Sources and asset licenses

- [CrocellKit and its official stereo adaptation](https://drumgizmo.org/wiki/doku.php?id=kits:crocellkit): [CC-BY-4.0](https://creativecommons.org/licenses/by/4.0/). Attribution is recorded in every affected asset, with a link to the full recording and stereo-mix credits. The source archive SHA-256 is in `catalog.json`; each source WAV has a separate SHA-256 in its bank manifest. The archive is 561,035,551 B.
- [Versilian Community Sample Library](https://github.com/sgossner/VCSL/tree/c1ea7bcc3c7309650ab0da9d15c9cd1fbc4a4c7e): CC0-1.0, two explicitly labeled modern-snare cross-stick hits. Source URLs are commit-pinned and each source is hashed.

Code remains MIT. Audio keeps its separate license. For CrocellKit recordings and adaptations, retain the attribution and license links in redistributed packs and in compositions wherever other credits are presented. A suitable credit is:

> CrocellKit by DrumGizmo and Crocell; official stereo adaptation; CC-BY-4.0. Full recording/mix credits: https://drumgizmo.org/wiki/doku.php?id=kits:crocellkit. Cicada adaptation selects hits, maps velocity/takes, changes WAV encoding and fades the final 5 ms.

## Browser host

Use the matching optional sampler module and loader for this format: descriptor word 19 bit 9 carries `ChokeSustain`. Older modules reject this flag. Prepare both nodes while the audio context is suspended. Configure one bank per node, with 16 voices per bank for 32 bounded note slots overall. Copy each catalog manifest pin into `createSampleInstrument`; the existing [host documentation](../../../docs/sampler/packs.md) supplies the API and exact event-seed rules. All fetching, decompression, hashing and PCM upload happen before playback.

## Example grooves

The five scores cover rock, funk, reggae one-drop, soca and a jazz ride pattern. Groove source notation has fixed note velocity 100; ghosts use quiet recorded mappings. Explicit MIDI event scores in the listening pack demonstrate velocities 8–127, repeated takes, a 35 ms flam, a snare roll, overlapping cymbals and choking. Jazz groove timing uses the fixed-grid ride pattern, not a claim of triplet-grid support.

`full_kit_demos.py` writes paired scores with identical notes/timing/seed/gain and different bank declarations. It also writes the starter VCSL substitution maps: toms use its kick, extra snare strokes use its center snare, ride uses closed hat, cymbals use open hat, and missing silent choke controls use zero-gain zones. Starter ghosts use a documented 0.25 gain. This is a palette comparison with the old three-piece kit, not a DSP-only comparison. Raw pairs preserve dynamics; optional FFmpeg `loudnorm` level versions target -18 LUFS/-1 dBTP and must not be used to judge absolute velocity response.

See [pack hosting](../../../docs/sampler/packs.md) for manifest fields, loading, and playback. Sound quality and realtime CPU usage depend on the recordings and target device.
