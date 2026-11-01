# Audition: check what a score sounds like

`cicada audition` renders a score and tells you what came out, so you can check
the music without listening. A score can pass every loudness check and still
sound wrong: a part is too quiet to hear, the horns drone over the rhythm, or
the mix lands in a different key than you wrote. The audition report shows
those problems as numbers and pictures.

```sh
cicada audition song.cicada -o audition/
cicada audition song.cicada --from 9 --bars 8 --json
```

| Flag | Meaning |
| --- | --- |
| `-o <dir>` | Write `spectrogram.png`, `chroma.png` and `rhythm.png` to the directory. Without it, no images are written. |
| `--from <bar>` | One-based first bar (default 1). |
| `--bars <n>` | Bars to analyze; 0 means through the song end. |
| `--json` | Print the report as JSON instead of text. |
| `--max-resident-pcm <size>` | Cap on the decoded audio of the score's sampler packs, such as `2GiB` (default 1 GiB). `CICADA_MAX_RESIDENT_PCM` sets it when the flag is omitted. A score over the cap is refused before any pack is read. |

The command renders the mix and the per-track stems in process with the same
engine as `cicada render` and `cicada stems`, with no tail. The same score and
options give the same report and the same image bytes. Levels and spectra
combine the left and right channels by power, so opposite-polarity stereo is
measured at its true level instead of cancelling. It needs no extra tools.
Rendering takes about as long as `cicada render`.

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

## Reading the images

All three images are the mix, left to right in time. A vertical line marks each
bar; every fourth bar (bars 4, 8, ...) is brighter.

- `spectrogram.png`: frequency from 40 Hz (bottom) to 12 kHz (top) on a log
  scale, color from dark (quiet) to yellow (loud) over a 70 dB range. Kick
  drums show as bright low stripes, hats as noise at the top, held notes as
  horizontal lines.
- `chroma.png`: the pitched stems (not the full mix); the 12 pitch classes (C at the bottom, B at the top), one
  column per sixteenth note. A chord shows as the same few rows lit together.
  If your written notes are not lit, they are not audible.
- `rhythm.png`: one row per sounding track (in track order), 16 cells per row,
  the same data as the step columns.

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

## JSON schema

`--json` prints one object. Field names are stable within the `format` value
`cicada.audition/1`. Decibel values are rounded to 0.1, other numbers to 2 or 3
decimals. Levels below the floor read -120.

| Field | Type | Meaning |
| --- | --- | --- |
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
| `images[]` | array | File names written to `-o`. Omitted without `-o`. |

## Limits

The measures are heuristics from the rendered audio, not a transcription.
Note starts come from spectral flux and miss soft notes under loud ones.
`decay20` stops at the next detected attack, so a very dense part reports a
short gap. Written chords use onsets only: a note held from an earlier bar does
not count. Energy below 125 Hz does not affect the heard chord. A sampler that plays drum hits counts as pitched.

### Stereo compatibility per bar

Each non-silent bar includes `stereo.channel_correlation` (uncentered L/R correlation, null if either channel has no energy) and `stereo.mono_vs_stereo_db` (RMS of `(L+R)/2` relative to stereo RMS, floored at -120 dB). Silent bars omit `stereo`. These are whole-band, bar-averaged indicators, not a perceptual quality score: short cancellations and frequency-specific issues can be hidden. Audition the mono mix and inspect shorter windows when needed. Measurements use the rendered audition master, not an external encoded delivery.
