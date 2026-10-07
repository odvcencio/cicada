# Exporting

The renderer and exporters use the same validated project model as Studio.
Choose the format that fits your next step.

## WAV

Render stereo PCM16 or PCM24 (the default), or IEEE float32:

```sh
cicada render examples/first-acid.cicada -o first-acid.wav --rate 48000 --bits 24
cicada verify-wav first-acid.wav --rate 48000 --bits 24 --bars 16 --tail 3s
```

The engine supports 44.1, 48, and 96 kHz. A render includes a three-second
tail by default. Integer PCM output uses deterministic triangular dither.
`--normalize` peak-normalizes the finished file to −1 dBFS; it does not target
a loudness standard.

Use `--from N` and `--bars M` to select a range. Bar numbers start at 1, so
`--from 1` selects the first bar. The legacy value `--from 0` still selects the
first bar and prints a deprecation warning. `--bars 0` renders from that bar
through the end of the song. Cicada processes
the earlier bars before writing the selected range, so instruments and effects
have the state they would have reached in a full render. Pass the same range
options to `verify-wav`.

The render summary reports duration, pre-limiter peak, limiter activity, and
output clipping. `verify-wav` checks sample rate, bit depth, duration, peak,
DC offset, finite float data, and full-scale integer samples.

## Stems

Stems are stereo float32 WAV files rendered in one pass. The destination
directory must not already exist:

```sh
cicada stems examples/sfx-bus.cicada -o sfx-stems --bars 4
cicada verify-stems sfx-stems --tap pre-comp --residual-max-db -80
```

The directory contains numbered `01-<track>.wav` files, then `return-a.wav`,
`return-b.wav`, `music.wav`, `sfx.wav`, and `master.wav`, plus
`manifest.json`. The manifest records the routing and selected song range.
The music stem is tapped before the music-bus compressor; the master includes
compression and limiting. The verifier checks timing, finite samples, and the
sum of component stems against the bus taps.

## MIDI

MIDI export writes a Standard MIDI File type 1 at 960 pulses per quarter note:

```sh
cicada midi examples/first-acid.cicada -o first-acid.mid
cicada verify-midi first-acid.mid --ppq 960 --type 1
```

The file contains one track per Cicada track, tempo, meter, key signature, and
General MIDI drum notes. Swing and ratchets affect tick positions; a slide
creates a one-tick note overlap. `--pattern NAME` exports one pattern loop
instead of the full song. Chance uses the first seeded pass on every
repetition. MIDI import is not available.

To package a score as a browser instrument with live macros and MIDI input,
see [WAM2 instruments](wam2.md).

## Semantic JSON

If another tool needs the typed project instead of audio, use
`cicada convert score.cicada -o score.json`. JSON is a semantic interchange
format: note spellings are resolved to MIDI notes, source layout is omitted,
and defaults are materialized. Convert it back to normalized Cicada source and
use `cicada compare --semantic` to check that both files mean the same thing.
The [semantic model](../spec/semantic-model.md#semantic-json) documents the
schema, field catalog, and round-trip rules.
