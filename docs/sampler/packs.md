# External sample instrument packs

A pack is a pinned JSON map plus losslessly compressed PCM24/float32 WAV files. All file access, hashing, decompression and map validation finish before playback. The core kernel stays unchanged. `cmd/cicada-sampler-wasm` is an optional 64 KiB-budget kernel for one prepared bank per instance; the same DSP renders offline.

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

## Manifest version 1

`format` is `cicada.instrument-pack/1`. `config` holds `sample.InstrumentConfig`; `Humanize.Seed` is an exact uint64 decimal string. `assets` declare unique IDs, relative `.wav.gz` paths, compressed and WAV byte counts and SHA-256 hashes, source URLs/hashes, source rate/frame/channel dimensions, SPDX licence, licence URL and any required attribution. Accepted licences are CC0-1.0 and CC-BY-4.0; CC-BY needs attribution. Each pack is limited to 256 MiB decoded planar float32 PCM, 4096 assets/zones and 2 MiB JSON. Each asset is limited to 8 million frames. Code remains MIT; audio retains its own licence.

Zones name an asset and declare `Root`, `KeyLow/High`, `VelocityLow/High`, velocity centre `Layer`, key `Group`, zero-based round-robin `Position` and `Count`, `Release`, linear `Gain`, `TuneCents`, source-frame region/loop bounds and `Crossfade`. `ChokeGroup` zero disables choking; matching nonzero groups release old notes in 2 ms. `OneShot` ignores key-up. Group maps may not overlap ambiguously; each declared cycle must be complete. Velocity crossfades use the neighbouring centres and linear amplitude weights. This preserves coherent recordings; unrelated samples can produce phase cancellation and still require listening review.

`ChokeSustain: true` keeps a zone in its choke group while allowing successive strikes to overlap. A dedicated choke zone with the same group and `ChokeSustain: false` damps all those strikes. Omitted/false retains exclusive hat behavior. The optional sampler WASM packs this flag into descriptor word 19 bit 9; use the matching module and browser loader for packs that set it. Older modules reject the new flag during preparation.

## Browser and game hosts

Serve `web.InstrumentPack()` as `instrument-pack.js` and `web.SamplerProcessor()` as its sibling `sampler-processor.js`, plus the optional WASM kernel and external pack directory. These modules are opt-in; the existing Studio client/worklet and their size gates are unchanged.

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

Prepare instruments before resuming a suspended context: WAV admission occurs in the host, but WASM initialization and PCM upload occur in the worklet constructor and can interrupt an already running graph. The shared sinc banks use about 8.6 MB runtime memory per instance, in addition to pack PCM. Multiple large banks can therefore cost substantial memory. There is no disk streaming or acoustic resonance model.

The loader fetches only a requested pack, verifies every compressed and decoded byte stream, and caches admitted compressed bytes in the Cache API. A cached pack works offline. PCM decoding uses a bounded RIFF decoder rather than `decodeAudioData`, preserving source frames and native float32 conversion. `loadPack` and `prepareSampler` are exported for custom hosts/workers. Notes use unique nonzero uint32 owner IDs and integer output-frame positions. An optional `seed` decimal string on `on` makes timing/velocity/pitch humanization independent of unrelated triggers; use the same game-event ID on every client. Round-robin selection still follows trigger order. Send events in frame order; late events land at the next frame. `pedal` uses `down`; `legato` uses `id`, `note`, `cents`; `reset` silences all notes. A fixed 512-event queue refuses overflow. Callback PCM, scheduling and ownership storage are preallocated.
