# Audition: check what a score sounds like

`cicada audition` renders a score and tells you what came out, so you can check
the music without listening. A score can pass every loudness check and still
sound wrong: a part is too quiet to hear, the horns drone over the rhythm, or
the mix lands in a different key than you wrote. The audition report shows
those problems as numbers and pictures. It also shows what decides a mix: how
the loudness moves from section to section, where the band drops out, which
track owns each frequency band in each bar, and which hits set the peaks.

```sh
cicada audition song.cicada -o audition/
cicada audition song.cicada --from 9 --bars 8 --json
cicada audition song.cicada --tracks bass,kick,logdrum --json
```

| Flag | Meaning |
| --- | --- |
| `-o <dir>` | Write `spectrogram.png`, `chroma.png`, `rhythm.png` and `energy.png` to the directory, and pages of the spectrogram and chroma when the range is wider than `--image-width` ([Reading the images](#reading-the-images)). Without it, no images are written. |
| `--from <bar>` | One-based first bar (default 1). |
| `--bars <n>` | Bars to analyze; 0 means through the song end. |
| `--tracks <a,b,c>` | Analyze only these tracks and the mix. Only their stems are written and read, so the run needs less memory and disk. A name the score does not have is an error that lists the score's tracks (`unknown_track` with `valid_tracks` in JSON). See [Choosing tracks](#choosing-tracks). |
| `--peaks <n>` | How many true-peak moments of the mix to list, 0 to 100 (default 10; 0 lists none). |
| `--bar-bands` | Add each track's level in each band per bar to the JSON (`tracks[].bar_bands_db`). |
| `--no-bands` | Skip band ownership, about an eighth of the audition's CPU time (see [Cost](#cost)). The band fields are then absent. |
| `--image-width <px>` | Widest picture in pixels (default 1600; at least 200). 0 keeps one full-resolution strip of each picture, as before. |
| `--json` | Print the report as JSON instead of text. |
| `--max-resident-pcm <size>` | Cap on the decoded audio of the score's sampler packs, such as `2GiB` (default 1 GiB). `CICADA_MAX_RESIDENT_PCM` sets it when the flag is omitted. A score over the cap is refused before any pack is read. |

The command renders the mix and the per-track stems in process with the same
engine as `cicada render` and `cicada stems`, with no tail. The same score and
options give the same report and the same image bytes. Levels and spectra
combine the left and right channels by power, so opposite-polarity stereo is
measured at its true level instead of cancelling. It needs no extra tools.
Rendering takes about as long as `cicada render`; the measures add a few
seconds for a whole song (see [Cost](#cost)).

## Reading the text report

**Header.** Tempo, the key you wrote, the bar range, and one line per distinct
swing value listing the tracks that play it (`swing 56%: drums, bass`). `BPM` is the score tempo. When scenes set their own tempo, a `tempo changes` line lists each step (`bar 3: 180 BPM`). For a range that starts after a change, the first step is the tempo at the first analyzed bar. Without a step at the first bar, the score tempo applies there. Bars, chroma frames, sections and the images follow the same tempo map as the renderer, so a bar after a change is measured where it sounds.

**Heard key.** The key estimated from the pitched stems, by the
Krumhansl-Schmuckler method: their pitch-class energy is correlated with 24
major and minor key profiles. The top three are listed with their correlation
(1.0 is a perfect match). The pitched stems are the sum of every track that
plays notes (acid, keys, piano, guitar, instruments, samplers); drum tracks and
audio clips are left out, so drum noise cannot change the verdict. The line
`full mix key` gives the same estimate from the whole mix. When the two differ,
something unpitched is loud. Relative keys (A minor and E minor, C major and A
minor) score close, so read the top three, not only the first.

**Audibility.** Harmony can be right and still inaudible. The report measures
the energy of the pitched stems in the 125 Hz to 2 kHz band against the energy
of the whole mix in the same band, in dB (0 means the pitched parts are all of
the mix, -10 means a tenth of it). It is given for the whole range and for each
bar (`aud dB`). A bar is marked `MASKED` when this is below -15 dB, that is,
when the pitched parts hold under about 3% of the mix energy in the band. The
threshold is a judgment call: a part a few dB below it is quiet but usually
still audible. MASKED is separate from `DISAGREE`.

**Harmony per bar.** Each bar has two chord names:

- *written* is the chord that best matches the pitch classes of all pitched
  notes that start in that bar (drum and audio tracks are ignored). The notes
  are the ones the renderer plays, captured during the same render, so section
  chains, the phase of a pattern started by a scene change, arrangement
  placements, ties, chance and transposition are already applied. With one or
  two pitch classes the notes are listed, joined with `+`, because they do not
  decide a chord.
- *heard* is the chord that best matches the pitch-class energy of the summed
  pitched stems in that bar. The templates are major, minor, `7`, `m7` and
  `maj7`; a seventh must beat its triad by a small margin to win. Energy below
  125 Hz is ignored so bass fundamentals do not decide the chord, and only
  spectral peaks count so broadband noise adds little.

The `match` column is `agree` when the heard chord has the same root and the
same major or minor third as the written chord, or when its notes all appear in
the written set. It is `DISAGREE` otherwise, and `silent` when the pitched stems
make no sound in a bar that has written notes. `DISAGREE` means the notes
played differ from the notes written. `no-written-pitch` means no pitched note
starts in the bar. `agree MASKED` means the right notes play but cannot be
heard. This is a heuristic: dense or chromatic writing can disagree with a
correct render.

**Tracks.** One row per track, from the stems:

| Column | Meaning |
| --- | --- |
| `rms` | Level of the track's stem in dBFS (RMS). |
| `vs top` | Energy of this track in dB relative to the loudest track. |
| `share` | Share of the summed track energy, in percent. |
| `band dB` | Energy of the track in 125 Hz to 2 kHz relative to the whole mix in that band, in dB. Below -15 the track is masked. |
| `bright` | Spectral centroid in Hz: higher is brighter. |
| `attack` | Median time from the start of a note to its peak, in ms. |
| `decay20` | Median time for the level to fall 20 dB below its peak, in ms. Each note is measured only until the next detected attack on that track. A `>` means the median note did not fall 20 dB before the next attack (or 2 s), so the value is a lower bound: the note is sustained. |
| `on/bar` | Detected note starts per bar over the bars where the track plays or sounds. A bar counts when a note starts or is still held in it (a held note with no release lasts to the end of the range) or when the stem has sound in it (for a track that plays notes, sound in the half second after a bar starts is treated as the release tail of the bar before and is ignored). A note ends when the renderer releases it: its own release, a scene that stops the track, the end of a placement, or, for a held pitched note, the next note on the track. A drum hit or a clip start has no length: it counts only the bar it starts in, and its tail counts only through the stem energy above. A note that starts mid-bar counts its first bar. |
| step 1-16 | Rhythm profile (next paragraph). The percentage is the swing of the track's pattern. |

The **rhythm profile** shows where in the bar the track's attacks land. Each
cell is the mean onset strength at one sixteenth of the bar, scaled so the
strongest is full height (`█`); `·` is none. Cells are grouped by beat. For a
swung pattern the cell is read where the swing places that sixteenth, so a
shuffle shows its offbeat notes in the swung cells. Only bars where the track
plays a pattern count. A skank on the offbeats fills cells 3, 7, 11 and 15.

**Sections.** One row per song entry, or per marker in an arrangement: mix
loudness (RMS dBFS), brightness, note starts per bar over all tracks, and the
tracks in it. A section runs from its marker tick to the next marker tick, and
every measure uses exactly those ticks, so a marker inside a bar (for example
at beat 3) splits that bar between two sections. `bars` shows the bar that
holds the first tick through the bar that holds the last tick, so two sections
can share a bar number; the JSON `from_tick` and `to_tick` give the exact
boundaries (960 ticks per quarter note, 3840 per bar). Note starts per bar is
the count divided by the section length in bars, fractional for a short
section. The range chosen with `--from` and `--bars` clips the first and last
section. A track is in a section when its stem sounds there or it played
notes there, so audio clips, samplers and sustained tails are listed.

**Energy.** The loudness of the mix, measured with the meter the renderer
uses to check a loudness target (`kernel/loudness`, ITU-R BS.1770-4 with the
EBU R 128 gates). The first line gives the whole range: integrated loudness,
loudness range, true peak and the highest short-term loudness. Then one row
per section:

| Column | Meaning |
| --- | --- |
| `LUFS` | Integrated loudness over exactly the section's ticks, from a meter started at the section start. `n/a` when no 400 ms block passes the -70 LUFS gate. |
| `vs top` | LU under the loudest section. |
| `LRA` | Loudness range of the section (needs 3 s; short sections read 0). |
| `TP dBTP` | True peak of the section (4x oversampled at 48 kHz). |
| short-term per bar | One block per bar: the short-term loudness (3 s window) that ends at the bar's end, against the loudest bar of the range. `█` is within 3 LU of it, each step down is 3 LU more, `·` is 24 LU under or has no value (the first 3 s of the range have none). |

The last line names the loudest and the quietest section and the LU between
them. A drop-out shows as a section many LU under the others and as low blocks
in its bars; a build shows as blocks that rise bar by bar. The audition renders
at the score's own levels, with no loudness target, so its LUFS are those of
the unnormalized mix; the differences between sections and bars are what to
read.

**Band owners.** For every bar and seven fixed bands, the track that holds the
largest share of the band's energy, with its share in percent. The bands, in
Hz, hold the FFT bins whose centre frequency f is in `low <= f < high`:

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
tracks each hold more than 25% of the band. A band is `quiet`, with no owner,
when it is more than 30 dB under its own loudest bar, more than 30 dB under the
loudest band of the same bar, or more than 40 dB under the loudest band of any
bar; that keeps window leakage, attack clicks and the tail of a neighbouring
bar from making an owner. Under the table, one row per section counts the
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
50 ms apart, with their `bar.beat.sixteenth` and time, and the tracks that set
each one: each track's share of the summed stem energy in the 5 ms around the
peak (shares under 1% are left out, at most five tracks). The report finds the
blocks of 10 ms with the highest sample peaks, measures the true peak of the
eight best per listed peak, and ranks those. When a limiter or a loudness
target falls short, these are the hits to look at.

## Reading the images

The images run left to right in time. A vertical line marks each bar; every
fourth bar (bars 4, 8, ...) is brighter.

- `spectrogram.png`: the mix, frequency from 20 Hz (bottom) to 20 kHz (top) on
  a log scale, color from dark (quiet) to yellow (loud) over a 70 dB range. The
  bar number is printed at the top of each bar line (where there is room) and
  the frequency at the left edge (30, 50, 100, 200, 500, 1k, 2k, 5k and 10k
  Hz). Kick drums show as bright low stripes, hats as noise at the top, held
  notes as horizontal lines. One column is 1,024 samples (21 ms at 48 kHz).
  Below about 100 Hz the rows fall between the FFT's 11.7 Hz bins and are
  interpolated, so the picture shows where low energy is but cannot tell 35 Hz
  from 45 Hz; the band table is the measurement.
- `chroma.png`: the pitched stems (not the full mix); the 12 pitch classes (C
  at the bottom, B at the top), three pixels per sixteenth note. A chord shows
  as the same few rows lit together. If your written notes are not lit, they
  are not audible.
- `rhythm.png`: one row per sounding track (in track order), 16 cells per row,
  the same data as the step columns. It has no time axis, so the width cap
  does not apply.
- `energy.png`: the short-term loudness of the mix every 100 ms (white),
  drawn where each 3 s window ends; a white line and the bar number at each
  section start; each section's integrated loudness as an orange segment; grid
  lines every 6 LU, labelled in LUFS at the right edge.

No picture is wider than `--image-width` (1,600 pixels by default; an agent's
image input is scaled to about 1,568 pixels on its long side). When the range
does not fit, `spectrogram.png` and `chroma.png` show the whole range averaged
in time (the spectrogram averages power), and the range is also cut into pages
of whole bars at full resolution: `spectrogram-bars-001-008.png` and
`chroma-bars-001-008.png`, named by their first and last bar. A page holds 32,
16, 8, 4, 2 or 1 bars, the most that fit. At 74 BPM a bar is 152 spectrogram
columns, so a 64-bar song gives eight pages of 8 bars. A short excerpt that
fits writes the same four files as before and no pages. The JSON `pictures`
list names every file with its bar range.

## Worked example

A ska score writes C, F, G, C over four bars. A guitar and an organ play the
chords on the offbeats, a bass plays the roots, and a kick plays four on the
floor. The report says:

```text
heard key (pitched stems): C major (0.88), G major (0.81), E minor (0.76)
full mix key: C major (0.80), F major (0.58), A minor (0.53)
audibility: pitched stems are -5.1 dB re the mix in 125 Hz-2 kHz (MASKED below -15 dB); 0 of 4 bars masked

   bar  written        heard    mix dB  aud dB  match
     1  C              C         -29.8    -5.1  agree
     2  F              F         -30.2    -5.1  agree
     3  G              G         -29.4    -5.1  agree
     4  C              C         -29.8    -5.1  agree

  track          rms  vs top  share  band dB  bright  attack  decay20  on/bar  step 1-16 (swing)
  guitar       -39.3    -6.4   9.1%     -0.6    4362    19.2       63    4.00  ··█· ··█· ··▇· ··▆· (50%)
  piano        -37.1    -4.2  15.3%      1.2    3918    19.2       48    4.00  ··█· ··█· ··▇· ··▆· (50%)
  bass         -33.4    -0.5  35.5%    -19.1     137    18.4       75    3.75  ▅··· █··· ▇··· ▅··· (50%)
  kit          -32.9     0.0  40.1%     -1.7     904    17.5 >    483    4.00  ▅··· █··· █··· █··· (50%)
```

The key and chords agree with the score and nothing is masked. The guitar and
organ land on the offbeats (cells 3, 7, 11 and 15) and sit 4 to 6 dB under the
kit and bass. The bass reads -19 dB in the band because its energy is mostly
below 125 Hz, so the band measure does not see it.

The bundled `examples/first-acid.cicada` shows the other case. Its report reads
`heard key (pitched stems): E minor (0.75), A minor (0.69), ...` and
`full mix key: F# minor`, with every bar `agree`: the written harmony is
played. The drums hold 98% of the energy, the bass is 28 dB under them (`vs
top`) and 22 dB under the mix in the band, and the lead is 11 dB under, so the
whole pitched part reads -10 dB, quiet but above the -15 dB threshold. A bar
reads `agree MASKED` when the same notes play but sit under that threshold.

A part whose `decay20` reads `> 1355` next to a skank at 60 ms holds long notes
over the short ones, which sounds like a drone instead of a skank. In the skank
above, `decay20` is about 50 to 60 ms for the guitar and organ, and the kit's
`> 483` means its median sound is still sounding when the next one arrives.

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
  loudest section: lift (-28.8 LUFS); quietest: drop (-44.2 LUFS, 15.4 LU under)

   bar  sub           low           bass          low-mid       mid           presence      air
     6  quiet         quiet         quiet         quiet         quiet         drums 100     drums 100
     7  bass 67       drums 64*     drums 55*     drums 88      bass 82       bass 84       drums 91
  low 40-80 Hz collisions: 2 of 8 bars (7-8); pairs: sub+drums 2

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
most of the rest (`bars[6].bands[1]` reads `"owner": "drums", "share_pct":
63.8, "second": "sub", "second_share_pct": 29.5, "collision": true`). Both are
over 25%, so the bar collides: the sub and the kick share the band in which a
mix usually gives the bottom to one of them. Duck the sub under the kick, move
the sub an octave, or give the kick less body. The groove bars do not collide
in `low`: the kick owns it (91%), because the acid bass puts most of its
energy above 125 Hz. Both effects show in `energy.png` (the dip at bar 5) and
in `spectrogram.png` (the empty floor in bars 5 and 6 and the bright 50-100 Hz
band from bar 7).

## Choosing tracks

`--tracks bass,kick` renders as usual (every track plays, so the mix is the
song's mix) but writes and reads only the named stems. The fields that
describe tracks then cover only those tracks: `tracks[]`, each section's
`tracks`, every share (`energy_share_pct`, band shares, peak contributions),
`level_vs_loudest_db`, the swing groups, and the written and heard chords and
keys, which come from the chosen pitched tracks. The mix measures (`mix_rms_dbfs`,
`audibility_db` against the mix, loudness, silences, peak levels) are those of
the whole mix. `selected_tracks` lists the chosen tracks; it is absent without
the flag. `cicada stems --tracks` writes the same way: the chosen stems and
`master.wav`, each the same as in a full set, and no return or bus stems.

## JSON schema

`--json` prints one object. Field names are stable within the `format` value
`cicada.audition/1`. Decibel values are rounded to 0.1, other numbers to 2 or 3
decimals. Levels below the floor read -120.

| Field | Type | Meaning |
| --- | --- | --- |
| `schema` | string | Always `cicada.audition.v1`, named as the other `--json` formats are ([agent formats](agents.md)). |
| `format` | string | Always `cicada.audition/1`. |
| `title` | string | Score title. |
| `sample_rate` | number | Render rate in Hz (48000). |
| `score.tempo_bpm` | number | Score tempo. |
| `score.written_key` | string | Key from the score, for example `A minor`. |
| `score.song_bars` | number | Bars in the whole song. |
| `score.from_bar`, `score.bars` | number | First analyzed bar (one-based) and the count. |
| `score.tempo_changes[]` | array | Scene tempo steps that govern the analyzed bars: `bar` (one-based) and `bpm`. Omitted when none. `score.tempo_bpm` stays the score tempo. |
| `score.swing[]` | array | One entry per distinct swing value, ascending: `swing_percent`, `tracks`, `patterns`. Omitted when none. |
| `heard_key[]` | array | Top three keys from the summed pitched stems: `key` (for example `C major`), `score` (correlation). |
| `full_mix_key[]` | array | The same from the whole mix. |
| `audibility_db` | number | Pitched stems against the whole mix, 125 Hz to 2 kHz, in dB. |
| `masked_bars` | number | Bars with `masked` true. |
| `bars[]` | array | One per analyzed bar: `bar` (one-based), `written_notes` (pitch classes), `written`, `heard` (from the pitched stems), `heard_fit` (0..1 cosine similarity of the pitch-class energy to the chosen chord), `mix_rms_dbfs`, `audibility_db`, `masked` (`audibility_db` below -15), `match` (`agree`, `disagree`, `no-written-pitch`, `silent`). |
| `harmony_mismatches` | number | Count of bars with `match` `disagree`. |
| `tracks[]` | array | One per track, in score order: `id`, `silent` (true under -80 dBFS; the other fields except `rms_dbfs` are then zero, including `energy_share_pct`, and the silent track is left out of the other tracks' shares), `rms_dbfs`, `level_vs_loudest_db`, `energy_share_pct`, `band_vs_mix_db`, `masked` (`band_vs_mix_db` below -15), `centroid_hz`, `attack_ms`, `decay20_ms`, `decay20_lower_bound`, `onsets`, `active_bars` (bars where the track plays or sounds), `onsets_per_bar`, `swing_percent`, `rhythm` (16 numbers, 0..1, slot 1 first). |
| `sections[]` | array | `name`, `tempo_bpm` (tempo at the section start), `from_bar` (bar holding the first tick, one-based), `to_bar` (bar holding the last tick, inclusive), `from_tick`, `to_tick` (absolute ticks, end exclusive), `rms_dbfs`, `centroid_hz`, `onsets_per_bar`, `tracks`. |
| `images[]` | array | `spectrogram.png`, `chroma.png` and `rhythm.png` when `-o` is given, as before; the other files are in `pictures`. Omitted without `-o`. |

The fields below were added within `cicada.audition/1`; the fields above keep
their values. Loudness values are LUFS or LU rounded to 0.1; dB levels are
floored at -120; null means the method cannot give a value.

| Field | Type | Meaning |
| --- | --- | --- |
| `bars[].short_term_lufs` | number or null | Short-term loudness of the 3 s window that ends at the bar's end; null while under 3 s of the range precede it. |
| `bars[].sample_peak_dbfs` | number | Highest sample of the mix in the bar. |
| `bars[].sounding` | array | Tracks that sound in the bar (the activity rule above), in score order. |
| `bars[].bands[]` | array | Absent with `--no-bands`. Seven entries in band order: `band`, `level_db` (the analyzed tracks summed, dB re a full-scale RMS, so a full-scale sine reads -3), `quiet`, `owner` and `share_pct`, `second` and `second_share_pct`, `collision`. `owner` and `second` are absent when the band is quiet. |
| `tracks[].bar_level_db` | array | The stem's RMS in each bar, dB re its loudest bar (-120 when silent). |
| `tracks[].sounding` | array | One boolean per bar: the track sounds there. |
| `tracks[].note_bars` | number | Bars where the track starts a note. |
| `tracks[].same_as_previous_bar` | number | Bars whose played notes equal the bar before. |
| `tracks[].same_as_four_bars_before` | number | Bars whose played notes equal the bar four before. |
| `tracks[].longest_same_run` | number | Longest stretch of identical consecutive bars, in bars; 0 when no bar equals the one before. |
| `tracks[].bands_db` | array | The track's level in each band over the range, dB re a full-scale RMS. |
| `tracks[].bar_bands_db` | array | The same per bar: one array of seven per bar. Only with `--bar-bands`: it is the largest part of the document (seven numbers per track per bar). |
| `tracks[].section_bands_db` | array | The same per section, in `sections[]` order. |
| `sections[].integrated_lufs` | number or null | Integrated loudness over the section's ticks, from a meter started at the section start. |
| `sections[].loudness_range_lu` | number | Loudness range of the section. |
| `sections[].true_peak_dbtp` | number | True peak of the section. |
| `sections[].short_term_max_lufs`, `sections[].short_term_min_lufs` | number or null | Highest and lowest short-term loudness of the 3 s windows inside the section, read every 100 ms; null under 3 s. |
| `sections[].vs_loudest_lu` | number or null | `integrated_lufs` minus the loudest section's. |
| `sections[].bands[]` | array | As `bars[].bands[]` over the section's frames, plus `collision_bars`: bars whose first tick is in the section and whose band collides. |
| `selected_tracks` | array | Tracks chosen with `--tracks`, in score order. Absent without it. |
| `loudness` | object | The mix over the range: `integrated_lufs`, `loudness_range_lu`, `true_peak_dbtp`, `sample_peak_dbfs`, `max_short_term_lufs`, `max_momentary_lufs`, `loudest_section`, `quietest_section`, `section_spread_lu` (loudest minus quietest), and `short_term_per_beat_lufs`: one value per quarter note, the 3 s window that ends where the beat ends. Absent at a sample rate the meter does not support. |
| `bands` | object | Absent with `--no-bands`. `names`, `edges_hz` (eight edges for seven bands), `collision_share_pct` (25), `quiet_below_band_db` (30), `quiet_below_span_db` (30), `quiet_below_range_db` (40), and `collisions[]`: per band, `band`, `bars` (one-based bars with a collision) and `pairs` (`tracks`, the two colliding tracks in score order, and `bars`, how often), most frequent first. |
| `activity` | object | `sounding_threshold_db` (-30), `tail_grace_s` (0.5), `silence_below_dbfs` (-60), `silence_min_s` (0.25), and `silences[]`: `start` and `end` positions and `length_s`. |
| `peaks[]` | array | Highest true peaks first: `rank`, `at` (a position), `true_peak_dbtp`, `sample_peak_dbfs`, and `tracks[]` (`id`, `share_pct`, `peak_dbfs`: the track's own sample peak in those 5 ms). Empty with `--peaks 0`. |
| `pictures[]` | array | Every file written to `-o`, in write order: `file`, `kind` (`spectrogram`, `chroma`, `rhythm`, `energy`), `from_bar` and `to_bar` (one-based, inclusive; absent for `rhythm`), `width`, `height`, and `scaled` (true when the time axis was averaged to fit). Omitted without `-o`. |

A position (`at`, `start`, `end`) has `text` (`bar.beat.sixteenth`, as an
`@` placement position), `bar`, `beat` and `sixteenth` (one-based song
positions), `tick` (absolute, 960 per quarter note) and `time_s` (seconds from
the start of the analyzed range).

## Limits

The measures are heuristics from the rendered audio, not a transcription.

The added measures have these limits:

- Each bar is cut out of the stem before its frames are taken. A note held
  across a bar line is cut there, which spreads a little of its energy into
  neighbouring bands in the frames next to the line; notes that start and stop
  on the line put attack and release clicks there anyway. The quiet rules keep
  either from making an owner when the band holds nothing else.
- A bin belongs to one band, but the window spreads a tone over its
  neighbouring bins: about -6 dB one bin (5.86 Hz) away and about -30 dB two
  to three bins away. A tone within about 15 Hz of a band edge therefore also
  shows in the next band. Read a share near an edge, such as a 41 Hz E1
  against the 40 Hz edge, with that in mind.
- A collision says that two tracks hold energy in the same band in the same
  bar. It is not a masking model: it does not say whether either is heard.
- Short-term loudness in the bar table is the window that ends at the bar
  line. At tempos where a bar is shorter than 3 s, it includes the bar
  before. Section loudness starts a fresh meter at the section start, so its
  filters and windows do not see the section before. A loudness range over
  less than about a minute rests on few windows.
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
Note starts come from spectral flux and miss soft notes under loud ones.
`decay20` stops at the next detected attack, so a very dense part reports a
short gap. Written chords use onsets only: a note held from an earlier bar does
not count. Energy below 125 Hz does not affect the heard chord. A sampler that plays drum hits counts as pitched.

### Stereo compatibility per bar

Each non-silent bar includes `stereo.channel_correlation` (uncentered L/R correlation, null if either channel has no energy) and `stereo.mono_vs_stereo_db` (RMS of `(L+R)/2` relative to stereo RMS, floored at -120 dB). Silent bars omit `stereo`. These are whole-band, bar-averaged indicators, not a perceptual quality score: short cancellations and frequency-specific issues can be hidden. Audition the mono mix and inspect shorter windows when needed. Measurements use the rendered audition master, not an external encoded delivery.

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
lowers that. The JSON grows: about 300 KB for a three-minute song of twelve
or twenty-four tracks, plus about 140 bytes per track per bar with
`--bar-bands`.
