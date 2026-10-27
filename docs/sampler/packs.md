# External sample instrument packs

A pack is a pinned JSON map plus encoded audio. New downloads default to scaled PCM16 FLAC; the exact tier uses FLAC with gzip WAV fallbacks for float32 recordings that integer FLAC cannot preserve. Existing gzip packs remain supported. All file access, hashing, decompression and map validation finish before playback. The core kernel stays unchanged. `cmd/cicada-sampler-wasm` is an optional 64 KiB-budget kernel for one prepared bank per instance; the same DSP renders offline.

```cicada
cicada 2
sampler grand {
  pack = "packs/grand/manifest.json"
  sha256 = "EXACT_64_HEX_DIGIT_MANIFEST_HASH"
  root = c4
  voices = 16
}
track piano grand { level = -6dB }
pattern melody { c4 . e4 . g4 . c5 . }
scene main { piano = melody }
song { main*4 }
```

`root` supplies the home octave for relative score pitches; each pack zone has its own recorded root. `voices` limits simultaneous notes; each note can blend two attack layers and two release layers. The manifest hash pins the exact map, configuration, provenance and asset hashes. Packs cannot also declare `asset` or loop `mode`. Existing single-file sampler declarations retain their syntax.

`cicada render score.cicada -o after.wav` resolves packs inside the score directory, including confined symlink traversal. `--sampler-baseline` renders the same events through the existing single-region DSP using the same key maps, one fixed recorded dynamic, the first take and the original 2 ms release. The baseline has a separate pool for each key map and is an offline listening reference, not a performance comparison. Stems retain stereo sampler output. The host refuses missing/corrupt packs instead of substituting a synth.

## Memory limits

A host keeps a pack's audio in memory as decoded float32 PCM, four bytes per sample and channel. For the catalog's six CC0 packs that is 461.8 MB, about six times their 75.8 MB PCM16 FLAC download. The limit depends on where the pack plays:

| Where | Limit on pack audio in memory |
| --- | --- |
| `cicada render`, `cicada stems`, `cicada play` and Studio | One cap shared by every pack in the project, 1 GiB by default. `--max-resident-pcm <size>` sets it on `render`, `stems` and `play`; Studio uses the default. |
| Browser image and WAM2 export (`cicada wam2`, `cicada check --profile browser`) | 64 MiB for all packs and sampler assets together. The image that holds them is limited to 2 MiB. |
| `cmd/cicada-sampler-wasm` | 256 MiB for the one bank an instance holds. |

Each pack also stays under the 256 MiB limit described below, so the default cap holds up to four packs of that size. Sampler assets that are not packs keep their own 64 MiB limit.

```sh
cicada render song.cicada -o song.wav --max-resident-pcm 2GiB
```

A size is a whole number with `B`, `KiB`, `MiB`, `GiB` or `TiB` (powers of 1024), or `KB`, `MB`, `GB` or `TB` (powers of 1000). A project whose packs decode to more than the cap is refused before it renders. The message names the cap and the size measured when the cap was crossed:

```text
sampler packs exceed the native resident PCM cap (--max-resident-pcm): 139.35 MiB (146117712 bytes) decoded after loading packs/bass/manifest.json, cap 100 MiB (104857600 bytes)
```

`cicada play` reloads the packs when you save an edit, and the new copy sits beside the old one until it takes over at the next bar. A reload can therefore hold up to twice the cap for a short time.

## Checking a score against its packs

`cicada check` loads every pack that a score declares, as `render` does. It reports a pack that does not load (a missing file or a manifest that differs from its `sha256` pin) as `CICADA-SAMPLER-PACK`, at the `sampler` declaration. It also reports each scored note that falls outside every zone of its track's pack as `CICADA-SAMPLER-ZONE`, at the note in the source:

```text
song.cicada:24:8: error CICADA-SAMPLER-ZONE: pitch c0 (MIDI 12) on track lead, pattern demo, is outside every zone of the pack for sampler sound (packs/bass/manifest.json, which covers MIDI 23-66)
```

Without this check the note stops the render later, with engine fault 29, and stops the music in a browser. The check applies the pattern's `transpose` and each step's velocity, and a note can be outside every zone because of its velocity when the pack's zones leave a gap there (the message then adds `at velocity N`). Release zones do not start a sound, so they do not count. A pattern that no scene, arrangement placement, chain or live stinger plays on the track is not checked. `check` does not apply `--max-resident-pcm`; `render` and `play` do.

## Manifest version 1

`format` is `cicada.instrument-pack/1`. `config` holds `sample.InstrumentConfig`; `Humanize.Seed` is an exact uint64 decimal string. `assets` declare unique IDs, relative audio paths, byte counts and SHA-256 hashes, source URLs/hashes, source rate/frame/channel dimensions, SPDX licence, licence URL and any required attribution. Accepted licences are CC0-1.0 and CC-BY-4.0; CC-BY needs attribution. Each pack is limited to 256 MiB decoded planar float32 PCM, 4096 assets/zones and 2 MiB JSON. Each asset is limited to 8 million frames. Code remains MIT; audio retains its own licence.

`encoding: "flac"` assets include `pcm_sha256` and a positive float32 power-of-two `scale`, with `.flac` paths. The PCM pin hashes channel-major float32 values in little-endian order after applying the scale. Declared frames, rate and channels must match the stream before PCM can reach playback. Exact FLAC uses scale 1. `encoding: "wav-gzip"` fallbacks also include `wav_bytes` and `wav_sha256`; scale is 1. Legacy assets omit `encoding`, `scale` and `pcm_sha256` and retain their existing compressed/WAV pins. Encoded bytes, dimensions and canonical PCM are admitted by the host APIs from `host/audioencoding`.

Zones name an asset and declare `Root`, `KeyLow/High`, `VelocityLow/High`, velocity centre `Layer`, key `Group`, zero-based round-robin `Position` and `Count`, `Release`, linear `Gain`, `TuneCents`, source-frame region/loop bounds and `Crossfade`. `ChokeGroup` zero disables choking; matching nonzero groups release old notes in 2 ms. `OneShot` ignores key-up. Group maps may not overlap ambiguously; each declared cycle must be complete. Velocity crossfades use the neighbouring centres and linear amplitude weights. This preserves coherent recordings; unrelated samples can produce phase cancellation and still require listening review.

`ChokeSustain: true` keeps a zone in its choke group while allowing successive strikes to overlap. A dedicated choke zone with the same group and `ChokeSustain: false` damps all those strikes. Omitted/false retains exclusive hat behavior. The optional sampler WASM packs this flag into descriptor word 19 bit 9; use the matching module and browser loader for packs that set it. Older modules reject the new flag during preparation.

## Browser and game hosts

Serve `web.AudioEncoding()` as the sibling `audio-encoding.js`. Serve `web.InstrumentPack()` as `instrument-pack.js` and `web.SamplerProcessor()` as its sibling `sampler-processor.js`, plus the optional WASM kernel and external pack directory. These modules are opt-in; the existing Studio client/worklet and their size gates are unchanged.

```js
import {createSampleInstrument} from './instrument-pack.js';
const context = new AudioContext({sampleRate: 48000});
const node = await createSampleInstrument(context, {
  manifestURL: './packs/grand/manifest.json',
  sha256: catalog.packs.find(p => p.id === 'grand').sha256,
  wasmURL: './cicada-sampler.wasm',
  processorURL: './sampler-processor.js',
  seed: '4242'
});
node.connect(context.destination);
await context.resume();
const frame = Math.ceil(context.currentTime * context.sampleRate) + 128;
node.port.postMessage({kind: 'on', frame, id: 1, note: 60, velocity: 64});
node.port.postMessage({kind: 'off', frame: frame + 24000, id: 1});
```

For catalog-based downloads, pass `catalog`, `catalogURL` and `id` to `createSampleInstrument` instead of `manifestURL`/`sha256`. `selectPack(catalog, id)` and `loadCatalogPack(catalogURL, catalog, id)` choose `hq16` by default. Set `tier: 'lossless'` for exact PCM or `tier: 'gzip'` for the original bank. Catalog entries retain their legacy manifest and pin and add a `tiers` map containing each alternate manifest, pin and download size. Distribute catalogs as trusted metadata; native downloads require an explicit catalog SHA-256 pin. Studio's Instruments panel offers PCM16 FLAC and Exact download choices and provides the pinned sampler declaration after verification. Existing score declarations always load their explicit manifest/pin.

Prepare instruments before resuming a suspended context: WAV admission occurs in the host, but WASM initialization and PCM upload occur in the worklet constructor and can interrupt an already running graph. The shared sinc banks use about 8.6 MB runtime memory per instance, in addition to pack PCM. Multiple large banks can therefore cost substantial memory. There is no disk streaming or acoustic resonance model.

The loader fetches only a requested pack, verifies every compressed and decoded byte stream, and caches admitted compressed bytes in the Cache API. A cached pack works offline. Gzip PCM uses a bounded RIFF decoder. FLAC uses a rate-matched offline browser decoder and restores its integer grid if necessary; admission still requires the canonical PCM pin. `loadPack` and `prepareSampler` are exported for custom hosts/workers. Notes use unique nonzero uint32 owner IDs and integer output-frame positions. An optional `seed` decimal string on `on` makes timing/velocity/pitch humanization independent of unrelated triggers; use the same game-event ID on every client. Round-robin selection still follows trigger order. Send events in frame order; late events land at the next frame. `pedal` uses `down`; `legato` uses `id`, `note`, `cents`; `reset` silences all notes. A fixed 512-event queue refuses overflow. Callback PCM, scheduling and ownership storage are preallocated.
