# Owned electronic keyboard packs

These 21 packs record Cicada's original modeled keyboards at 192 kHz, filter with a centered 385-tap sinc, and downsample to 48 kHz PCM24. EPs use fourfold internal oversampling during capture. All audio is CC0-1.0. The source engines and tools are MIT. Reference recordings used for analysis are excluded.

The repository carries pinned manifests, render recipes, and demos. Generated audio stays external, as in the existing sampler catalog. Build the exact published default banks, then verify every asset and recipe:

```sh
mkdir -p build/keys-demo
cp examples/keys/sampled/*.cicada build/keys-demo/
GOWORK=off go run ./cmd/cicada-keys-pack --out build/keys-demo/packs --patch all
GOWORK=off go run ./cmd/cicada-keys-audit --packs build/keys-demo/packs
GOWORK=off go run ./cmd/cicada render build/keys-demo/tine_ep.cicada -o build/keys-demo/tine_ep.wav
```

Capture is deliberately expensive. Check dimensions first with `cicada-keys-pack --estimate-only --patch all`. Existing output directories are refused; keep admitted packs until a new build passes the published catalog. Changed audio, JSON, recipes, or gzip bytes fail the audit. To generate one bank, use its patch name instead of `all`; the complete catalog audit requires every bank.

Every patch maps MIDI notes 21–108, with 30 recorded roots at minor-third intervals plus the highest key, five velocity centers (25, 51, 76, 102, 127), and two deterministic round robins. EP and Clav include mechanical releases. Organ, Soft Pad, and String Machine retain their attack before a crossfaded sustain loop. Each bank stays within the existing 256 MiB decoded PCM limit; the limit is unchanged.

| Patch | Assets | Compressed bytes | Decoded PCM bytes | Release zones | Loop zones |
| --- | ---: | ---: | ---: | ---: | ---: |
| `bell_keys` | 300 | 137978901 | 230400000 | 0 | 0 |
| `brass_stab` | 300 | 149646202 | 230400000 | 0 | 0 |
| `clav` | 600 | 97814465 | 241920000 | 300 | 0 |
| `clav_hollow` | 600 | 98755289 | 241920000 | 300 | 0 |
| `clav_muted` | 600 | 29371605 | 241920000 | 300 | 0 |
| `fm_bass` | 300 | 154103755 | 230400000 | 0 | 0 |
| `fm_ep` | 300 | 153912578 | 230400000 | 0 | 0 |
| `organ_full` | 300 | 164343820 | 230400000 | 0 | 300 |
| `organ_jazz` | 300 | 163703880 | 230400000 | 0 | 300 |
| `organ_soft` | 300 | 162475090 | 230400000 | 0 | 300 |
| `poly_keys` | 300 | 148248556 | 230400000 | 0 | 0 |
| `reed_ep` | 600 | 107684992 | 241920000 | 300 | 0 |
| `reed_tremolo` | 600 | 104646930 | 241920000 | 300 | 0 |
| `soft_pad` | 300 | 153653153 | 230400000 | 0 | 300 |
| `string_machine` | 300 | 159825504 | 230400000 | 0 | 300 |
| `sync_lead` | 300 | 159809086 | 230400000 | 0 | 0 |
| `tine_bark` | 600 | 156666059 | 241920000 | 300 | 0 |
| `tine_bell` | 600 | 149210991 | 241920000 | 300 | 0 |
| `tine_ep` | 600 | 153277131 | 241920000 | 300 | 0 |
| `tine_tremolo` | 600 | 160537728 | 253440000 | 300 | 0 |
| `tonewheel_organ` | 300 | 164385723 | 230400000 | 0 | 300 |

Use the [single-file score demos](../../examples/keys/sampled/) in native Studio after copying them beside generated `packs/`. Each demo contains a two-bar comping phrase and a two-bar melodic phrase. Four monophonic comping parts preserve the modeled chord notes and timing with eight total sampler slots, following the sampler score language's current monophonic pattern contract.

For a browser or game, serve generated banks with the optional sampler WASM, [`instrument-pack.js` and sampler worklet](../../docs/sampler/packs.md). Select the bank's manifest hash from `catalog.json` and send unique note IDs to its prepared eight-voice instance. Prepare PCM before resuming the context. Audio is loaded only for the requested bank; there is no download of every pack together and no disk streaming. Native Studio supports prepared sampled playback; the optional sampler browser host is a separate opt-in interface from the score worklet.

These are recorded takes of our models. Integer roots at velocity centers preserve their captured waveform after the sampler's two-millisecond onset ramp. Transposed pitches, velocity blends, arbitrary release timing, sustain loops, and the interaction of separately sampled rotary/drive voices approximate the model. Unlooped mono captures finish at four seconds; stereo captures finish at two seconds. Keep the modeled instruments for continuously evolving expression and long mechanical decays. See the [renderer contract and measurements](../../cmd/cicada-keys-pack/README.md) and [modeled keys controls](../../docs/manual/keys.md).
