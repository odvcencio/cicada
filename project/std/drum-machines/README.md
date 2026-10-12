# Classic drum voices

`std/drum-machines` provides two original, editable kits, `eight-oh-eight` and
`nine-oh-nine`. Import the library and pin it with `cicada lib update`:

```cicada
import "std/drum-machines"
track rhythm drum-machines.eight-oh-eight { level = -6dB }
```

The 808-inspired kit has a rounded swept-sine kick, two shell resonances and
filtered noise for the snare, a six-oscillator metallic hat/cymbal source,
four staggered clap bursts, lightly swept toms, rimshot, and cowbell. Its pulse
oscillators use Cicada's harmonic-limited, oversampled graph path.

The 909-inspired kit has a faster, more driven kick, a brighter snare and
clap, deeper tom pitch drops, short bright hats, a ride, and a crash. Its
`cb` lane plays the ride; `cy` plays the crash. The other nine lane names
keep their usual meanings. Closed hats choke open hats, including graph
voices, and repeated hits use the engine's short retrigger fades.

These are sound-design starting points, not measured hardware replicas.
In particular, the original TR-909 uses recorded hats and cymbals; this first
kit synthesizes those sounds and does not include or recreate Roland's sample
ROM. [Roland's TR-909 history](https://www.roland.com/uk/promos/roland_tr-909/)
describes the original hybrid design. The
[TR-808 history](https://www.roland.com/uk/promos/roland_tr-808/) describes its
analog origins. Hardware comparison recordings are needed before making a
replica-fidelity claim.

Listen to [Classic Drum Study](../../../examples/drum-machines/main.cicada)
for isolated hits followed by the same groove on each kit. In Session, loop
the `eight-groove` or `nine-groove` section, or audition a placement solo. The
example uses equal track levels and no effects so the voices remain exposed.

The private `_808-*` and `_909-*` graphs are plain Cicada source. Copy a voice
into your score to edit its frequency, envelope, filter, or drive. Graph kit
lanes currently have fixed prepared synthesis settings; they do not expose
the built-in kit's `bd_tune` or `sd_snappy` controls. This also makes the need
for per-piece controls and imported sample-backed kit lanes explicit.

When shaping a body or noise tail, `env(gate, 400ms)` reaches approximately
1% amplitude (-40 dB) after 400 ms. The built-in kit's `bd_decay`, `oh_decay`,
and similar controls instead specify an exponential time constant: the
amplitude is about 37% (-8.7 dB) after that time. Copying the same number between
those controls produces a much shorter graph envelope. Multiply the built-in
time by approximately 4.6 to match its decay slope in an authored `env`.
Built-in voices retain their full decay, including a snare's independently
set wire tail; explicit hat choking still applies a short fade.
