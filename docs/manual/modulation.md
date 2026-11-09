# Modulation

Modulation moves a track parameter using a control source. A source can be a
periodic LFO, a smooth random sway, a drawn contour, or an envelope that follows
another track. The score stores both the source and its routes, so playback,
seek, and offline rendering use the same declarations.

```cicada
cicada 2
tempo 118
track lead acid { level = -8dB }
track beat drums { level = -6dB }
pattern line acid { 1 . 3 . 5 . 3 . }
pattern pulse drums { bd: x... x... x... x... }

lfo drift { wave = triangle rate = 8bars }
sway wander { rate = 2bars octaves = 2 }
envelope drums-level { source = beat attack = 5ms release = 120ms }
contour swell { period = 4bars @0 -1 @1/4 1 smooth @1 -1 }

mod drift -> lead.pan depth = 0.5
mod wander -> lead.level depth = 2dB
mod swell -> lead.cutoff depth = 500Hz
mod drums-level -> lead.level depth = -4dB

scene groove { lead = line beat = pulse }
song { groove*8 }
```

For more sources and routes, see
[`examples/detail/modulators.cicada`](../../examples/detail/modulators.cicada).

LFO waves are `sine`, `triangle`, `square`, and `sample-hold`. LFO and sway
rates accept note lengths such as `1/4`, bar counts such as `8bars`, or
frequencies such as `0.25Hz`. A contour's period uses a note length or bar
count; its positions run from `@0` to `@1`, and its point shapes are `step`,
`linear`, `smooth`, and `exponential`. Sway accepts one to four octaves.
An envelope follows the named track after its fader; following a track and
modulating that same track is rejected to avoid a feedback cycle.

An omitted route mode means `add`: depth has the target's unit, as in `2dB`
or `500Hz`. A `multiply` route uses a plain factor instead. Routes to the same
target apply in declaration order, after the saved base and automation, then
the engine clamps and smooths the result. A source that moves a target faster
than 20 Hz at the song's fastest tempo is refused. A score supports at most 32
sources and 64 routes.

The current route targets are track level, pan, sends A and B, the acid
cutoff, resonance, envelope amount, decay, and accent controls, and the first
eight parameters an authored instrument declares, such as `bass.cutoff` (see
[live instrument parameters](#live-instrument-parameters)). Legacy delay
and reverb settings cannot be routed. A routed track's level and pan use a
table-based calculation and can differ slightly from an unrouted track even
at zero depth. Registered-parameter routes are control-rate, not audio-rate.

Sources default to `loop = wrap`, which restarts them at the song loop. Set
`loop = continue` to fit whole cycles to the loop; Cicada warns if that moves
the authored rate by more than 10%. The envelope follower resets at load,
seek, and loop wrap.

## Sources in authored instruments

An authored instrument can read a named source with `mod(name)`. It returns a
unitless value held between the engine's 128-frame control-grid updates. All
voices read the same song-positioned source, including after a seek:

```cicada
cicada 2
lfo breath { wave = triangle rate = 2bars }
instrument lead {
  voice mono { out = sine(pitch) * (0.5 + 0.4 * mod(breath)) }
}
track voice lead {}
pattern line { c4 . e4 . }
scene verse { voice = line }
song { verse }
```

The source must be declared in the project. Graph modulation inside authored
drum-kit lanes is not supported.
For a playable example, see
[`examples/detail/instrument-modulation.cicada`](../../examples/detail/instrument-modulation.cicada).

For a per-sample oscillator local to an authored instrument, use
`lfo(rate, wave)`. The rate is a fixed frequency from `0.01Hz` to `20Hz`;
the waveform is `sine`, `triangle`, `square`, or `sample-hold`. It follows
the song position by default, holding its phase while transport is stopped
and restarting at a song loop. Add `note` as the third argument to restart
the oscillator for each new note; a slide keeps its current phase.

```cicada
cicada 2
instrument lead {
  voice mono { out = sine(pitch) * (0.5 + 0.4 * lfo(2Hz, triangle, note)) }
}
track voice lead {}
pattern line { c4 . e4 . }
scene verse { voice = line }
song { verse }
```

Graph LFOs count toward the 32 stateful-node limit and use the optional
modular browser kernel. They are not supported inside authored drum-kit lanes.

## Live instrument parameters

An authored instrument's own parameters can move while it plays. A scene
setting, an automation lane, a modulation route or a macro mapping names the
parameter on the track that plays the instrument, including a track that plays
a preset of it:

```cicada
cicada 2
tempo 112
instrument pmbass {
  param cutoff = 420Hz
  voice mono { out = lowpass(saw(pitch), cutoff) * env(gate, 300ms) * 0.4 }
}
track bass pmbass { cutoff = 480Hz }
pattern line { c2 . . c2 . . eb2 . }
lfo lift { wave = triangle rate = 4bars }
mod lift -> bass.cutoff depth = 300Hz
scene verse { bass = line }
scene open { bass = line bass.cutoff = 1600Hz }
song { verse*4 open*4 }
automate bass.cutoff {
  @5.1.1 1600Hz
  @8.4.4 3000Hz exponential
}
```

The track's static setting (`cutoff = 480Hz`) is the authored value that a
scene restore or a seek returns to. A moved value glides over 20 ms. The
parameter keeps its declared unit, and its range comes from that unit:
`hz` 1–20,000 Hz on a log curve, `ms` 0–60,000 ms, `db` −60 to +24 dB,
and a unitless parameter −1,000 to 1,000; a default outside that range widens
it. Only the first eight parameters an instrument declares can move, in
declaration order; a later one is refused with the limit named. Cicada checks
the instrument at the lowest and highest values the scenes, automation, routes
and macros can give a parameter, as it checks a track's static setting, so a
delay time or comb feedback out of range is refused rather than clamped while
it plays. A seek or a scene restore also returns a routed parameter's base to
the authored value. MIDI controls cannot reach an authored parameter yet.

Native playback, `render` and `stems` move these parameters. The
portable browser and WAM images refuse a score that moves one. Reading a
modulation source inside the instrument with `mod(name)` still works there,
and in drum-kit graph lanes, where live parameters are not available.

## Studio and inspection

Open the **Automation** detail dock. **Modulation** lists sources and routes;
add a source, then choose a target, mode, and depth. Save a depth or remove a
route from its row. Expand a source to edit its settings or contour points, or
remove it after removing its routes. These edits validate before saving and
support Undo. Imported library sources remain read-only. A rejected edit
leaves the saved score intact.

`cicada explain score.cicada lead.cutoff @1` lists matching modulation routes
in order and labels the computed value before modulation. The actual playback
value also depends on the source signals, live state, and smoothing.

Studio plays these scores through its native audio service. The optional
AudioWorklet host and WAM export need `make build-modular-wasm`; a score that
also uses modeled keyboard voices needs `make build-keys-modular-wasm`. The
existing core and keyboard modules retain their own size budgets. The public
browser demo uses the modular module and includes the Drift sketch; keyboard
instruments and external assets remain unavailable there.
