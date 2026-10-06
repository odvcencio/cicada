# Sample instrument listening candidate

The engine passes the [measurable acceptance targets](quality.md). The six CC0 packs are a listening candidate with limited recording coverage; the owner's listening acceptance is still pending. Measurements qualify playback accuracy and the tested workloads, not equivalence to a commercial DAW library.

## Behaviour and signal measurements

Velocity fixtures cover all 128 velocities and crossfade boundaries. Round-robin, recorded release, pedal, stale note ownership, hat choke, phase-preserving legato, seeded variation and block-size invariance pass. Deliberately discontinuous loop/steal fixtures stay below a 0.03 first difference at 48 kHz. These are fixture bounds, not a guarantee that every authored sample sounds seamless.

| Gate | Target | Result |
| --- | ---: | ---: |
| Qualified SRC downsampling stopband rejection | >=80 dB | >=120.78 dB |
| Qualified SRC in-band residual | <=-90 dB | <=-127.50 dB |
| Qualified SRC ripple | <=0.05 dB | <=0.00001 dB, rounded |
| Native/TinyGo sample fixture | Bit exact | 221,184 samples, 3 rates, 3 block sizes |
| Native/production sampler WASM ABI | Bit exact, no growth | 8,192 samples, zero render-time memory growth |
| Idle/reset output | Exact zero | Pass, including real browser worklet |
| Native note/render allocations | Zero | Pass |

SRC ratios are 0.25, 0.5, 0.75, 1, 1.25, 1.5, 2, 3, 4 and the 44.1/48 kHz conversions. The stopband figure refers to the downsampling sweep; the other ratios use the residual measurement. These gates reuse the existing windowed-sinc resampler rather than adding a second resampler.

Selected root notes are also compared against the licensed original recordings decoded to 48 kHz float32. The first 128 attack frames and loop transitions are excluded. Significant spectral bins are within 0.00444 dB, significant 10 ms envelope frames within 0.000087 dB, and relative conversion/playback residual is at most -108.68 dB. The separate source-comparison gates are 0.1 dB spectral, 0.01 dB envelope and -75 dB residual. Exact selections, source URLs/hashes, tail RMS and DC measurements are in [the source report](evidence/source-reference.json). A sounding recording's excerpt tail is not an isolated noise-floor measurement; source noise, room sound and decay are preserved, not removed or claimed silent.

## Runtime and size

Node V8 14.6.202.34-node.28, 48 kHz, 128-frame blocks, 10,000 measured blocks after warmup:

| Voices / layers / pitch ratio | Median block | p99 block | Median ns / sample / voice |
| --- | ---: | ---: | ---: |
| 1 / 1 / 1 | 2.934 us | 14.560 us | 22.922 |
| 8 / 2 / 1 | 37.824 us | 100.120 us | 36.938 |
| 8 / 2 / 1.5 | 244.818 us | 487.828 us | 239.080 |

All eight benchmark cases have zero render-time memory growth; [full results](evidence/wasm-timing.jsonl) include one/two layers at unity and ratio 1.5. Timing covers the optional sampler DSP, not loading, event bursts, effects, OS audio or a Windows release callback qualification. It does not qualify every 64-voice configuration. Eight shared sinc tables occupy about 8.61 MB per WASM instance; resident PCM is additional. Worklet construction uploads and seals one whole bank synchronously. Prepare while the context is suspended, before gameplay.

Pinned CI Chromium 152.0.7977.64 passes actual AudioWorklet output/scheduling, exact uint64 seeds, lazy requests, offline compressed-cache reuse, corrupt-pin rejection and reset silence; see [browser report](evidence/browser.json). Existing browser functional/budget gates pass, with their CPU measurement using the Node V8 fallback. Native tests, vet, grammar, golden, allocation/timing, production kernel/stream parity and production size gates pass with `GOWORK=off`.

| Artifact | Before | After | Delta |
| --- | ---: | ---: | ---: |
| Production kernel, raw | 266,005 B | 266,005 B | 0 B |
| Production kernel, Brotli | 80,800 B | 80,800 B | 0 B |
| Optional sampler module, raw | Absent | 31,553 B | +31,553 B, separate module |

The main raw budget remains 300 KiB; the optional module has a separate 64 KiB gate. Audio is external: 316 compressed assets total 264,739,133 B. [The catalog](../../assets/sampler/README.md) lists individual pack sizes, recording coverage, provenance and licences. Code is MIT; every recording is CC0-1.0. No impulse responses or proprietary reference recordings are distributed.

Reproduce the source comparisons after building the pinned packs (NumPy and FFmpeg are required):

```sh
GOWORK=off go run ./tools/sampler/source_render.go -packs build/pro-packs -out build/source-reference > build/source-reference-selections.txt
python3 tools/sampler/source_metrics.py --packs build/pro-packs --sources sample-source-cache --selections build/source-reference-selections.txt
GOWORK=off make build-sampler-wasm
node tools/sampler/wasm_metrics.mjs
node tools/sampler/browser_gate.mjs
```

The browser gate needs Playwright installed and a Chrome executable selected by `CHROME_BIN`; `PLAYWRIGHT_MODULE` can name its installed entrypoint. Use the same source cache supplied to the catalog builder. Published pack verification detects toolchain output changes instead of silently replacing known-good packs.

## Listening and integration limits

The listening pack has six instrument scores and paired dry/room WAVs at 48 kHz PCM24. Each pair uses the same notes, velocities, seed, gain and existing Cicada reverb. `--sampler-baseline` selects the old single-region sample-pool DSP with the same key map, fixed nearest recorded layer, first take and 2 ms release. Main previously had no complete offline sampler renderer, so “before” identifies that DSP baseline. The raw WAVs preserve musical level differences; additional `-lufs.wav` pairs normalize each dry render to -16 LUFS for a timbre comparison. Normalized pairs must not be used to assess absolute velocity response.

The piano lacks sympathetic/pedal resonance and dense upper dynamics; guitar has one recorded dynamic/take; bass has sparse roots; the kit has kick/snare/hats only; violin/trumpet lack recorded legato and additional articulations. Looped vibrato and crossfading unrelated recordings still need listening review. Filtering is one-pole lowpass. Room versions use the existing synthetic reverb, not convolution. There is no disk streaming or per-zone browser paging. Main's Studio compiler and native realtime `cicada play` do not route these pack declarations; the offline renderer and optional browser/game sample host do. [Pack integration](packs.md) explains how the prepared-assets and preset work can connect without depending on unmerged branches.
