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
