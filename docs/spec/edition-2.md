# Cicada source edition 2

Edition 2 keeps the validated language and mixer behavior of edition 1 while requiring the named mixer forms. The complete grammar is in the [EBNF appendix](appendix.ebnf); validation applies the edition-specific rules below. Source edition and semantic JSON format are independent: edition 2 mixer projects are represented in `cicada.project/2`.

## Selecting the edition

**Syntax:** A loose score can begin with `cicada 2`. A project selects the edition in `cicada.mod`:

```text
project sketch
cicada 2
```

**Meaning:** A loose score with no header defaults to edition 1. In a project, the manifest selects the edition; if a score also has a source header, it must match the manifest.

**Errors:** Unsupported editions and a source-header/manifest mismatch report `CICADA-VERSION`.

## Multi-file manifests

A manifest may add `entry "main.cicada"`, repeated `source "parts/voice.cicada"` directives, `license "MIT"`, and `author "Cicada contributors"`. These project directives also work in edition 1. They do not change the source grammar. Paths are explicit project-relative `.cicada` files; traversal, absolute paths, glob patterns, and escaping symlinks are rejected.

All listed files compile together. The entry loads first, followed by the remaining files in sorted path order. Source headers are optional and must match the manifest. A manifest without `entry` or `source` keeps its existing single-file behavior. See [multi-file projects](features.md#multi-file-projects-and-manifest-metadata) for load errors and the implemented tool support. Library imports and `cicada.sum` hash pins are supported; version requirements are not supported.

## Named mixer forms

**Syntax:** Edition 2 uses named effect declarations, named sends, per-send taps, named inserts, built-in bus declarations, and a separate mute switch:

```cicada
cicada 2
fx room delay {}
fx hall reverb {}
fx glue comp {}
bus music { insert = glue }
master { level = -1dB mute = off }
track bass acid {
  level = -6dB
  mute = off
  send room = 0.25 pre
  send hall = -12dB
  insert = none
  out = music
}
pattern pulse acid { 1 . 1 . }
scene main { bass = pulse }
song { main }
```

The music and SFX buses exist without declarations. `bus music` may select the music compressor; `bus sfx` configures the built-in SFX bus. Track sends name a declared delay or reverb effect. A send's optional `pre` applies only to that send. Insert chains use `a -> b`, but this edition supports at most one insert per route. Track and master `mute` is independent of `level`.

**Removed spellings:** Edition 2 reports `CICADA-VERSION` for the edition-1 shorthand `fx delay {}`, track `send_a`, `send_b`, `send_pre`, `bus = music` or `bus = sfx`, and track `level = off`. Use `fx <name> <kind>`, `send <effect> = <level> [pre]`, `out`, and `mute = on` instead. `level = off` is not emitted by `cicada fix`; it becomes `mute = on`, preserving the track's stored level.

**Unsupported routes:** A syntactically valid topology without engine support reports `CICADA-UNSUPPORTED`:

```cicada-invalid CICADA-UNSUPPORTED
cicada 2
track bass acid { send music = 0.2 }
pattern pulse acid { 1 }
scene main { bass = pulse }
song { main }
```

Unknown effect and bus names report `CICADA-REFERENCE`. Supported effect kinds, send levels, insert placement, and bus controls are specified in edition 1's mixer sections.

## Experimental guitar voice

**Syntax:** Select the built-in `guitar` voice with an explicit, saved opt-in:

```cicada
cicada 2
track lead guitar { experimental = on brightness = 0.7 pickup = 0.22 }
pattern riff { 1^ . 3 5~ 6 - . . }
scene clean { lead = riff }
scene muted { lead = riff lead.damping = 0.8 lead.drive = 0.7 }
song { clean muted }
```

`guitar` is a reserved built-in name. Edition 1 reports `CICADA-VERSION`;
omitting the opt-in or selecting `experimental = off` reports
`CICADA-EXPERIMENTAL`. JSON stores the opt-in in `track.params.experimental`
and validates it before engine compilation. The opt-in cannot be changed by
a scene or live command.

**Controls:** These numeric controls use the shared parameter registry. The
language server, Studio parameter addresses/MIDI mappings and `cicada explain`
use the same types, units, ranges and smoothing. Bend and vibrato use bare
numbers, as do other pitch offsets; ratios also accept percent literals.

| Parameter | Type / unit | Range | Default | Smoothing | Model mapping |
| --- | --- | --- | --- | --- | --- |
| `bend` | number / semitones | −12..12 | 0 | 8 ms | Sequenced Hz × 2^(bend/12) |
| `vibrato` | number / cents | 0..100 | 0 | 8 ms | Depth of the model's 5 Hz pitch modulation |
| `brightness` | number / ratio | 0..1 | 0.7 | 8 ms | Frequency-dependent string loss |
| `damping` | number / ratio | 0..1 | 0 | 8 ms | Palm damping; higher values shorten decay |
| `pickup` | number / string length ratio | 0.05..0.45 | 0.22 | 8 ms | Pickup comb and pluck position |
| `drive` | number / ratio | 0..1 | 0 | 8 ms | Built-in 4× oversampled, antialiased amp |

`octave` is an integer in 0..6, defaults to 2, and applies when compiling
relative pitches. Ordinary mixer settings still apply. Wrong control units
report `CICADA-UNIT`; unknown controls and out-of-range track controls report
`CICADA-PARAM`. Scene settings use the existing registry diagnostics.

**Articulation:** Each note-on replucks one modeled string. A `~` slide changes
pitch with the same 8 ms glide while its gate remains open; it does not repluck.
A slide from a closed gate starts a new pluck. Ties retain the string and gate;
note-off releases the string. Velocity sets pluck strength. The guitar ignores
the acid-specific accent timbre flag. Bend changes the sounding pitch without
re-excitation. The model clamps pitch, including bend, to 40..2000 Hz.

**Runtime:** Native playback, the production TinyGo AudioWorklet kernel and
offline WAV/stem rendering use the same physical model and amp. One track uses
one voice from the unchanged 32-voice, 16-track core limits. Storage is prepared
before rendering; note/control/render callbacks allocate nothing. At 48 kHz the
string delay stores 2,408 float64 samples (19,264 bytes), plus fixed voice/amp
state. Guitar is available in the core profile within its unchanged size gates.

**Image format:** Guitar images use version 15 with voice kind 4 and six
float64 control values in registry order. Existing voices retain version 13
encoding; shipped versions 8..13 remain readable. Version 14 and capability
bit 0 support chords and bounded graph polyphony. Version 15 retains that
layout and adds the guitar payload, so guitar and chord tracks can share a
project. This extension does not change graph opcodes or the 24-byte command
ABI.

See [the riff example](../../examples/expressive-guitar.cicada) for held-note
bends, slides, palm mutes and clean/drive contrast. Zero drive still includes
the prototype amp's coloration. It is neither a measured pickup nor a circuit
or cabinet emulation; it has no sympathetic strings, fret buzz or feedback.

## Graph delays and plucked strings

**Syntax:** `delay(audio, ms)` and `comb(audio, ms, unit, unit)` are ordinary typed function calls in an instrument's mono voice. Unit divided by Hz produces ms: `1 / pitch` is one period, and `2 / pitch` is two periods. Bare `1 / 440` remains a unit value and cannot supply a delay time. Semantic JSON preserves Hz-derived division as the two-argument `period` expression operator; source conversion writes it back as `/`, retaining the numerator's unit type and denominator's Hz type even for numeric literals.

**Meaning:** `delay(x, time)` reads a fractional tap with linear interpolation. It suits slapback and moving taps because it has no recursive interpolation state. `comb(x, time, feedback, damping)` delays its input and feeds the output back through the one-pole filter `filtered[n] = (1-damping)*output[n] + damping*filtered[n-1]`. Zero damping passes the output unchanged; increasing damping removes high frequencies. Zero feedback makes a single delayed pass.

The comb uses a first-order allpass fractional section to preserve loop gain. Its `time` denotes the complete loop period, including damping. The kernel subtracts the damping filter's phase delay at `1000/time` Hz and tunes the allpass phase at that same frequency. This avoids the pitch offset from a low-frequency allpass approximation or an uncompensated damping filter. Higher modes remain dispersive. Control changes reuse the existing rings and can produce transients; no crossfade is implied.

**Limits:** Every delay or comb reserves 4,096 float32 samples (16,384 bytes) before rendering. A core voice can contain at most two such nodes, including unused bindings: 8,192 samples (32,768 bytes) of ring storage plus fixed executor and control state. Voices without delay nodes reserve no rings. `delay` admits 1–4,096 samples; `comb` admits 4–4,096 samples. Maximum time is 92.8798 ms at 44.1 kHz, 85.3333 ms at 48 kHz, and 42.6667 ms at 96 kHz. Feedback and damping are unit values from zero inclusive to one exclusive.

**Errors:** Wrong units report `CICADA-UNIT`; invalid constant times, feedback, damping, track overrides, or pitch-derived times for sequenced graph notes report `CICADA-PARAM`. `cicada check` checks times at 48 kHz; voice construction checks constants at the selected render rate. A third ring reports `CICADA-LIMIT` before playback. Runtime modulation and live notes clamp controls to the render rate's sample bounds; non-finite controls use the lower bound. Reset clears rings; note on and slide retain their tails, as for the existing graph filters. The language server uses the same checks. `cicada explain graph.delay` and `cicada explain graph.comb` share their descriptions with editor hover.

**Example:** A noise burst excites a tuned Karplus–Strong loop. [pluck.cicada](../../examples/pluck.cicada) also uses `delay` for a 60 ms slapback:

```cicada
cicada 2
instrument plucked {
  voice mono {
    let burst = noise() * env(gate, 1ms) * velocity
    let string = comb(burst, 1 / pitch, 0.995, 0.5)
    out = string * env(gate, 1600ms) * 0.3
  }
}
track strings plucked {}
pattern melody notes { a2 . c3 . e3 . a3 . }
scene main { strings = melody }
song { main }
```

**Kernel images:** Delay graphs keep the version-13 layout and require capability bit 1 in the previously reserved 16-bit header word. `gosx_audio_capabilities` advertises that bit. Legacy images keep a zero capability word, and versions 8–13 remain readable. Older readers reject a required capability before playback. Graph opcodes 24 and 25 carry delay and comb; opcode 26 converts a Hz-derived period to ms. The comb's fourth input occupies the existing node value word as an exact integer index, so node records stay eight bytes. Graph and command opcodes have separate namespaces.

## Graph phase modulation

**Syntax and types:** `pm(carrier_hz, modulator, index)` takes Hz, audio, and unit and returns audio. Index is the phase deviation in radians for a unit-amplitude modulator; it accepts an envelope and negative values. `pm(pitch, sine(pitch * 3.5), 4 * env(gate, 260ms))` makes a two-operator voice. The sine modulator gives the classic FM sideband spectrum, with brightness controlled by the index envelope. No feedback edge is permitted.

**Meaning:** The carrier emits `sin(2*pi*phase + modulator*index)`. Its stored float32 phase advances by the carrier frequency divided by the render sample rate and wraps into `[0,1)`. The offset product is evaluated in float64 and reduced modulo one turn before sine evaluation; it never accumulates into carrier state. Very large controls lose fractional phase precision. Carrier frequency clamps to `0..0.49*sample_rate`; zero emits silence. A non-finite frequency clamps to zero; a non-finite offset emits silence while carrier phase continues. A fresh note resets both operators; slides preserve phase and use the existing pitch glide. Reset clears phase. Execution uses the same graph on native, WASM, and offline paths.

**Limits and errors:** PM adds one stateful node and no delay storage. The existing limits of 128 graph nodes and 32 stateful nodes remain. Wrong units report `CICADA-UNIT`; wrong arity reports `CICADA-PARAM`. Source conversion and semantic JSON retain the three typed inputs. Language-server hover and `cicada explain graph.pm` share the operation description. Existing one-input oscillators retain their signatures.

**Aliasing:** PM is not antialiased. Clamping carrier frequency does not bound modulation sidebands. Keep the example's bell at or below E5 (659.255 Hz), with modulator ratio 3.5 and index at or below 4; higher pitch, ratio, or index can alias strongly. These settings do not guarantee alias-free output. The feedback-free bass uses a 1:1 ratio and peak index 1.5. See [fm-bell.cicada](../../examples/fm-bell.cicada).

**Image compatibility:** Additive in source editions 1 and 2. Graph opcode 32 is PM; the eight-byte node record uses its three existing input indices. Image version 13 requires capability bit 5 for PM in tracks or kit lanes, alongside I-1's delay bit 1; bit 0 stays reserved for chords. `gosx_audio_capabilities` advertises both supported bits. Legacy images retain their capability word and versions 8–13 remain readable. Readers without PM reject its required capability before playback.

## Migrating with `cicada fix`

**Syntax:** Run `cicada fix score.cicada`; use `cicada fix score.cicada --check` to check whether a source rewrite or manifest update is needed. From the project folder, `cicada fix --all` migrates all edition-1 scores together; `--check` can be combined with either form.

**Meaning:** The command rewrites recognized edition-1 spellings wherever they occur, including in a project whose manifest already selects edition 2. It preserves unrelated text and layout, then creates a `cicada.mod` at edition 2 or upgrades an existing edition-1 manifest when needed. `send_a` maps to the declared delay effect and `send_b` to the declared reverb effect. `send_pre = true` moves the pre-fader tap onto each migrated send; `send_pre = false` is removed. `bus = sfx` becomes `out = sfx`; the default `bus = music` is removed. Track `level = off` becomes `mute = on`. Legacy `fx comp {}` becomes a named compressor and is inserted on the music bus unless that insert is already present.

The migration requires valid source before editing and checks typed semantic equality after rewriting. `TestFixNamedMixerMigrationIsTypedAndPCMExact` also renders the covered examples and migration fixture before and after, then compares their PCM24 hashes.

**Errors:** Invalid input, unsupported source editions, ambiguous rewrites, and any semantic change stop the migration. Before writing source or creating/upgrading a manifest, a single-score migration checks for sibling edition-1 scores and refuses with their paths; use `cicada fix --all` to migrate them together. A source header must be standalone for `cicada fix` to remove it safely.
