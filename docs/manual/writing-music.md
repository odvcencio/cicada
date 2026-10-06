# Writing music

Start with one voice, one pattern, and one scene. Then add another part when
you know what the first one should do.

```cicada
cicada 2
title "Night circuit"
tempo 138
key a minor
seed 4242

track bass acid {}
track drums drums {}

pattern bassline acid {
  swing = 54%
  gate = 58%
  1^ . 1~ 5 . 1 7, . | 1^ . 1'~ 1 5^ 3 1 .
}

pattern beat drums {
  bd: X... x... X... x.x.
  sd: .... X... .... X..x
  ch: x.x. x.x. x.x. x.x.
}

scene main {
  bass = bassline
  drums = beat
}

song { main*8 }
```

This score makes a short bass phrase and drum groove, binds both patterns to a
scene, and plays that scene for eight bars. Check the score before you open it:

```sh
cicada check
cicada fmt --check
```

Run those commands from the project directory. `check` validates every score
in the project. `fmt --check` reports files that need formatting without
changing them.

## Split a score into files

List the files in `cicada.mod` when patterns or instruments need their own source file:

```text
project multifile
cicada 2
entry "main.cicada"
source "main.cicada"
source "parts/patterns.cicada"
source "parts/voices.cicada"
license "MIT"
author "Cicada contributors"
```

The [multi-file example](../../examples/multifile/main.cicada) puts scenes and the song in `main.cicada`, a phrase and patterns in `parts/patterns.cicada`, and instruments and tracks in `parts/voices.cicada`. Every file shares the project's declarations. References can point forward or into another file without an import. Each file must contain complete declarations.

The entry loads first; the remaining sources load in sorted path order. The entry is included even without its own `source` line. Files outside the explicit list are ignored. Paths must stay inside the project, including through symlinks; use ordinary relative paths without `..`, absolute paths, or globs. `license` stores one SPDX identifier, quoted or bare. `author` stores a quoted string. Both are optional. Existing manifests with only `project` and `cicada` keep working unchanged.

Run these commands from the example folder:

```sh
cicada check
cicada fmt --check
cicada explain main.cicada bass.cutoff @1.1.1
cicada play main.cicada
cicada render main.cicada -o shared.wav --rate 48000 --bits 24
```

A command given any listed source loads the whole project. `check` reports errors at the right file, line, and column; a duplicate also identifies the first declaration. `fmt` keeps files separate and retains comments and source spelling. To migrate an edition-1 project, use `cicada fix --all`; the command checks the complete project before writing each file and upgrading the manifest.

Multi-file migration requires atomic file exchange, available on supported Linux and Windows filesystems. It refuses before writing project files when that operation is unavailable. Editor saves made during publication cause a revision conflict and rollback. Displaced versions and their `.revision` receipts remain in the project directory so writes through already-open editor handles can be recovered. If a later command reports an external write or incomplete save in a recovery file, close other editors, compare and merge that version with the project file, then move the recovery file and its receipt out of the project directory before retrying.

The language server uses unsaved buffers alongside the other listed files. Go to definition and rename work across files. Save As copies all sources and their assets. Studio currently refuses projects with more than one source file with a message directing you to a text editor, check, play, or render. Its editing and undo history remain available for single-file projects.

## Import a library

A library packages instruments, effects, kits, phrases, patterns, samplers, and audio assets for reuse. The [library example](../../examples/libraries/main.cicada) imports `demo/tone` from the project's `lib/demo/tone/` folder. Its manifest uses `library` instead of `project`:

```text
library demo/tone
cicada 2
source "tone.cicada"
license "MIT"
author "Cicada contributors"
engine 2
capabilities 0
```

A library needs an explicit source list, license, and author. It has no entry score. `cicada` declares its source edition; `engine` declares its minimum engine edition and defaults to the source edition. `capabilities` is an unsigned engine capability bit mask, decimal or hexadecimal, and defaults to zero. This engine supports editions through 2 and no optional capability bits. Unsupported requirements produce `CICADA-LIB-CAPABILITY`. A library cannot declare tracks, scenes, songs, score headers, buses, clips, mastering settings, exports, or live controls; these produce `CICADA-LIB-DECL`. An optional source edition marker must match its library manifest. An importing score accepts libraries from the same or an older source edition. Edition-1 effect shorthand retains its processor kind as a qualified named effect; edition-2 library source still requires an explicit kind.

In a score, import the path and use its final path component as the namespace:

```text
import "demo/tone"
track lead tone.glass {}
scene verse { lead = tone.melody }
song { verse*2 }
```

Libraries can import other libraries. Imports in all files of a project or library share that scope. A library's unqualified references select its own declarations; dependency references use the imported namespace. Dependencies are not re-exported. Names beginning with `_` are private to their library; referring to `tone._hook` from outside it produces `CICADA-LIB-PRIVATE`. Import cycles produce `CICADA-LIB-CYCLE`.

Paths use slash-separated source identifiers. `builtin` is reserved as an import namespace and as the first component of a library path; built-in drum recipes such as `builtin.ch` remain available. Resolution checks embedded `std/` libraries, the project's `lib/`, and the user library in that order. The embedded standard namespace is initially empty. The user library is Go's per-OS user config directory plus `cicada/lib`; `$CICADA_LIBRARY` overrides it. Direct imports from it are allowed. If the same path exists in more than one location, two imports share the same final component, or an import namespace conflicts with a local declaration, loading fails with `CICADA-LIB-SHADOW`.

Library assets live under the library's `audio/` folder. Their source paths resolve from that library root, and their own declared audio hashes are verified as well. Source and asset paths cannot escape the library through traversal or symlinks. `cicada convert` refuses library-owned audio with `CICADA-LIB-ASSET` before writing output. Keep the score imports until asset vendoring is supported.

Run these commands from `examples/libraries/`:

```sh
cicada lib update
cicada check
cicada explain main.cicada tone.glass
cicada explain main.cicada lead.level
cicada play main.cicada
cicada render main.cicada -o library.wav --rate 48000 --bits 24
```

`cicada.sum` is generated and tool-owned. Commit it with the score. Each non-comment line contains `PATH KIND sha256:HASH`, where `KIND` is `std`, `project`, or `user`. Records sort by path and include transitive imports. The SHA-256 input starts with `cicada.library/1` and a zero byte, then the manifest, listed sources, and every regular file under `audio/`, sorted together by relative path. Each path and its exact bytes are preceded by their unsigned 64-bit big-endian lengths. Absolute roots and timestamps are excluded.

Every load recomputes the hashes. Missing pins, changed content, and changed resolution kinds produce `CICADA-LIB-HASH` at the importing file, line, and column. `check` reports them; playback and rendering refuse them. After an intended edit, run `cicada lib update demo/tone` to update that pin, or omit the path to update every imported library and remove unused pins. The command prints the old and new location kinds and hashes. A targeted update leaves dependency pins unchanged; update those dependencies explicitly or update all imports. A loose score uses `cicada.sum` in its own directory. An update scan containing loose scores from different directories is refused with `CICADA-LIB-ROOT`; run from each score directory or add a shared `cicada.mod`.

The language server completes import paths and public qualified names and goes to definitions in library source. Unsaved library changes also trigger hash diagnostics. `explain` identifies the library behind a declaration or instrument value. Project-wide formatting and notation fixes keep imported library files untouched. Notation fixes, local rename, and parameter hover retain import context for loose and independent scores too. Notation fixes stop on changed library pins. Library vendoring, Save As with imports, bundle provenance, and `require` versions remain follow-up work.

## Tracks and patterns

A track chooses a sound source: the built-in `acid` or `drums` voice, a
declared mono instrument, or an authored drum kit. A pattern describes steps
for that source. The scene connects a pattern to a track by name.

The current grid uses sixteenth-note steps. A 16-step pattern fills one bar;
patterns may contain 1–64 steps and repeat while their scene plays. `.` is a
rest. `|` groups cells for your eyes and does not add a step.

The supported key scales are `minor`, `major`, `dorian`, `phrygian`,
`harmonic`, `pent`, `mixo`, and `blues`. Numbered pitches `1` through `7`
follow the selected scale. Letter pitches such as `c#3` name an absolute
pitch. Unnumbered letter pitches use the instrument's home octave, which
defaults to 2. MIDI note C4 is 60. An apostrophe raises a pitch an octave; a
comma lowers it. Leave a space between a letter pitch and a tie or next pitch:
write `c3 - e3`, not `c3-e3`.

Pattern settings go inside the braces before the steps. `swing` ranges from
50% to 75% and defaults to 50%; `gate` ranges from 10% to 100% and defaults
to 55%. `transpose` changes note patterns by −24 to +24 semitones. A pattern
without its own `seed` inherits the project seed. The project defaults are
130 BPM, A minor, and seed 0.

## Try the experimental guitar

Edition 2 can play the physical string and amp prototype with
`track lead guitar { experimental = on }`. It is a research voice without
listening acceptance. Use it for auditions; DSP checks do not establish timbre.

The [guitar riff](../../examples/expressive-guitar.cicada) demonstrates bends,
slides, palm mutes and clean versus driven amp settings. Render it with
`cicada render examples/expressive-guitar.cicada -o guitar.wav`, or open the
score in Studio. Native playback, the AudioWorklet and offline rendering all
run the model. `~` slides into the next pitch; a new note replucks the string.
Scene settings such as `lead.bend = 2` and `lead.damping = 0.8` change the
sounding string. Bend is in semitones and vibrato is in cents, both written
without a suffix. All six continuous controls smooth over 8 ms.

See [controls and limits](../spec/edition-2.md#experimental-guitar-voice).
Drive zero still has amp coloration. This voice models one string and a
generic amp; it does not emulate a measured guitar, circuit or cabinet.

## Shape a note

Keep modifiers next to the note they change:

- `^` adds an accent.
- `~` slides into the next playable note.
- `*2` through `*8` retriggers the note that many times in one step.
- `?70` gives the note a 70% chance on each seeded pass.
- `-` ties the preceding note; `.` is a rest.

A note without `?` has a 100% chance. The legacy `%70` spelling is still
accepted; `cicada fix` changes it to `?70`.

Chance uses the pattern seed, track index, compiled pattern slot, pass number,
and step index. The same score therefore makes the same decisions in playback
and WAV export, independent of audio block size. All lanes of a drum pattern
use its compiled slot; a lane number does not replace the slot in the chance
hash. Changing a pattern's slot can change its seeded decisions.

You can reuse a phrase and transpose a use:

```cicada
cicada 2
track bass acid {}
phrase hook { 1^ . 1~ 5 }

pattern verse {
  use hook
  use hook +7
}

scene main { bass = verse }
song { main*8 }
```

Phrase uses expand before scheduling. Each use can repeat 1–64 times and
transpose by up to 24 semitones; the expanded pattern still has to fit the
64-step limit.

## Generate a starting phrase

`cicada gen` makes a deterministic acid score from a seed, key, and density
settings. Save the generated source and optional trace to separate files:

```sh
cicada gen --seed 4242 --key a --scale minor --trace -o generated.cicada > generated.trace.json
cicada check generated.cicada
```

Use a new output filename. The generator defaults to a 16-step phrase, a C
minor key, and the AABA structure. The full option ranges and defaults are in
the [generator reference](../spec/edition-1.md#phrase-generator-command).

## Write drum rows

Each drum row names one of the built-in lanes. `x` is a hit at velocity 100,
`X` is an accented hit at velocity 127, and `x1`–`x9` select stepped
velocities. `.` leaves a cell silent. Rows in one pattern must have equal
length; any lane you leave out is silent.

The built-in lanes are `bd`, `sd`, `ch`, `oh`, `cp`, `rs`, `lt`, `mt`, `ht`,
`cb`, and `cy`. An authored kit can route a lane to a custom instrument or to
its built-in recipe. See the [kit and lane reference](../spec/edition-1.md#authored-kits).

## Define an instrument

Use an instrument when you want a sound made from Cicada's typed synthesis
primitives. A `voice mono` block has ordered `let` bindings and one audio
output. Parameters and inputs carry types and units, so a frequency cannot
silently be used where a time value is expected.

```cicada
cicada 2
instrument glassbass {
  octave = 2
  param cutoff = 720Hz
  param bite = 0.65
  voice mono {
    let osc = saw(pitch)
    let shape = env(gate, 380ms)
    out = ladder(osc, cutoff, bite) * shape
  }
}

track lead glassbass { cutoff = 900Hz }
pattern lead-a { 5 . 3 . 1 . 3 . }
scene main { lead = lead-a }
song { main*4 }
```

The current profile supports mono graphs with at most 128 nodes and 32
stateful nodes. Polyphony, plugins, and sample assets are not in edition 1.
The [instrument reference](../spec/edition-1.md#instruments-and-voices) lists
every primitive signature.

## Make an arrangement

Scenes give named patterns to any tracks they mention. A missing assignment
keeps that track's current pattern. `off` stops a track; `keep` is an explicit
no-change action. A song plays scenes in order:

```cicada
cicada 2
track bass acid {}
track drums drums {}
pattern bassline acid { 1 . 3 . }
pattern beat drums { bd: X... }

scene verse { bass = bassline drums = beat }
scene break { bass = bassline drums = off }
song { verse*8 break*2 verse*8 }
```

Each song entry defaults to one bar and can last 1–999 bars. In Studio you can
launch a scene or pattern live and change the song lane without leaving the
source; the [Studio chapter](studio.md) explains those controls.

## Keep score files clear

Use `cicada fmt -w main.cicada` to write the canonical formatting. `cicada
fix main.cicada --check` reports whether legacy source spellings need
rewriting, even when the manifest already selects edition 2. `cicada fix
main.cicada` applies a meaning-preserving rewrite. If other edition-1 scores
share the folder, `fix` lists them and refuses the single-file change; run
`cicada fix --all` from the project folder to migrate them together. Both
commands check that the score parses and compiles. The specification lists every
[edition-1 spelling migration](../spec/semantic-model.md#cicada-fix-and-migrations).

The public [`examples/`](../../examples/) directory has 55 scores, from
first-acid grooves to custom instruments and effect routing. Use them as
complete, runnable starting points.

## Audio assets and samplers

Use edition 2 in `cicada.mod` to describe audio files, clip regions and a sampler.
Generate a complete example and its 9,644-byte mono PCM16 WAV from the repository root:

```sh
GOWORK=off go test ./testdata/audio-assets -run TestGenerateExample -args -out /tmp/cicada-audio-example
cicada check /tmp/cicada-audio-example/main.cicada
cicada fmt /tmp/cicada-audio-example/main.cicada
```

The Go test helper writes `cicada.mod`, `main.cicada`, and `audio/example.wav`.
The generated score contains the exact hash of the generated bytes. Its core declarations are:

```cicada
cicada 2
asset vocal "audio/example.wav" {
  sha256 = "528059ad119b79f269c9580505858c14d3636f77ade0e908942653e4c708f6ee"
  format = wav
  frames = 4800
  rate = 48000Hz
  channels = 1
  source = generated
}
clip vocal-a vocal { start = 10ms end = 4800frames gain = -2dB fade_in = 1ms fade_out = 2ms }
sampler vocal-hit { asset = vocal root = c3 mode = oneshot voices = 8 }
track chops vocal-hit { level = -8dB }
track vox audio { level = -6dB }
pattern hits { c3 . c3 . }
scene verse { chops = hits vox = vocal-a }
song { verse*4 }
```

`vocal-a` trims the first 10ms from a 100ms recording. Its end is the exclusive
source frame 4800. The `vox` audio track binds that clip in `verse`; the `chops`
track plays notes through `vocal-hit`. The sampler root `c3` is MIDI note 48,
with eight voices and one-shot mode. Use `mode = loop` for a whole-asset loop.

`check` hashes every asset and checks the WAV header against the declared rate,
channels and frame count. Asset paths resolve from the directory containing the
nearest `cicada.mod`, including scores in subdirectories. Exact `frames` literals
support sample editing; seconds and milliseconds must land on exact source frames.

These declarations compile to project data. Playback and recording support arrive
in the other Phase 1 lanes; the current engine reports `CICADA-UNSUPPORTED` for
projects containing audio data.
