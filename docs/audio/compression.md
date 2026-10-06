# Compressed audio before playback

Use FLAC for exact integer samples, gzip WAV for float32 samples that FLAC cannot reconstruct exactly, and scaled PCM16 FLAC for a smaller tier. Both tiers decode before playback and verify the encoded bytes, declared dimensions, and canonical float32 PCM. The kernel keeps its existing PCM representation and allocation-free render path.

```sh
go run ./cmd/cicada-audio-encode -in take.wav -out exact -tier lossless
go run ./cmd/cicada-audio-encode -in take.wav -out smaller -tier hq16
```

The output stem receives `.flac` or `.wav.gz`, plus a `.json` descriptor. Existing files are never overwritten. FLAC encoding uses an installed FFmpeg executable; `-ffmpeg` selects it. Lossless export falls back to deterministic gzip if FLAC encoding or exact reconstruction is unavailable. The `hq16` tier requires FFmpeg and verifies its output before writing. No network access is needed.

Decode the descriptor's `asset` with `host/audioencoding.Decode` in Go or `decodeEncodedAudio` from `host/web/audio-encoding.js` in a browser host. Browser FLAC uses `decodeAudioData` with an `OfflineAudioContext` at the authored sample rate; gzip WAV decodes its sample bytes directly to preserve float32 tails. These are independent asset APIs. Existing instrument-pack loaders do not yet consume these descriptors.

## Descriptor and admission

The descriptor format is `cicada.audio-encoding/1`. It names the tier and source WAV SHA-256, then an asset with encoding, encoded size/hash, frames, channels, sample rate, scale and decoded PCM SHA-256. Gzip also declares its expanded byte count. Descriptor bytes can themselves be pinned by the caller.

The PCM hash covers channel-major float32 samples in little-endian IEEE 754 representation. Admission checks it after decoding and scaling. Dimensions and hashes must match before PCM reaches an audio owner. Frame, file and PCM limits match the existing bounded preparation policy; invalid scales, non-finite samples, CRC failures, excess frames and trailing gzip streams are rejected.

The `hq16` tier divides source PCM by a shared power of two, rounds to signed PCM16 and records that scale. Decoding multiplies by the same power of two. This preserves quiet sample amplitude and avoids differences in floating gain arithmetic between hosts. Browser admission also restores the encoded integer grid when a decoder uses asymmetric PCM16 normalization; each reconstruction must match the pinned PCM hash. It is still a lossy tier: keep the original for exact export, and use listening to accept each source family.

Decoded resident PCM stays float32. Native admission needs PCM plus one codec frame; browser FLAC admission also retains its AudioBuffer and a temporary PCM hash buffer. Decoding and checksums are preparation work, never audio-callback work. The FLAC dependency belongs to the host; the kernel does not import it.

## Measurements from the compression study

Measured on 2026-10-06: 34 excerpts, 67.23 seconds at 48 kHz, covering five grand registers, four velocity layers, bass, drum attacks and quiet tails, and a sustained violin loop. Bytes below use the same excerpt corpus. Ratios are today's gzip bytes divided by candidate bytes. Quality is mean per-excerpt SNR capped at 140 dB when combining exact results; lossless is exact.

| Encoding | Bytes | Ratio | SNR | Mean spectral distance |
|---|---:|---:|---:|---:|
| Current PCM24/float32 gzip | 14,955,118 | 1.00× | exact | 0 dB |
| Exact FLAC/gzip mix | 9,361,063 | 1.60× | exact | 0 dB |
| Scaled PCM16 FLAC | 4,749,381 | 3.15× | 87.47 dB | 0.39 dB |
| Scaled PCM20 FLAC | 6,946,560 | 2.15× | 119.48 dB | 0.02 dB |
| Opus 256 kbps stereo | 2,189,578 | 6.83× | 27.92 dB | 5.53 dB |
| AAC 256 kbps stereo | 2,063,875 | 7.25× | 24.90 dB | 4.55 dB |
| TurboQuant 32-frame blocks, 8 bits | 6,865,928 | 2.18× | 38.18 dB | 10.00 dB |

Scaled PCM16's median SNR was 78.90 dB; its lowest excerpt was 68.73 dB. Unscaled PCM16 had an 18.09 dB worst quiet-tail result and was rejected. FLAC's direct integer conversion changed four float32 kit sources; the exact tier retains their gzip encoding.

The delivered encoder also converted and verified every asset in the two complete source libraries. These totals exclude descriptor/catalog bytes; the excerpt quality measurements above remain separate from these whole-library size measurements.

| Library | Assets | Current gzip bytes | Exact tier bytes | Ratio | PCM16 tier bytes | Ratio |
|---|---:|---:|---:|---:|---:|---:|
| Six instrument packs | 361 | 290,679,811 | 165,591,092 | 1.76× | 75,795,475 | 3.84× |
| Full kit | 322 | 252,612,372 | 142,689,506 | 1.77× | 79,681,355 | 3.17× |

Exact export retained gzip for 28 instrument-pack assets and two full-kit assets. The standalone descriptors add 312,840 bytes to the combined exact tier and 311,206 bytes to PCM16. Both tiers retain the same 918,394,776 aggregate float32 PCM bytes when all assets are decoded; per-pack residency limits still apply.

Production admission was measured separately on grand, snare and violin in each tier. Median elapsed preparation milliseconds per second of source audio, over five decodes per asset, include encoded and decoded checksums. File reads and first-use setup are excluded. The loaded-machine timings are a cost estimate rather than a render deadline.

| Tier | Native Go ms/s | Go/WASM ms/s | Browser host ms/s | Kernel delta |
|---|---:|---:|---:|---:|
| Exact FLAC/gzip | 8.70 | 25.41 | 7.85 | 0 |
| Scaled PCM16 FLAC | 7.38 | 21.75 | 11.45 | 0 |

Chromium decoded every conventional codec. Pure-Go FLAC and Opus decoded all test streams, and their Go/WASM output matched native PCM exactly. Chromium and native-Go Opus differed by small float errors in captured comparisons; a native browser Opus tier needs an explicit precision policy if byte-identical PCM is required. FLAC was exact in those browser/native comparisons. The delivered tiers avoid a separate browser codec WASM download.

TurboQuant did not win the measured uses. Across 4/6/8 bits, raw 32-frame blocks averaged 20.47/29.05/38.18 dB SNR. A DCT pre-transform gave no consistent benefit. Phase-discarding envelopes, cepstra and harmonic/noise models stayed small but had poor waveform reconstruction. The existing neural amp and reed have only 36/105 parameters: TQ saved at most 8/114 bytes before decoder overhead, with substantial output error. Keep their pinned int16 weights.

The published keys branches use analytic/modal instrument models, with no sampled packs or dense learned weights. Keep those compact source parameters. Apply these tiers if that lane later publishes self-sampled packs; the acoustic parametric trial does not validate a future phase-aware keys codec.

An external probe of the actual kernel found that one reachable TQ decoder increased raw WASM by 103,487 bytes and Brotli by 47,363 bytes, exceeding both unchanged limits. The delivered host codecs add zero kernel bytes. Timings in the study are preparation costs measured on a loaded machine, not real-time render acceptance. Objective metrics do not establish ABX acoustic evaluation.

Sources: [TurboQuant](https://github.com/odvcencio/turboquant), [paper](https://arxiv.org/abs/2504.19874), [FLAC specification](https://www.rfc-editor.org/rfc/rfc9639.html), [Ogg Opus specification](https://www.rfc-editor.org/rfc/rfc7845.html).
