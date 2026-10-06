# CC0 sample instrument catalog

The [full acoustic drum kit](full-kit/README.md) has a separate pinned catalog with CC-BY-4.0 CrocellKit recordings and CC0 VCSL cross-stick. The six packs below remain CC0.

Build the external packs, copy a demo beside them, and render it:

```sh
python3 tools/sampler/build_cc0.py --out demo/packs --cache sample-source-cache --verify-catalog assets/sampler/catalog.json
cp assets/sampler/demos/grand.cicada demo/grand.cicada
cicada render demo/grand.cicada -o demo/grand.wav
```

Python 3, GitHub CLI (`gh`) and FFmpeg are required. The pinned sources are public; no commercial DAW assets are used. `--verify-catalog` refuses rebuilt maps/audio that differ from the published hashes. The builder stages output and refuses to overwrite a different or corrupt existing pack. Keep the known-good pack when a new FFmpeg/zlib version changes bytes; inspect the difference before re-pinning. Source files are limited to their first eight seconds for this starter catalog. Sustained violin/trumpet regions loop from one to three seconds with a 200 ms overlap; piano, guitar and bass retain recorded decay. The builder retains source channels, converts to 48 kHz PCM24 (float32 for very quiet drums), strips container metadata, adds RIFF padding, and emits deterministic gzip files. Float32 drums preserve the quiet recorded tails without PCM24 quantization changing weak spectral bins.

Only JSON manifests, source/output checksums and scores are committed. Audio downloads total about 291 MB and are loaded when a host requests an instrument. A browser/game server serves the generated pack directory beside the optional sampler host modules. All sample assets are **CC0-1.0**; Cicada's MIT licence applies to code, not as a replacement for asset provenance. Each sample's manifest records its source URL, source SHA-256, licence evidence URL, compressed/WAV SHA-256 and dimensions.

| Pack | Recorded material | Compressed audio | Resident float32 PCM | Limits |
| --- | --- | ---: | ---: | --- |
| grand | VCSL acoustic grand; 13 roots across MIDI 21–108, up to four dynamics, dampening recordings | 79,386,196 B | 155,649,208 B | One take, sparse upper dynamics/releases; no resonance model |
| nylon | Freepats nylon guitar; 31 recorded roots | 13,222,317 B | 19,636,248 B | One recorded dynamic/take; no release/fret transitions |
| bass | Karoryfer fingered electric bass; 9 roots, four dynamics/four takes, releases | 98,796,132 B | 146,117,712 B | Sparse roots, mapped MIDI 23–66; no slides/fret transitions |
| kit | VCSL kick/snare/closed/open hats; recorded dynamics, two takes; hat choke | 18,877,620 B | 24,542,760 B | Open hat has one dynamic; no toms/ride/crash |
| violin | VSCO-2 CE solo violin; 11 roots, two dynamics, recorded vibrato | 46,638,714 B | 67,584,000 B | One take; no recorded legato; sustain loop can change vibrato phase |
| trumpet | VSCO-2 CE trumpet; 8 roots, two dynamics | 33,758,832 B | 48,302,184 B | One take; no recorded legato; no mutes/staccato set |

Cicada uses sounding MIDI pitch, with middle C at note 60. The piano/trumpet source filenames label middle C as C3; their roots are shifted up an octave. Bass filenames and the upstream SFZ map use written bass pitch, an octave above the recorded sound; roots are shifted down an octave. The lowest piano recording labelled A#-1 rings at A0 (its fourth partial is near 110 Hz) and is mapped explicitly to MIDI 21. Selected root recordings are qualified against measured periodic pitch; filename labels alone are insufficient. All packs retain the room/stereo content present in their sources; the nylon source is mono. There are no separately licensed impulse responses or proprietary reference recordings.

An upstream PCM24 recording has one incomplete trailing frame. FFmpeg reports and drops that incomplete frame; admitted pack WAVs have complete frames and checked dimensions. Published checksums pin both the upstream bytes and converted output.

Source licence evidence:

- [VCSL CC0 statement](https://github.com/sgossner/VCSL/blob/c1ea7bcc3c7309650ab0da9d15c9cd1fbc4a4c7e/README.md).
- [Freepats guitar CC0 licence](https://github.com/freepats/spanish-classical-guitar/blob/6f4eb1b092acc88f5448cea1a0001bd07b971af8/LICENSE).
- [Karoryfer bass CC0 statement](https://shop.karoryfer.com/pages/free-black-and-blue-basses) and [all free libraries' CC0 policy](https://shop.karoryfer.com/pages/free-samples).
- [VSCO-2 CE CC0 licence](https://github.com/sgossner/VSCO-2-CE/blob/440300901dfe9275fd84e0b7763af1f8443ae62e/LICENSE).

The owner's ears remain the final acceptance gate. This is a starter set with measured playback and honest recording coverage, not a claim of parity with a detailed commercial piano, guitar or orchestral library. See [pack hosting](../../docs/sampler/packs.md) and [quality targets](../../docs/sampler/quality.md).

## Download quality

Keep the original gzip banks for existing pinned scores. Build each new tier into its own directory from those verified banks:

```sh
GOWORK=off go build -o build/cicada-audio-encode ./cmd/cicada-audio-encode
python3 tools/sampler/build_cc0.py --tier lossless --from-packs demo/packs --out demo/packs/tiers/lossless --cache sample-source-cache --encoder "$PWD/build/cicada-audio-encode"
python3 tools/sampler/build_cc0.py --tier hq16 --from-packs demo/packs --out demo/packs/tiers/hq16 --cache sample-source-cache --encoder "$PWD/build/cicada-audio-encode"
```

The exact tier preserves original decoded PCM, using FLAC for integer recordings and gzip where float32 PCM cannot round-trip through FLAC. PCM16 FLAC quantizes after power-of-two scaling to preserve quiet recordings; it pins that reconstruction and is the default for new browser and Studio downloads. Both tiers retain the same maps, dynamics, takes, loops, licenses and resident float32 PCM. Catalog `tiers` entries pin each alternate manifest. Host admission verifies encoded hashes, dimensions and canonical decoded-PCM hashes before playback; codecs add no kernel code. The builder checks source pins, stages output and refuses to replace differing or corrupt packs. Use `--verify-catalog` (CC0) or `--verify` (full kit) with the corresponding published `tiers/<tier>/catalog.json` to verify a rebuild. Copy the published root catalog beside the generated banks to expose both tiers.

The six packs contain 165,591,092 encoded bytes in the exact tier or 75,795,475 bytes in PCM16 FLAC, versus 290,679,811 gzip bytes.
