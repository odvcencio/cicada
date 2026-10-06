# Render owned keyboard sample packs

`cicada-keys-pack` records Cicada's original modeled keyboards into the pinned `cicada.instrument-pack/1` format. Audio uses CC0-1.0; every sound comes from our modeled engines. No reference recordings or third-party patch data enter the output.

```sh
GOWORK=off go run ./cmd/cicada-keys-pack --estimate-only --patch all
GOWORK=off go run ./cmd/cicada-keys-pack --out ./owned-key-packs --patch all
```

Each patch gets its own directory containing `manifest.json`, its SHA-256 pin, gzip-compressed PCM24 WAV samples, a render recipe, a measurement report, and a README. Existing patch directories are rejected. A pack appears at its final name only after its files and map pass validation.

For a small pack with two roots:

```sh
GOWORK=off go run ./cmd/cicada-keys-pack --out ./small-key-packs \
  --patch tine_ep --low 60 --high 63 --duration .25
```

## Capture controls and unchanged limits

| Flag | Default | Meaning |
| --- | --- | --- |
| `--out` | required | Output directory. Every selected patch gets a separate subdirectory. |
| `--patch` | `all` | Any of the 21 modeled patch names, or every patch. |
| `--step` | `3` | Root interval: 1 for chromatic or 3 for minor thirds. The highest requested key always gets a root. |
| `--layers` | `5` | 5..16 velocity centers with nonoverlapping ranges. Five centers are 25, 51, 76, 102, and 127. |
| `--round-robins` | `2` | 2..32 recorded takes for each root and velocity layer. |
| `--low`, `--high` | `21`, `108` | MIDI key range. Zones select the nearest recorded root. |
| `--duration` | automatic | Held capture length in seconds; default 4 seconds mono or 2 seconds stereo. |
| `--rate` | `48000` | Output rate; the model always renders at 192000 Hz. |
| `--estimate-only` | false | Check all requested dimensions and print decoded PCM sizes before writing audio. |

The first take uses the named patch's physical controls. EP internal oversampling increases to four for maximum offline quality. Later takes use small deterministic pickup/felt, tangent, detune, ensemble-rate, or shared-oscillator phase variations. The render recipe records the base controls and grid without machine-specific paths.

The renderer detects mono for dry EP and Clav patches and verifies every captured frame remains mono. Other families retain stereo. A centered 385-tap windowed sinc with a 21 kHz cutoff filters the 192 kHz output before fourfold decimation. Guard frames preserve attack alignment. WAV quantization, gzip headers, file order, and JSON are deterministic. Source SHA-256 hashes the generated high-rate stereo float32 stream, including guard frames, before decimation and end fades. Asset pins independently protect the WAV and gzip bytes; the manifest has its own pin.

Decoded float32 PCM remains bounded by the existing 256 MiB admission limit. Defaults use 30 roots, five layers, and two round robins:

| Pack kind | Capture | Decoded PCM |
| --- | --- | ---: |
| Mono EP/Clav, with releases | 4 s + 0.2 s release | 241920000 bytes |
| Stereo Tine Tremolo, with releases | 2 s + 0.2 s release | 253440000 bytes |
| Other stereo patches | 2 s | 230400000 bytes |

An oversized request fails with its size estimate. The renderer also preserves the 4096-asset/zone limits and sets eight sampler voice slots. Zone gain compensates the sampler's velocity multiplier with `127 / velocity_center`, plus any headroom scaling needed for PCM24. It remains below 16. The 16-layer maximum keeps the lowest equally spaced center at eight or above, preserving that gain bound.

## Loops and release samples

Tonewheel registrations, Soft Pad, and String Machine use sustain loops. The search retains their initial attack, finds a lower-error match between the end and a later sustain window, and uses at least a 40 ms crossfade. Default organ percussion is retained through at least 0.75 s; slow-percussion Organ Jazz through 1.4 s; Soft Pad through 1.075 s; String Machine through 0.28 s. The long slow-percussion tail can still contribute to a repeated sustain window. Loops approximate evolving rotary, pad, and ensemble motion.

EP and Clav releases capture the model state after 0.125 s, or the shorter requested duration, so short-decay voices retain their mechanical key-off sound. The release residual subtracts a matched held signal with the nominal exponential damper curve. This retains key-off noise and nonlinear damping residuals while avoiding a second complete sustained body. Residual PCM may have either polarity. The sampler's linear release envelope and arbitrary held-note duration make release playback an approximation.

Unlooped captures fade at their end; a held note finishes with the recorded take. Captured effects become part of each sampled voice. Shared percussion rearming, common rotary/drive interactions between chord notes, and live mechanical pedal state belong to the modeled engine. Integer roots at recorded velocity centers give the closest single-take match; other notes or velocities transpose or scale the selected recording.

## Measured playback error

Focused tests admit generated packs through the real manifest/asset loader, verify complete layers and round robins, exercise releases and loop wrapping, reject oversized maps, and compare every generated byte across repeated renders.

At root 60, measured over the first 125 ms of Tine EP, replay after the sampler's mandatory 2 ms onset ramp has peak amplitude error below `0.00000006` against the high-rate captured model. Normalized RMS error spans `0.00000034..0.00000446` across the five velocity centers. Including the onset ramp, normalized error spans 7.72..8.79%. The live default 48 kHz model compared with the 192 kHz maximum-quality recording has 3.63..12.92% normalized error after the onset ramp; sample rate, oversampling, filtering, and phase differences remain observable.

The FIR measures 1 kHz gain `0.999999990` and 28 kHz rejection `-126.06 dB`. A one-second organ capture with a retained percussion prefix measures a loop-wrap jump of `0.001704`, versus `0.005554` for the largest natural adjacent-frame change in its sustain region. Its normalized pre-crossfade seam mismatch spans `0.401337..0.498584`; the crossfade removes the abrupt seam, while repeated modulation remains an approximation.

```sh
GOWORK=off go test ./cmd/cicada-keys-pack -v
GOWORK=off go vet ./cmd/cicada-keys-pack
```

Pack distribution is separate from this source renderer. Generated audio belongs outside the repository until its distribution and the sampler integration are ready.

## Playback and verification limits

The modeled family requires the keyboard integration. Native playback includes
the engines; Studio loads the separate `cicada-keys.wasm` module only for modeled
keys scores. Generated packs use the existing sampler module and its unchanged
admission limits. This command adds no browser kernel code.

Switching Studio between core and modeled-keyboard modules keeps the
AudioContext and estimated transport position, resets held notes and effect
tails, and finishes and disarms active capture before replacing the module.
The owner must arm capture again. Module lifecycle tests cover this behavior;
a real browser core/keys switching receipt remains outstanding. Native live
keyboard input supports velocity, sustain and MIDI-targeted release. Modeled
keys reject per-note pitch, pressure and timbre expression explicitly.

Candidate-pack QA admitted all 21 banks and 9000 assets through the actual
pinned native loader, checking their compressed/WAV hashes and dimensions.
The largest bank decoded to 253440000 bytes, below 268435456. The high-rate
source stream was regenerated for two selected takes per bank: 42 source
hashes matched. Those spot checks do not establish reproduction of every
high-rate source hash. Generated candidates and their QA reports remain
outside this source-only change; listening acceptance and audio distribution
are separate deliverables.
