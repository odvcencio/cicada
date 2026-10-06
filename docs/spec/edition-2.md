# Cicada source edition 2

Edition 2 keeps the validated language and mixer behavior of edition 1 while requiring the named mixer forms. The complete grammar is in the [EBNF appendix](appendix.ebnf); validation applies the edition-specific rules below. Source edition and semantic JSON format are independent: edition 2 mixer projects are represented in `cicada.project/2`.

## Selecting the edition

**Status:** Implemented.

**Syntax:** A loose score can begin with `cicada 2`. A project selects the edition in `cicada.mod`:

```text
project sketch
cicada 2
```

**Meaning:** A loose score with no header defaults to edition 1. In a project, the manifest selects the edition; if a score also has a source header, it must match the manifest.

**Errors:** Unsupported editions and a source-header/manifest mismatch report `CICADA-VERSION`.

## Multi-file manifests

A manifest may add `entry "main.cicada"`, repeated `source "parts/voice.cicada"` directives, `license "MIT"`, and `author "Cicada contributors"`. These project directives also work in edition 1. They do not change the source grammar. Paths are explicit project-relative `.cicada` files; traversal, absolute paths, glob patterns, and escaping symlinks are rejected.

All listed files compile together. The entry loads first, followed by the remaining files in sorted path order. Source headers are optional and must match the manifest. A manifest without `entry` or `source` keeps its existing single-file behavior. See [multi-file projects](accepted.md#multi-file-projects-and-manifest-metadata) for load errors and the implemented tool support. Imports, `require`, and `cicada.sum` remain accepted-only.

## Named mixer forms

**Status:** Implemented for the routes listed in [edition 1](edition-1.md#named-effects), [buses and master](edition-1.md#buses-and-master), and [track mixer settings](edition-1.md#track-mixer-settings).

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

**Status:** Experimental research prototype. No human listening acceptance is claimed.

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
bit 0 are reserved for the chord/schedule lanes (#100/#103). This extension
does not change graph opcodes, the 24-byte command ABI or the worklet asset.
Integration with those lanes must preserve their version-14 payloads before
combining features in version 15; their unmerged layouts are not interpreted
as legacy images here.

See [the riff example](../../examples/expressive-guitar.cicada) for held-note
bends, slides, palm mutes and clean/drive contrast. Zero drive still includes
the prototype amp's coloration. It is neither a measured pickup nor a circuit
or cabinet emulation; it has no sympathetic strings, fret buzz or feedback.

## Migrating with `cicada fix`

**Status:** Implemented.

**Syntax:** Run `cicada fix score.cicada`; use `cicada fix score.cicada --check` to check whether a source rewrite or manifest update is needed. From the project folder, `cicada fix --all` migrates all edition-1 scores together; `--check` can be combined with either form.

**Meaning:** The command rewrites recognized edition-1 spellings wherever they occur, including in a project whose manifest already selects edition 2. It preserves unrelated text and layout, then creates a `cicada.mod` at edition 2 or upgrades an existing edition-1 manifest when needed. `send_a` maps to the declared delay effect and `send_b` to the declared reverb effect. `send_pre = true` moves the pre-fader tap onto each migrated send; `send_pre = false` is removed. `bus = sfx` becomes `out = sfx`; the default `bus = music` is removed. Track `level = off` becomes `mute = on`. Legacy `fx comp {}` becomes a named compressor and is inserted on the music bus unless that insert is already present.

The migration requires valid source before editing and checks typed semantic equality after rewriting. `TestFixNamedMixerMigrationIsTypedAndPCMExact` also renders the covered examples and migration fixture before and after, then compares their PCM24 hashes.

**Errors:** Invalid input, unsupported source editions, ambiguous rewrites, and any semantic change stop the migration. Before writing source or creating/upgrading a manifest, a single-score migration checks for sibling edition-1 scores and refuses with their paths; use `cicada fix --all` to migrate them together. A source header must be standalone for `cicada fix` to remove it safely.
