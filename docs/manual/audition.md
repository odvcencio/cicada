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
| `--tracks <a,b,c>` | Analyze only these tracks and the mix, reading only their stems into memory. An unknown name fails with `unknown_track` and `valid_tracks`; an empty or repeated name with `usage`. See [Choosing tracks](#choosing-tracks). |
| `--peaks <n>` | How many true-peak moments of the mix to list, 0 to 100 (default 3; 0 lists none). |
| `--detail` | Add the per-bar and per-beat detail to the JSON ([JSON schema](#json-schema)). Without it the JSON keeps summaries, small enough for an agent to read whole. The text report always shows every table. |
| `--no-bands` | Skip band ownership, about an eighth of the audition's CPU time (see [Cost](audition-measures.md#cost)). The band fields are then absent. |
| `--image-width <px>` | Widest picture in pixels (default 1600; at least 200). 0 keeps one full-resolution strip of each picture, as before. |
| `--json` | Print the report as JSON instead of text. |
| `--max-resident-pcm <size>` | Cap on the decoded audio of the score's sampler packs, such as `2GiB` (default 1 GiB). `CICADA_MAX_RESIDENT_PCM` sets it when the flag is omitted. A score over the cap is refused before any pack is read. |

The command renders the mix and the per-track stems in process with the same
engine as `cicada render` and `cicada stems`, with no tail. The same score and
options give the same report and the same image bytes. Levels and spectra
combine the left and right channels by power, so opposite-polarity stereo is
measured at its true level instead of cancelling. It needs no extra tools.
Rendering takes about as long as `cicada render`; the measures add a few
seconds for a whole song (see [Cost](audition-measures.md#cost)).

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

A track whose stem is under -80 dBFS RMS is listed as `silent` with its level
and no other measure, for example `piano  -81.4  silent: under -80 dBFS RMS,
too quiet to measure`. Such a track still plays: raise its level or its
velocities to measure it. `silent: no signal above -120 dBFS` means the stem
holds no sound at all.

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

**Energy, band owners, activity and peaks.** Four more tables follow. Their
methods, thresholds and limits are in [Audition measures](audition-measures.md).

- *Energy*: the mix's loudness, from the meter the renderer uses for loudness
  targets (ITU-R BS.1770-4): integrated loudness, loudness range and true peak
  over the range and per section, the LU under the loudest section, and one
  block per bar for the short-term loudness (the 3 s window that ends at the
  bar line). The last line names the loudest and the quietest section with
  their bars.
- *Band owners*: for each bar and seven bands (20-40, 40-80, 80-160, 160-400,
  400-1,600, 1,600-5,000 and 5,000-20,000 Hz), the track with the largest
  share of the band's energy, its share in percent and the band's level in dB
  re full-scale RMS (`kick 64* -38`). `*` marks a collision: two tracks each
  hold over 25%. `quiet` has no owner. Collisions are then counted per section
  and listed per band with the colliding pair.
- *Activity and repeats*: per track, whether it sounds in each bar (within
  30 dB of its own loudest bar), and how many bars play the same notes as the
  bar before or four bars before, from the notes the renderer played. Then the
  mix's silences: under -60 dBFS for at least 0.25 s.
- *Peaks*: the highest true peaks of the mix. For each, the signal entering
  the master chain is split into the track stems and effect returns: signed
  percentages that add up to 100, where a negative part lowers the peak.

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
image input is scaled to about 1,568 pixels). When the range does not fit,
`spectrogram.png` and `chroma.png` show it averaged in time, and the range is
also cut into pages of 32, 16, 8, 4, 2 or 1 whole bars at full resolution,
named by their bars (`spectrogram-bars-001-008.png`). A short excerpt that
fits writes the same four files as before. The JSON `pictures` list names
every file with its bar range.

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

## Choosing tracks

`--tracks bass,kick` renders as usual (every track plays, so the mix is the
song's mix) but writes only the named track stems, the two effect returns and
the two bus stems, and reads only the named stems into memory. The fields that
describe tracks then cover only those tracks: `tracks[]`, each section's
`tracks`, the energy and band shares (`energy_share_pct`, band owners),
`level_vs_loudest_db`, the swing groups, and the written and heard chords and
keys, which come from the chosen pitched tracks. The mix measures (`mix_rms_dbfs`,
`audibility_db` against the mix, loudness, silences, peak levels) are those of
the whole mix. A peak's `pre_master_sample` is still the whole signal entering
the master chain (read from the bus stems); its parts list the chosen tracks
and the returns, and `pre_master_rest_pct` holds the other tracks.
`selected_tracks` lists the chosen tracks; it is absent without the flag.
`cicada stems --tracks` writes the chosen stems and `master.wav`, each the
same as in a full set, and no return or bus stems.

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
| `tracks[].sounding_spans[]` | array | The runs of consecutive bars where the track sounds (the activity rule above), first first: `from_bar` and `to_bar`, one-based and inclusive. A gap between two runs is a drop-out of that track; the first `from_bar` is its entrance. Empty for a track that never sounds. |
| `tracks[].note_bars`, `same_as_previous_bar`, `same_as_four_bars_before`, `longest_same_run` | number | Bars where the track starts a note; of those, bars whose played notes equal the bar before, and the bar four before; the longest stretch of identical consecutive bars (0 when no bar equals the one before). |
| `sections[].integrated_lufs`, `loudness_range_lu`, `vs_loudest_lu` | number or null | The section's integrated loudness and loudness range (null under 3 s), from a meter started at the section start, and its integrated loudness minus the loudest section's. |
| `sections[].band_owners` | array | The owner of each band over the section, in band order; null where the band is quiet. Absent with `--no-bands`. |
| `selected_tracks` | array | Tracks chosen with `--tracks`, in score order. Absent without it. |
| `loudness` | object | The mix over the range: `integrated_lufs`, `loudness_range_lu` (null under 3 s), `true_peak_dbtp`, `sample_peak_dbfs`, `max_short_term_lufs`, `max_momentary_lufs`; `loudest_section`, `quietest_section` and their `_index` (from 0 in `sections[]`: names can repeat); `section_spread_lu`; and `short_term_per_bar_lufs`, the 3 s window that ends at each bar line (null in the first 3 s). Absent at a rate the meter does not support. |
| `bands` | object | Absent with `--no-bands`. `names`, `edges_hz`, the rules (`collision_share_pct` 25, `quiet_below_band_db` 30, `quiet_below_span_db` 30, `quiet_below_range_db` 40), and `collisions[]`: per band, `band` and `pairs` (`tracks`, two in score order, and `bar_count`), most frequent first. |
| `activity` | object | `sounding_threshold_db` (-30), `tail_grace_s` (0.5), `silence_below_dbfs` (-60), `silence_min_s` (0.25), and `silences[]`: `start` and `end` positions and `length_s`. |
| `peaks[]` | array | Highest true peaks first (3 by default, `--peaks`): `rank`, `at` (the position of the mix's largest sample in the peak), `true_peak_dbtp`, `sample_peak_dbfs`, `channel` (`left` or `right`); `pre_master_sample`, the signed sum there of every track stem and both effect returns, before the master chain (full scale is 1); `pre_master_parts[]` (`id`, `kind` `track` or `return`, `sample`, and `signed_share_pct`: `sample` over `pre_master_sample` in percent, negative when the part lowers the peak), parts of at least 2%, at most five; and `pre_master_rest_pct`, so the parts and the rest add up to 100. The master chain can reshape the peak after these parts ([Peaks](audition-measures.md#reading-the-tables)). |
| `pictures[]` | array | Every file written to `-o`, in write order: `file`, `kind` (`spectrogram`, `chroma`, `rhythm`, `energy`), `from_bar` and `to_bar` (one-based, inclusive; absent for `rhythm`), `width` and `height` in pixels, and `scaled` (true when the time axis was averaged to fit). Omitted without `-o`. |

`--detail` adds the detail behind those summaries: `bars[].sample_peak_dbfs`,
`sounding` and `bands`; `tracks[].bands_db`, `bar_level_db`, `sounding`,
`bar_bands_db` and `section_bands_db`; `sections[].true_peak_dbtp`,
`band_collision_bars` and `bands`; `bands.collisions[].bars`; and
`loudness.short_term_per_beat_lufs`. [Audition measures](audition-measures.md#detail-fields)
describes each.

The default report on the 181-second, twelve-track reference song
`examples/songs/anywhere-you-are.cicada` is 63.8 KB (63,790 bytes); `--detail` makes it
439 KB. The fields that were there before these measures are about 49 KB of it.

A position (`at`, `start`, `end`) has `text` (`bar.beat.sixteenth`, as an
`@` placement position), `bar`, `beat` and `sixteenth` (one-based song
positions on the song's meter; the sixteenth is counted within its beat), `tick` (absolute, 960 per quarter note) and `time_s` (seconds from
the start of the analyzed range).

## Limits

The measures are heuristics from the rendered audio, not a transcription.

The added measures' limits are in [Audition measures](audition-measures.md#limits).

Note starts come from spectral flux and miss soft notes under loud ones.
`decay20` stops at the next detected attack, so a very dense part reports a
short gap. Written chords use onsets only: a note held from an earlier bar does
not count. Energy below 125 Hz does not affect the heard chord. A sampler that plays drum hits counts as pitched.

### Stereo compatibility per bar

Each non-silent bar includes `stereo.channel_correlation` (uncentered L/R correlation, null if either channel has no energy) and `stereo.mono_vs_stereo_db` (RMS of `(L+R)/2` relative to stereo RMS, floored at -120 dB). Silent bars omit `stereo`. These are whole-band, bar-averaged indicators, not a perceptual quality score: short cancellations and frequency-specific issues can be hidden. Audition the mono mix and inspect shorter windows when needed. Measurements use the rendered audition master, not an external encoded delivery.
