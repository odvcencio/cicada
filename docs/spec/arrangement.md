# Named placements use the shared schedule

Edition 2 accepts one arrangement authority: `song` or `arrange`. Scene
definitions remain available for live launches. Existing songs retain their
scene boundaries, global pattern phase, slide behavior, and parameter changes.

```cicada
cicada 2
track bass fm_bass {}
pattern pulse { c3 . c4 . }
arrange {
  tempo @1.1.1 120
  tempo @3.1.1 96
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

Tempo steps use `tempo @bar.1.1 BPM` inside `arrange`. They must increase by
bar, remain within the placements, and use a number from 20 to 300 BPM. Values
round to the nearest thousandth. The header tempo applies until the first
step; each step then holds until the next. A step may repeat a value to restore
it after a live tempo adjustment. Steps do not extend the arrangement's length.
Project JSON stores them as optional `arrange.tempos` entries with zero-based
integer `bar` and `bpm_milli` fields. Native and portable playback, seeking,
looping, WAV timing, stems and MIDI tempo events share those steps. Studio
shows the resulting duration and tempo list. Scene definitions stay available
for live launches; their tempo settings do not govern the placement timeline.

Session recordings retain a tempo snapshot with the take and its comp metadata.
Selecting a vocal from a scene song carries its tempo steps into the arrangement
alongside supported numeric controls. Raw PCM remains unchanged. Phrase timing,
waveform geometry and count-in use the recording's saved map; selecting against
a different current map is refused so the recording is not silently retimed.

Studio's Session timeline moves and resizes placements and offers exact time,
track, and content fields, plus copy/add/remove and marker controls. These
commands patch CST fields atomically, compile the complete project, check the
source revision, and enter the shared Undo history. Rejected overlaps and
inexact divisions never publish a partial edit. See [Studio](../manual/studio.md#transport-and-session).

Compilation produces immutable tempo and start/release events in absolute ticks.
Tempo steps precede placement events at the same tick. Releases
precede starts at the same tick; placement identities prevent an old release
from stopping another instance. When another pattern placement starts on the
same track at the release tick, including the wrap of a looping arrangement
from the last end to a placement at tick 0, the release is skipped. Held notes,
slides, and drum or voice tails carry across the boundary like a scene change.
Step patterns, including chance steps, still restart at each placement's
origin. A release followed by a gap still stops the track's notes and tails.
Preparation validates voice capacity and copies
event data outside rendering. Seeking reconstructs active placements and clip
source positions without rendering skipped frames.

Native playback, WASM images, WAV, stems, and MIDI note export consume this
schedule. WAV, stems, and MIDI retain the existing whole-bar export convention:
a partial final bar is included. Placement boundaries remain exact within that
span. Audio export removes engine output latency and adds
the requested effect tail. MIDI bounds note-offs at each placement end and
preserves arrangement markers as MIDI marker events.

Unified kernel image version 15 carries placements, prepared planar audio and
explicit graph chord fields. The two incompatible development-v14 dialects are
rejected; recompile their project source. See [the exact v15 contract](kernel-image-v15.md). Legacy
projects still encode as version 13, and versions 8–13 remain readable. Hosts
verify and decode assets before rendering; the current resident loader and
2 MiB image limit remain in force. Streamed readers use a separate host preparation path.
