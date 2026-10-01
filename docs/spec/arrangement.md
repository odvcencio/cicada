# Named placements use the shared schedule

Edition 2 accepts one arrangement authority: `song` or `arrange`. Scene
definitions remain available for live launches. Existing songs retain their
scene boundaries, global pattern phase, slide behavior, and parameter changes.

```cicada
cicada 2
track bass acid {}
pattern pulse { c3 . c4 . }
arrange {
  place bass-1 bass pulse { at = @2.3.4 length = 1920ticks }
  place bass-2 bass pulse { at = @4.1.1 length = 2bars }
  marker chorus { at = @4.1.1 }
}
```

Placement names identify instances; content names identify reusable patterns
or clips. Positions use a one-based `@bar.beat.step` grid in 4/4. Durations
accept bars, ticks, or exact note divisions. A quarter note has 960 ticks;
unsupported fractional ticks fail validation. Step patterns start on a step
and restart at the placement's origin. Audio placements may overlap. Step
patterns on the same track must have disjoint intervals.

Project JSON preserves integer `at_tick` and `length_ticks` values under
`arrange`. `notation.SetPlacementField` changes one time literal while keeping
all other source bytes. A move followed by its inverse restores the exact
original source, including comments and authored units.

Compilation produces immutable start/release events in absolute ticks. Releases
precede starts at the same tick; placement identities prevent an old release
from stopping another instance. Preparation validates voice capacity and copies
event data outside rendering. Seeking reconstructs active placements and clip
source positions without rendering skipped frames.

Native playback, WASM images, WAV, and stems consume this schedule. WAV and
stems retain the existing whole-bar export convention: a partial final bar is
included, followed by the requested effect tail. Placement boundaries remain
exact within that span. Export removes engine output latency.

Kernel image version 14 carries placements and prepared planar audio. Legacy
projects still encode as version 13, and versions 8–13 remain readable. Hosts
verify and decode assets before rendering; the current resident loader and
2 MiB image limit remain in force. Streamed page ownership and admission
profiles belong to the other Phase 2 lanes.
