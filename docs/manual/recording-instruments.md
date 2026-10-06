# Record your own instrument

In Studio, open **Record → Record your own instrument**, choose **Record
microphone**, tap 15 times from soft to hard, then choose **Stop and build**.
There is no count-in or accompaniment. Microphone monitoring is
off. You can instead choose or drop one or more PCM16/24/32 or float32 WAV files.

Play the root, third, fifth or octave buttons. **Playing velocity** selects the
recorded dynamic and controls output level. Repeated notes cycle through the
takes in each layer. **Silence** stops voices, including pending auditions.
Microphone capture uses the existing worklet and recoverable browser journal;
analysis and native pitch conversion run outside the audio callback.

The default is three velocity layers. Fifteen similar-pitch taps produce five
round robins per layer. Hits are sorted by their first 40 ms RMS before
normalization. The pack restores each hit's relative peak gain, so normalizing
the files does not erase the recorded dynamic differences. Pitch is estimated for every hit. By default, taps share the fallback root so
variations in resonance keep their round robins. Enable **Map detected note
pitches** (CLI `--auto-pitch=true`) for pitched takes: estimates with at least
80% periodicity confidence set individual note roots and tuning; unpitched hits
use the fallback root. The key map covers up to two octaves on
either side of each root, subject to source-rate and resampling bounds.

**Detected hits and pinned score declaration** shows the source-frame slices,
pitch estimates, confidence and measured loudness. **Download score** writes a
starter score beside which the pack's relative `assets/recorded/` directory
must be available. An `instrument.cicada` score is also saved inside each pack
directory and renders immediately with `cicada render`. Studio displays its
relative path. Pinned pack scores use the sampler-pack playback capability;
the recording panel's audition uses the original sample voice.

## Offline WAV packs

```sh
cicada record-pack -o assets/recorded/recorded --name recorded --root 60 --layers 3 taps.wav
cicada record-pack -o assets/recorded/notes --name notes --auto-pitch=true take-a.wav take-b.wav
cicada record-pack -o assets/recorded/taps --auto-pitch=false taps.wav
```

Put flags before file names. The command prints the hit count and the exact
sampler declaration. Packs are immutable: an existing directory is reused only
when every generated file is identical. Different contents require a new
directory. The CLI and Studio share the same analysis and writer.

Each pack contains `manifest.json`, `analysis.json`, `instrument.cicada`,
`LICENSE.txt` and one
losslessly compressed float32 WAV per hit. The manifest pins each compressed
file, decoded WAV and original input WAV with SHA-256. Its own checksum pins
the zone map and configuration. No input file names, absolute paths or device
identifiers are written into a pack. The license label is **user recording**;
it records the supplied audio's provenance and grants no public redistribution
rights.

Admission limits are 32 WAV files, 64 MiB input, 8,388,608 total source frames,
256 hits, eight layers and 32 takes per layer. Studio stops microphone capture
after 60 seconds. Incomplete capture and non-finite PCM are rejected. Capture
timing is uncalibrated; hit detection uses source-frame coordinates rather than
claiming calibrated score placement.

The fixture is synthetic inharmonic percussion, not acoustic listening
acceptance. Pitch tests use known decaying tones at 16, 44.1, 48 and 96 kHz.
Real sounds, rooms and microphones can need a different fallback root or
layer count. The current panel selects one layer at a time; the sampler-pack
host may blend neighbouring velocity centres when playing the pinned score.

The real browser capture fixture reached playback in 4.1 seconds, including
the 3.5-second recording, with three layers and five round robins. The native
recorded-instrument render check measured zero allocations. Captured views:
[desktop, 1440×1000](screenshots/recorded-instrument-1440.png) and
[mobile, 390×900](screenshots/recorded-instrument-390.png).

## Fit a playable model

After recording or importing hits, choose a **Take** and press **Fit model**.
Studio estimates up to ten resonant frequencies, their relative amplitudes and
their decay times. The modeled instrument is selected automatically. Play the
same note buttons and velocity slider; **Play sampled** returns to the original
recording. The source WAV hash and the exact trimmed-hit WAV hash remain in
`model.json`, with the `user recording` license.

The fitted voice uses the modal engine's bounded contact, resonator, glide and
four-strike variation paths. Measured ratios and decay poles replace the built-in
profile's poles. It accepts every MIDI note, omits modes above the passband, and
allocates no memory during note handling or rendering. Fitting, file admission,
WAV auditions and pack construction happen outside audio rendering.

For immediate score playback, Studio also bakes the fitted engine at five root
notes and three dynamics into a standard pinned sampler pack. The manifest pins
the model JSON checksum as well as the generated audio. **Download score** and
the saved `instrument.cicada` use this pack. Between baked roots, score playback
resamples the modal renders; decay duration changes with that resampling. Studio
audition evaluates the fitted resonators directly at the requested pitch.

The offline equivalent uses the same fitter and bank builder:

```sh
GOWORK=off go run ./cmd/cicada fit-model -o assets/recorded_model \
  --name recorded_model --hit 1 recordings/taps.wav
GOWORK=off go run ./cmd/cicada render assets/recorded_model/instrument.cicada \
  -o recorded-model.wav
```

Put flags before the WAV path. `--hit` is one-based after automatic slicing.
Fitting rejects silent, invalid or very short input. A 60 ms attack spectrum
identifies separated modes; windowed amplitude measurements estimate exponential
decay. Closely spaced modes, rapidly changing pitch, noisy rooms and nonlinear
impacts can reduce fit quality. The displayed decay confidence describes that
regression, not perceptual similarity. Try another take when a fit sounds poor.

The synthetic microphone fixture plays both versions in about four seconds,
including its 3.5-second recording. Known three-mode fixtures at 16, 44.1, 48 and
96 kHz meet ten-cent frequency and 15% decay-error bounds. The fitted voice maps
all 128 MIDI notes and matches 65,536 native/WASM samples exactly, without render
memory growth. This is fixture evidence; a physical sound source recording still needs
listening evaluation. Captured [modeled desktop](screenshots/modeled-instrument-1440.png)
and [modeled mobile](screenshots/modeled-instrument-390.png) views show the controls.
