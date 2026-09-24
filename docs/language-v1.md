# Cicada 1 language reference

A `.cicada` file is a text score. The first declaration is `cicada 1`.
Declarations can be separated by spaces or newlines; braces delimit bodies.
`//` starts a comment. The [Grammargen grammar](../language/cicada.grammar)
defines the syntax, and `cicada validate` checks names, units, ranges, and
render support.

## Score and arrangement

```cicada
cicada 1
title "Night circuit"
tempo 138
key a minor
seed 4242

track bass acid { cutoff = 620hz reso = 0.62 }
pattern bass-a acid steps=4 swing=54 gate=55 { 1^ . 1~ 5 }
scene main { bass=bass-a }
song { main*16 }
```

`title` is optional. Tempo is 20–300 BPM with at most three decimal places;
the default is 130. The default key is `a minor`. The project seed is an
integer from 0 to 4,294,967,295. Names use lowercase ASCII letters, digits,
underscores, and hyphens, beginning with a letter or underscore.

A track chooses the built-in `acid` or `drums` voice, or a declared
instrument. An acid track plays `acid` or `notes` patterns. A custom
instrument plays `notes` patterns. A drum track plays `drums` patterns.
Track parameters are checked for the selected voice. `level` (dB or `off`)
and `pan` (-1 to 1) control the dry stereo mixer.

A scene assigns patterns to tracks. `off` stops a track, and `keep`
retains its previous pattern. A song lists scenes in order; `main*16`
repeats a scene for 16 bars. Each entry can last 1–999 bars.

## Notes and patterns

`acid` and `notes` patterns have 1–64 cells. `.` is a rest, `-` ties
the prior note, and `|` separates groups visually without taking a step.
A pitch is a scale degree `1`–`7` or a letter note such as `c#3`.
`'` raises a pitch one octave; `,` lowers it one octave. C4 is MIDI note
60. Notes without an explicit octave use octave 2.

Note modifiers are `^` for accent, `~` for slide, `*2`–`*8` for
ratchet, and `%1`–`%99` for hit probability. A plain note has probability
100. Normalized source writes modifiers in accent, slide, ratchet,
probability order. A slide needs an adjacent sounding target, including one
supplied by the next pattern iteration or a scene switch.

Pattern attributes include `steps`, `swing`, `gate`, `transpose`,
and `seed`. `steps` must equal the expanded cell count. Swing ranges
from 50 to 75 percent; gate from 10 to 100 percent; transpose from -24 to
24 semitones for note patterns. Drum patterns cannot transpose lanes.
Defaults are 50, 55, and 0 respectively. A pattern without
its own seed inherits the project seed.

The supported scales are `minor`, `major`, `dorian`, `phrygian`,
`harmonic`, `pent`, `mixo`, and `blues`. `pent` and `blues` use
degrees `1 3 4 5 7`; degrees `2` and `6` are errors. In `blues`,
`4#` gives the blue note.

A named phrase reuses acid steps before scheduling:

```cicada
phrase hook acid { 1^ . 1~ 5 }
pattern bass-b acid steps=8 {
  use hook
  use hook transpose=12
}
```

`use hook*2` repeats a phrase twice. Expansion must fit within 64 cells.
The source retains the phrase use and pitch spelling; the semantic project
stores the expanded notes.

## Drums

A drum pattern has one semicolon-terminated row per lane. `x` is a hit
at velocity 100, `X` is an accented hit at velocity 127, and `x1`
through `x9` choose stepped velocities. Hits can use ratchet and
probability modifiers. Omitted rows are rests.

```cicada
track kit drums { bd_tune = 55hz }
pattern beat drums steps=8 {
  bd: X...x...;
  sd: ....X...;
  ch: x.x.x.x.;
}
```

The rendered M0 lanes are `bd`, `sd`, `ch`, `oh`, `cp`, and `rs`.
The remaining lane names `lt`, `mt`, `ht`, `cb`, and `cy` are
reserved for a later milestone and produce an unsupported diagnostic when
used.

## Code-defined instruments

An instrument declares typed parameters and one `voice mono` block.
Ordered `let` bindings can use parameters, earlier bindings, and built-in
inputs `pitch`, `gate`, `velocity`, and `sample_rate`. The `out`
expression must produce audio.

```cicada
instrument glassbass {
  param cutoff: hz = 720hz;
  param bite: unit = 0.65;
  voice mono {
    let osc = saw(pitch);
    let shape = env(gate, 380ms);
    out = ladder(osc, cutoff, bite) * shape;
  }
}
track lead glassbass { cutoff = 900hz }
pattern lead-a notes steps=4 { 5 . 3 . }
```

Parameter units are `unit`, `hz`, `ms`, and `db`. The expression
type checker also tracks `gate` and `audio`. The current graph primitives
are `saw`, `square`, `sine`, `noise`, `env`, `ladder`, `diode`,
`lowpass`, `highpass`, `mix`, `tanh`, `exp2`, and `clamp`.
Graphs are acyclic, with at most 128 nodes and 32 stateful nodes. They
compile before playback and run without allocation in the audio callback.
`voice poly`, arbitrary DSP loops, sample assets, and plugins are outside
the current profile.

## Tools and interchange

```sh
cicada validate score.cicada
cicada fmt --check score.cicada
cicada convert score.cicada -o score.cicada.json
cicada convert score.cicada.json -o normalized.cicada
cicada compare --semantic score.cicada normalized.cicada
cicada render score.cicada -o score.wav --rate 48000 --bits 24
```

Source is the authoring form. Canonical JSON is the typed semantic
interchange form; conversion back to source must preserve semantic meaning.
Canonical JSON and JSON input are limited to 2 MiB; source validation reports
`CICADA-LIMIT` when its canonical project would exceed that bound.
An unedited CST can print the original bytes, including comments and pitch
spelling. See [first-acid.cicada](../examples/first-acid.cicada) for a full
score and the [live engine contract](live-engine.md) for playback boundaries.
