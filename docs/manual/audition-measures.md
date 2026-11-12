# Audition measures: methods, limits and cost

This page completes [Audition](audition.md). It describes how `cicada
audition` measures the mix's loudness, band ownership, activity and peaks,
the fields `--detail` adds, a worked example, the limits of these measures
and their cost.

## Reading the tables

**Energy.** The loudness of the mix, measured with the meter the renderer
uses to check a loudness target (`kernel/loudness`, ITU-R BS.1770-4 with the
EBU R 128 gates). The first line gives the whole range: integrated loudness,
loudness range, true peak and the highest short-term loudness. Then one row
per section:

| Column | Meaning |
| --- | --- |
| `LUFS` | Integrated loudness over exactly the section's ticks, from a meter started at the section start. `n/a` when no 400 ms block passes the -70 LUFS gate. |
| `vs top` | LU under the loudest section. |
| `LRA` | Loudness range of the section. `n/a` under 3 s, where no short-term window fits. Under about a minute it rests on few windows: a short section that fades out reads a wide range. |
| `TP dBTP` | True peak of the section (4x oversampled at 48 kHz). |
| short-term per bar | One block per bar: the short-term loudness (3 s window) that ends at the bar's end, against the loudest bar of the range. `█` is within 3 LU of it, each step down is 3 LU more, `·` is 24 LU under or has no value (the first 3 s of the range have none). |

The last line names the loudest and the quietest section, with their bars
(a song can play a section twice under one name), and the LU between them. A
drop-out shows as a section many LU under the others and as low blocks in its
bars; a build shows as blocks that rise bar by bar. A drop-out shorter than
the 3 s window can still draw a high block, because the window holds the bars
before it: read the mix silences, or with `--detail` the bar's sample peak, for those. The audition renders
at the score's own levels, with no loudness target, so its LUFS are those of
the unnormalized mix; the differences between sections and bars are what to
read.

**Band owners.** For every bar and seven fixed bands, the track that holds the
largest share of the band's energy, its share in percent, and the band's level
in dB re a full-scale RMS (`kick 64* -38`). The bands, in Hz, hold the FFT
bins whose centre frequency f is in `low <= f < high`; energy under 20 Hz or
over 20 kHz is in no band, so a track's seven band levels can sum to a little
less than its level (noise effects read up to 0.4 dB less):

| Band | Edges |
| --- | --- |
| `sub` | 20-40 Hz |
| `low` | 40-80 Hz |
| `bass` | 80-160 Hz |
| `low_mid` | 160-400 Hz |
| `mid` | 400-1,600 Hz |
| `presence` | 1,600-5,000 Hz |
| `air` | 5,000-20,000 Hz |

The energy comes from 8,192-point Hann-windowed FFTs (5.86 Hz per bin at
48 kHz) of each stem, measured bar by bar: each bar's frames see only that
bar's samples, so no sound from a neighbouring bar counts in it, and the frames
are 2,048 samples (43 ms) apart, a quarter window, where the squared windows
sum to a constant. Every instant of a bar therefore weighs the same, whatever
the tempo and wherever a hit falls against the grid. (The per-sixteenth frames
of the 125 Hz-2 kHz band measure would weight a hit by where it falls, so the
band owners do not reuse them.) A share is
the track's energy over the sum of the analyzed tracks' energies in that band;
effect returns are not tracks and are left out. `*` marks a **collision**: two
tracks each hold more than 25% of the band, compared before the shares are
rounded (so a runner-up shown as 25.0 can collide). A band is `quiet`, with no
owner, when it is more than 30 dB under its own loudest bar, more than 30 dB
under the loudest band of the same bar, or more than 40 dB under the loudest
band of any bar. That keeps the window leakage of a loud band and the tail of a
neighbouring bar from making an owner when they are far under the bar's
content. It does not stop a hit from owning a band that holds nothing else:
a hit's attack and decay spread real energy over every band, and when no
other track plays there, the hit owns it. For example, a 440 Hz knock with
an instant attack and a 40 ms decay owns the 20-40 Hz band of its bar at
-71 dB, 26 dB under its own 400-1,600 Hz band. Read the level before acting
on an owner. Under the table, one row per section counts the
bars with a collision in each band (a bar belongs to the section that holds its
first tick), and one line per band lists the colliding bars and how often each
pair of tracks collides.

**Activity and repeats.** One row per track. The blocks show, bar by bar,
whether the track sounds and how loud it is against its own loudest bar: `█`
is within 3 dB, each step down is 3 dB more, and `·` means it does not sound:
its RMS in the bar is more than 30 dB under its loudest bar, or under -80
dBFS. For a track that plays notes, a bar where no note starts is measured
after its first 0.5 s, so the release tail of the bar before does not count;
a held note still does. Entrances, exits and drop-outs read straight off the
rows. The numbers before them count, from the notes the renderer played (so
chance and humanize are what sounded, unlike a MIDI export): `notes`, the bars
where the track starts a note; `=prev`, the bars whose notes equal the bar
before; `=4`, the bars whose notes equal the bar four before; and `run`, the
longest stretch of identical consecutive bars. Two bars are equal when every
note start has the same offset in the bar, the same pitches, the same velocity
(after humanize) and, where the renderer reported a release, the same length.

**Mix silences.** Stretches of at least 0.25 s where the mix stays under
-60 dBFS RMS in 10 ms blocks, with their start and end as `bar.beat.sixteenth`.
A drop to short hits lists the gaps between them.

**Peaks.** The highest true-peak moments of the mix, highest first, at least
50 ms apart, with their `bar.beat.sixteenth` and time, and what set each one.
At the mix's largest sample in the peak, on that channel, the report sums every
track stem and both effect returns: the signal entering the master chain. Each
part is the stem's own sample there as a signed percentage of that sum, so the
parts add up to 100%: `kick +72%, bass +31%, return-a -3%` means the bass
added to the kick's peak and the reverb return took a little away. A held bass
under a hit gets its real part of the peak, not more because it carries more
energy. Parts of at least 1% are listed, at most five, largest first; `rest`
is the remainder. The report finds the blocks of 10 ms with the highest sample
peaks, measures the true peak of the eight best per listed peak, and ranks
those. When a limiter or a loudness target falls short, these are the hits to
look at. The master chain can reshape the signal after these parts are read
(see [Limits](#limits)).

## Detail fields

`--detail` adds the detail behind the summaries in [Audition](audition.md#json-schema):

| Field | Type | Meaning |
| --- | --- | --- |
| `bars[].sample_peak_dbfs` | number | Highest sample of the mix in the bar. |
| `bars[].sounding` | array | Tracks that sound in the bar, in score order. |
| `bars[].bands[]` | array | Absent with `--no-bands`. Seven entries in band order: `band`, `level_db` (the analyzed tracks summed, dB re a full-scale RMS, so a full-scale sine reads -3), `quiet`, `owner` and `share_pct`, `second` and `second_share_pct`, `collision`. `owner` and `second` are absent when the band is quiet. |
| `tracks[].bands_db` | array | The track's level in each band over the range, dB re a full-scale RMS, in band order. |
| `tracks[].bar_level_db` | array | The stem's RMS in each bar, dB re its loudest bar (-120 when silent). |
| `tracks[].sounding` | array | One boolean per bar: the track sounds there. |
| `tracks[].bar_bands_db` | array | The track's band levels per bar: one array of seven per bar. |
| `tracks[].section_bands_db` | array | The same per section, in `sections[]` order. |
| `sections[].true_peak_dbtp` | number | True peak of the section. |
| `sections[].band_collision_bars` | array | For each band, the bars whose first tick is in the section and whose band collides. |
| `sections[].bands[]` | array | As `bars[].bands[]` over the section's frames, plus `collision_bars`. |
| `bands.collisions[].bars` | array | The one-based bars where the band collides; absent for a band with none. |
| `loudness.short_term_per_beat_lufs` | array | One value per beat of the song's meter (a quarter note in 4/4): the 3 s window that ends where the beat ends; null while under 3 s of the range precede it. |

## Worked example: a drop-out and a low-end collision

This score plays a groove for four bars, drops to hats for two, then adds a
sine sub under the bass and kick for two:

```cicada
cicada 2
title "Drop and lift"
tempo 120
key a minor
seed 3

instrument subline {
  voice mono {
    out = sine(pitch) * adsr(gate, 5ms, 40ms, 0.9, 60ms) * velocity * 0.15
  }
}

track bass acid { cutoff = 620Hz level = 6dB }
track sub subline {}
track drums drums { level = -12dB }

pattern riff { a1 . c2 . e2 . a1 . a1 . c2 . g2 . e2 . }
pattern low notes {
  gate = 100%
  a1 - - - - - - - c2 - - - e2 - - -
}
pattern beat drums {
  bd: x... x... x... x...
  ch: ..x. ..x. ..x. ..x.
}
pattern hats drums {
  ch: ..x. ..x. ..x. ..x.
}

scene groove { bass = riff drums = beat }
scene drop { bass = stop drums = hats }
scene lift { bass = riff sub = low drums = beat }
song { groove*4 drop*2 lift*2 }
```

The report says (shortened):

```text
energy (ITU-R BS.1770-4, the render's loudness meter; short-term = 3 s window ending at the bar line):
  whole range: -30.0 LUFS integrated, loudness range 11.5 LU, true peak -19.1 dBTP, max short-term -28.5 LUFS
  section        bars      LUFS vs top   LRA TP dBTP  short-term per bar (...)
  groove         1-4      -30.3   -1.5   0.1   -20.1  ·███
  drop           5-6      -44.2  -15.4   1.5   -23.8  ▇▃
  lift           7-8      -28.8    0.0   0.4   -19.1  ██
  loudest section: lift, bars 7-8 (-28.8 LUFS); quietest: drop, bars 5-6 (-44.2 LUFS, 15.4 LU under)

   bar  sub                low                bass               low-mid            mid                presence           air
     6  quiet              quiet              quiet              quiet              quiet              drums 100 -68      drums 100 -52
     7  bass 67 -57        drums 64* -30      drums 55* -36      drums 88 -41       bass 82 -52        bass 84 -59        drums 91 -51
  low 40-80 Hz collisions: 2 of 8 bars (7-8); pairs: sub+drums 2
  bass 80-160 Hz collisions: 2 of 8 bars (7-8); pairs: sub+drums 2

  track        notes =prev    =4  run  bars 1-8
  bass             6     4     2    4  ████ ··██
  sub              2     1     0    2  ···· ··██
  drums            8     5     2    4  ████ ▄▂██
```

Read the drop-out first. The `drop` section is 15.4 LU under the loudest
section, and its short-term blocks fall from `▇` to `▃`: bar 5's window still
holds the last second of the groove, bar 6's is all hats. The activity rows
confirm what emptied: the bass and the sub do not sound in bars 5 and 6 (`··`),
and the drums drop to `▄▂`, 14 and 20 dB under their loudest bar, because only
the hats play. Bar 6 has no owner below 1.6 kHz at all.

Then the low end. In bars 7 and 8 the kick holds 64% of 40-80 Hz and the sub
most of the rest (with `--detail`, `bars[6].bands[1]` reads `"owner": "drums",
"share_pct": 63.8, "second": "sub", "second_share_pct": 29.5, "collision":
true`; the default JSON sums it up in `bands.collisions[1].pairs`: `sub` and
`drums` in 2 bars). Both are
over 25%, so the bar collides: the sub and the kick share the band in which a
mix usually gives the bottom to one of them. Duck the sub under the kick, move
the sub an octave, or give the kick less body. The groove bars do not collide
in `low`: the kick owns it (91%), because the acid bass puts most of its
energy above 125 Hz. Both effects show in `energy.png` (the dip at bar 5) and
in `spectrogram.png` (the empty floor in bars 5 and 6 and the bright 50-100 Hz
band from bar 7).

## Limits

- Each bar is cut out of the stem before its frames are taken. A note held
  across a bar line is cut there, which spreads a little of its energy into
  neighbouring bands in the frames next to the line; notes that start and stop
  on the line put attack and release clicks there anyway. The quiet rules keep
  that spread from making an owner when it is far under the bar's content, not
  when a band holds nothing else (see Band owners); the band's level shows it.
- A bin belongs to one band, but the window spreads a tone over its
  neighbouring bins: about -6 dB one bin (5.86 Hz) away and about -30 dB two
  to three bins away. A tone within about 15 Hz of a band edge therefore also
  shows in the next band. A sine exactly on an edge splits by where the edge
  falls between bins: at 40 Hz, 27% sub and 73% low; at 80 Hz, 39% low and 61%
  bass; at 160 Hz, 64% bass and 36% low-mid; at 400 Hz, 67% low-mid and 33%
  mid; at 1,600 Hz, 80% mid and 20% presence; at 5,000 Hz, 63% presence and
  37% air. A sine at a band's geometric centre keeps over 99.9% in its band.
  Read a share near an edge, such as a 41 Hz E1 against the 40 Hz edge, with
  that in mind.
- A collision says that two tracks hold energy in the same band in the same
  bar. It is not a masking model: it does not say whether either is heard.
- Short-term loudness in the bar table is the window that ends at the bar
  line. At tempos where a bar is shorter than 3 s, it includes the bar
  before. Section loudness starts a fresh meter at the section start, so its
  filters and windows do not see the section before. A loudness range needs
  3 s (it is null under that), and over less than about a minute it rests on
  few windows.
- The audition renders without a loudness target or tail, at the score's own
  levels. A listening render with `--loudness` adds input gain before the
  master chain, which changes how hard a master compressor works, so its
  loudness range can be lower than the audition's.
- Peaks are ranked among the eight highest sample-peak moments per listed
  peak; a true-peak moment outside them could rank only if many moments sit
  within the few dB by which a true peak can exceed a sample peak.
- The activity threshold is relative to each track's own loudest bar, so a
  track that is quiet throughout still counts as sounding; read
  `bar_level_db` and the track level for how loud it is.
- Repeats compare note events, so a sampler's round-robin or a modeled
  instrument's variation between equal notes does not make two bars differ.
- A pitched percussion voice such as a log drum adds energy at its third
  harmonic (a fifth above the octave), which can pull the heard chord a fifth
  above the written one when it outweighs the harmony parts.
- A peak's parts are measured before the master chain: they split the signal
  that enters it at the peak's sample. The master chain can reshape that
  signal before it becomes the mix's peak: a music-bus compressor (more so
  when a track's sidechain ducks the music at the very hits that set the
  peaks), master EQ and the limiter. `pre_master_sample` and the mix's
  `sample_peak_dbfs` show how much it changed. The parts are read at the
  mix's largest sample; a true peak between samples can differ slightly.
- The activity matrix, and `sounding_spans`, have one cell per bar: a part
  that rests for a beat or two inside a bar still sounds in that bar.
  `activity.silences`, and with `--detail` `bars[].sample_peak_dbfs` and
  `loudness.short_term_per_beat_lufs`, show shorter drops.

## Cost

The added measures reuse the render, the stems and the note events the report
already has; nothing is rendered twice. Their cost is arithmetic on the stems
in memory. A CPU profile of `audition --json` on the four-track, 95-second
reference song `examples/songs/lanterns-after-rain.cicada` (13.1 s of CPU in
all) puts them at about a fifth of the run:

| Part | CPU | Share |
| --- | --- | --- |
| Band ownership: one 8,192-point transform every 2,048 samples of each stem | 1.71 s | 13% |
| Loudness: two passes of the meter over the mix (whole range, then sections and peaks) | 0.90 s | 7% |
| The rest (activity, repeats, silences) | under 0.1 s | under 1% |
| The render that makes the stems, for comparison | 6.34 s | 48% |

Band ownership grows with the number of tracks and the length; the loudness
passes only with the length. `--no-bands` skips band ownership. Memory does
not change measurably: the report already holds every analyzed stem in memory
as 32-bit float, about 23 MB per track per minute of song, and `--tracks`
lowers that.

Measured on the 181-second, twelve-track reference song
`examples/songs/anywhere-you-are.cicada` (Apple M1 Max, four Go threads,
load average 4 to 9):

| `audition --json` | Wall time | Peak memory |
| --- | --- | --- |
| Before these measures | 58 to 60 s | 1.84 GB |
| With them | 70 to 75 s | 1.83 GB |
| With them, `--no-bands` | 63 s | 1.85 GB |

The JSON stays small by default: 63.8 KB for that song, against about
49 KB before these measures, and 439 KB with `--detail`
([JSON schema](audition.md#json-schema)).
