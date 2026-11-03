# Modulation

Modulation moves a track parameter using a control source. A source can be a
periodic LFO, a smooth random sway, a drawn contour, or an envelope that follows
another track. The score stores both the source and its routes, so playback,
seek, and offline rendering use the same declarations.

```cicada
lfo drift { wave = triangle rate = 8bars }
sway wander { rate = 2bars octaves = 2 }
envelope drums-level { source = beat attack = 5ms release = 120ms }
contour swell { period = 4bars @0 -1 @1/4 1 smooth @1 -1 }

mod drift -> lead.pan depth = 0.5
mod wander -> lead.level depth = 2dB
mod swell -> lead.cutoff depth = 500Hz
mod drums-level -> lead.level depth = -4dB
```

The score must declare the `lead` and `beat` tracks. The complete example is
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

The current route targets are track level, pan, sends A and B, and the acid
cutoff, resonance, envelope amount, decay, and accent controls. Legacy delay
and reverb settings cannot be routed. A routed track's level and pan use a
table-based calculation and can differ slightly from an unrouted track even
at zero depth. Registered-parameter routes are control-rate, not audio-rate.

Sources default to `loop = wrap`, which restarts them at the song loop. Set
`loop = continue` to fit whole cycles to the loop; Cicada warns if that moves
the authored rate by more than 10%. The envelope follower resets at load,
seek, and loop wrap.

## Studio and inspection

Open the **Automation** detail dock. **Modulation** lists sources and routes;
add a source, then choose a target, mode, and depth. Save a depth or remove a
route from its row. These edits validate before saving and support Undo. Edit
source settings and contour points in Code. A rejected edit leaves the saved
score intact.

`cicada explain score.cicada lead.cutoff @1` lists matching modulation routes
in order and labels the computed value before modulation. The actual playback
value also depends on the source signals, live state, and smoothing.

Studio plays these scores through its native audio service. The optional
AudioWorklet host and WAM export need `make build-modular-wasm`; a score that
also uses modeled keyboard voices needs `make build-keys-modular-wasm`. The
existing core and keyboard modules retain their own size budgets. The public
browser demo currently uses the core module and cannot play modulated scores.
