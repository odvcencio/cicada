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
