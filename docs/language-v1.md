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
and `pan` (-1 to 1) control the stereo mixer. A track can set `insert = drive`
when the score declares `fx drive`:

```cicada
fx drive { shape = hard gain = 18db tone = 12khz mix = 0.75 }
track bass acid { insert = drive }
```

Drive shapes are `soft`, `hard`, `fold`, and `diode`. Gain is 0–36 dB, tone
is 1–20 kHz, and mix is 0–1. Unset fields default to soft, 0 dB, 12 kHz,
and fully wet. The insert runs before track level and pan.

The score can also declare `fx delay` and route a track with `send_a = 0.4`.
`send_pre = true` taps after the insert and before level/pan; the default
post-fader send follows level and pan. Delay `time` accepts 1–2000 ms or
`1/32`, `1/16`, `1/16T`, `1/16.`, `1/8`, `1/8T`, `1/8.`, `3/16`, `1/4`,
`1/4.`, and `1/2`. Feedback is 0–0.95, damp 1–16 kHz, pingpong is
`true` or `false`, and width and mix are 0–1. Defaults are `1/8`, feedback
0.35, damp 6 kHz, pingpong false, width 1, and fully wet. A synced division
must fit the four-second delay buffer at the current tempo; for example,
`1/2` is rejected below 30 BPM.

`fx reverb` routes through `send_b = 0.4`; `send_pre` selects pre-fader input for both sends. Reverb
accepts `size` 0.5–1.5, `decay` 0.3–12 s, `damp` 2–16 kHz, `highpass`
40–400 Hz, `predelay` 0–200 ms, and `mix` 0–1. Defaults are size 1,
decay 2.4 s, damp 8 kHz, highpass 120 Hz, zero predelay, and fully wet.
Compressor and authored effect graphs are not implemented yet.

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

All eleven built-in lanes render: `bd`, `sd`, `ch`, `oh`, `cp`, `rs`,
`lt`, `mt`, `ht`, `cb`, and `cy`. The [drum kit example](../examples/drums-kit.cicada)
plays each lane.

An authored kit uses `kit steel { bd=kick; ch=builtin.ch; }` and
`track drums steel {}`. Each lane binds one declared mono instrument or one
`builtin.<lane>` recipe. Omitted lanes stay silent. A graph instrument receives
the lane's fixed MIDI trigger pitch and hit velocity. The binding is retained
in the typed project and plays in native, offline, and WASM renderers. See the
[authored kit example](../examples/authored-kit.cicada).
An authored graph should shape its one-shot decay with `env`; its gate remains
high after a hit until a choke or scene stop. A retrigger restarts the graph
with a 1 ms fade of the prior state.

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
JSON conversion reports stable diagnostic codes, with a `#` JSON Pointer for
located fields (for example, `score.json#/patterns/0/data/3/velocity`).
Source positions use one-based Unicode scalar columns. The v1 diagnostic codes
are `CICADA-SYNTAX`, `CICADA-VERSION`, `CICADA-KEY`,
`CICADA-SCALE-DEGREE`, `CICADA-SEED`, `CICADA-PARAM`, `CICADA-UNIT`,
`CICADA-REFERENCE`, `CICADA-DUPLICATE`, `CICADA-EXPANSION`, `CICADA-USE`,
`CICADA-UNSUPPORTED`, `CICADA-LIMIT`, `CICADA-SLIDE-REST`, `CICADA-IO`,
and `CICADA-AUDIO`.
An unedited CST can print the original bytes, including comments and pitch
spelling. See [first-acid.cicada](../examples/first-acid.cicada) for a full
score and the [live engine contract](live-engine.md) for playback boundaries.
