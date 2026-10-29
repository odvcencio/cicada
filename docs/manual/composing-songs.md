# Composing a complete song

A useful song example needs a musical idea, contrast, a return, and an ending.
Build those from a few reusable parts before adding more instruments. The
[demo songs](../../examples/songs/README.md) provide three different starting
points, including full render commands and focused editing exercises.

## Give each track a job

Start with rhythm, a low part, harmony, and a recognizable lead. They do not all
need to play at once. A sparse opening makes the later entrance of the groove
audible without changing the master volume.

[Neon Causeway](../../examples/songs/neon-causeway.cicada) uses an acid bass,
drum kit, and PM bell. [Lanterns After Rain](../../examples/songs/lanterns-after-rain.cicada)
adds extended keyboard chords. [Tidal Glass](../../examples/songs/tidal-glass.cicada)
uses two copies of one pluck instrument at different levels and pan positions.

Use the existing sound controls first. When you need an authored instrument,
scale its output to leave room for the other tracks and chords. Increasing a
voice count changes capacity; it is not a substitute for balancing the mix.

## Match pattern length to the musical phrase

The default cell is a sixteenth note. In 4/4, sixteen cells fill one bar.
`step = 1/8` makes eight cells one bar; `step = 1/4` makes four. Eighth-note
triplets, `step = 1/8t`, use twelve cells per bar. Patterns have at most 64
expanded cells, so a quarter-note grid can hold sixteen bars.

Different track grids can coexist. Lanterns has a two-bar drum groove and
four-bar chord, bass, and melody patterns. Its section lengths are multiples
of four bars, keeping their phrase boundaries together.

For chords, use a polyphonic graph instrument or a supported keyboard voice,
and declare the pattern as `notes`. Brackets on an acid pattern subdivide a
cell into a tuplet instead. This complete example plays a four-bar phrase:

```cicada
cicada 2
tempo 104
key d minor
track keys tine_ep { voices = 4 level = -6dB }
pattern harmony notes {
  step = 1/4
  gate = 85%
  [d3 f3 a3 e4] - - .
  [g3 bb3 d4 f4] - - .
  [bb3 d4 f4 a4] - - .
  [a2 c#3 e3 g3] - - .
  velocity: 84 . . . 88 . . . 82 . . . 96 . . .
}
scene main { keys = harmony }
song { main*4 }
```

Letter pitches make the chord voicing explicit. Scale degrees are convenient
for phrases that should retune with the score's key. The A7 above deliberately
uses C# as a leading note back to D; changing `key` does not retune that letter
pitch. A velocity-row dot holds its preceding value, and the row must begin
with a number. A tie holds the current chord's pitches together.

## Choose scenes or named placements

Use `song` when the form is a sequence of repeatable scenes. Start from an
opening, verse, fuller section, breakdown, return, and closing. Inspect the
arrangement's bar ranges in Studio's **Session** panel.

A scene binding selects a pattern. An omitted binding or `keep` carries the
previous selection; `off` releases it. Scene settings also carry forward.
When a scene should work as a standalone live launch, explicitly assign or
silence its tracks rather than depending on what happened before it.

Scene patterns follow the shared transport phase. A section entrance halfway
through a four-bar phrase need not restart that phrase. Align section lengths
with phrase lengths when you want their beginnings together.

Use edition-2 `arrange` when individual parts need their own starts and lengths.
Named placements restart a pattern at their own origin, and markers label the
form. Tidal Glass places its answering voice one beat after a section begins.
The placement's exclusive end is the first position outside its region.
Patterns on one track must not overlap; use another track for a simultaneous
voice. Choose one arrangement authority per score: `song` or `arrange`.
See [named placements](../spec/arrangement.md) for exact timing rules.

In Studio, select a placement in **Session** to edit its track, content,
start, or length. Drag blocks to move them and their right edges to resize;
use the time fields for an exact beat or triplet duration. Copy, add, remove,
and marker controls share the same revision checks and Undo as score edits.

Use **Loop section** to hear the transition and groove while arranging. Open
a placement and choose **Audition solo** to check its phrase, or **Audition in
song** to hear how it fits with overlapping parts. **Loop solo** and **Loop in
song** repeat the same range while you work. Moves, trims, and Undo follow at
the next bar or loop restart. **Loop an exact range** is useful for a pickup
or short transition. **Play full song** returns to the complete arrangement.
See [Session listening](studio.md#transport-and-session) for the controls.

## Develop the sound over time

Change one dimension at a time: note density, register, harmony, drum detail,
or filter brightness. Repeat a hook often enough to recognize it, then change
its answer or accompaniment. A breakdown can remove the rhythmic anchor while
keeping the hook, making its return sound larger.

Scene parameter settings make discrete changes with the engine's smoothing.
For a planned song sweep, use an `automate` lane. Positions are one-based
`@bar.beat.sixteenth`, with beats and sixteenths each from 1 to 4. The shape on
a point describes the segment arriving at that point:

```cicada
cicada 2
fx warm drive { tone = 2kHz }
track bass fm_bass { insert = warm }
pattern line { 1 . 1 5 }
scene main { bass = line }
automate warm.tone {
  @1.1.1 2kHz
  @5.1.1 8kHz smooth
}
song { main*4 }
```

Before its first point a lane contributes no value. Afterward it overrides
scene settings on that path and holds its last value. Frequency and time
parameters interpolate in logarithmic control space; this sweep's halfway
target is 4 kHz. `cicada explain score.cicada warm.tone @3` shows the
automation contribution and final target, alongside any scene setting.
The target is a prepared control value; smoothing can make the instantaneous
audio parameter lag it. Continuous lanes currently use `song` time.

## Finish and export

Write an ending into the form: remove parts, hold a final harmony, or fade
supported track levels. Master level is static; the demos use track-level
lanes for fades. Post-fader sends follow those levels, while existing reverb
and delay tails decay. Pre-fader sends keep feeding an effect through a fader
fade, which can be useful but produces a different ending.

Check the score and formatting, then render the whole arrangement before
judging one isolated loop:

```sh
cicada check main.cicada
cicada fmt --check main.cicada
cicada render main.cicada -o song.wav --bits 24 --loudness -16
```

Leaving out `--bars` renders through the end. The default three-second tail
adds decay after the musical form. Loudness targeting adjusts delivery level;
it does not repair a crowded arrangement. Compare the intro, densest section,
transition into the breakdown, return, and final tail. Inspect stems when it
is unclear which track is masking another; see [Exporting](exporting.md).

Use Save As before personal experiments. Small edits such as changing the
answer, moving a placement, or changing one velocity row make clearer
comparisons than replacing the entire score at once.
