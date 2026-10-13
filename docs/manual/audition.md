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

The command renders the mix and the per-track stems in process with the same
engine as `cicada render` and `cicada stems`, with no tail. The same score and
options give the same report and the same image bytes. It needs no extra tools.
Rendering takes about as long as `cicada render`.

## Reading the text report

**Header.** Tempo, the key you wrote, the bar range, and the swing of each
pattern that has swing. Tempo is one value for the whole score; this version
has no scene tempo changes.

**Heard key.** The key estimated from the mix, by the Krumhansl-Schmuckler
method: the mix's pitch-class energy is correlated with 24 major and minor key
profiles. The top three are listed with their correlation (1.0 is a perfect
match). If the first entry is not your written key, something other than your
harmony is loud, usually drums or a bass note that outweighs the chords.

**Harmony per bar.** Each bar has two chord names:

- *written* is the chord that best matches the pitch classes of all pitched
  notes that start in that bar (drum and audio tracks are ignored). With one or
  two pitch classes the notes are listed, joined with `+`, because they do not
  decide a chord.
- *heard* is the chord that best matches the pitch-class energy of the rendered
  mix in that bar. The templates are major, minor, `7`, `m7` and `maj7`; a
  seventh must beat its triad by a small margin to win. Energy below 125 Hz is
  ignored so kick drums and bass fundamentals do not decide the chord, and only
  spectral peaks count so broadband noise adds little.

The `match` column is `agree` when the heard chord has the same root and the
same major or minor third as the written chord, or when its notes all appear in
the written set. It is `DISAGREE` otherwise, and `silent` when the bar has no
sound. A disagreement usually means a part is inaudible or masked. `no-written-pitch`
means no pitched note starts in the bar. This is a heuristic: dense or
chromatic writing can disagree with a correct mix.

**Tracks.** One row per track, from the stems:

| Column | Meaning |
| --- | --- |
| `rms` | Level of the track's stem in dBFS (RMS). |
| `vs top` | Energy of this track in dB relative to the loudest track. |
| `share` | Share of the summed track energy, in percent. |
| `bright` | Spectral centroid in Hz: higher is brighter. |
| `attack` | Median time from the start of a note to its peak, in ms. |
| `decay20` | Median time for the level to fall 20 dB below its peak, in ms. Each note is measured only until the next detected attack on that track. A `>` means the median note did not fall 20 dB before the next attack (or 2 s), so the value is a lower bound: the note is sustained. |
| `on/bar` | Detected note starts per bar where the track plays. |
| step 1-16 | Rhythm profile (next paragraph). The percentage is the swing of the track's pattern. |

The **rhythm profile** shows where in the bar the track's attacks land. Each
cell is the mean onset strength at one sixteenth of the bar, scaled so the
strongest is full height (`█`); `·` is none. Cells are grouped by beat. For a
swung pattern the cell is read where the swing places that sixteenth, so a
shuffle shows its offbeat notes in the swung cells. Only bars where the track
plays a pattern count. A skank on the offbeats fills cells 3, 7, 11 and 15.

**Sections.** One row per song entry, or per marker in an arrangement: mix
loudness (RMS dBFS), brightness, note starts per bar over all tracks, and the
tracks that sound in it.

## Reading the images

All three images are the mix, left to right in time. A vertical line marks each
bar; every fourth bar (bars 4, 8, ...) is brighter.

- `spectrogram.png`: frequency from 40 Hz (bottom) to 12 kHz (top) on a log
  scale, color from dark (quiet) to yellow (loud) over a 70 dB range. Kick
  drums show as bright low stripes, hats as noise at the top, held notes as
  horizontal lines.
- `chroma.png`: the 12 pitch classes (C at the bottom, B at the top), one
  column per sixteenth note. A chord shows as the same few rows lit together.
  If your written notes are not lit, they are not audible.
- `rhythm.png`: one row per sounding track (in track order), 16 cells per row,
  the same data as the step columns.

## Worked example

A ska score writes C, F, G, C over four bars. A guitar and an organ play the
chords on the offbeats, a bass plays the roots, and a kick plays four on the
floor. The report says:

```text
heard key: C major (0.80), F major (0.58), A minor (0.53)

   bar  written        heard    mix dB  match
     1  C              Cmaj7     -29.8  agree
     2  F              F7        -30.2  agree
     3  G              G7        -29.4  agree
     4  C              Cmaj7     -29.8  agree

  track          rms  vs top  share  bright  attack  decay20  on/bar  step 1-16 (swing)
  guitar       -39.3    -6.4   9.1%    4362    19.2       63    4.00  ··█· ··█· ··▇· ··▆· (50%)
  piano        -37.1    -4.2  15.3%    3918    19.2       48    4.00  ··█· ··█· ··▇· ··▆· (50%)
  bass         -33.4    -0.5  35.5%     137    18.4       75    3.75  ▅··· █··· ▇··· ▅··· (50%)
  kit          -32.9     0.0  40.1%     904    17.5 >    483    4.00  ▅··· █··· █··· █··· (50%)
```

The key and chords agree with the score. The guitar and organ land on the
offbeats (cells 3, 7, 11 and 15), but they sit 4 to 6 dB under the kit and bass
and hold 9% and 15% of the track energy. A part that is much further down would
fade from the chroma image and, in time, make the bars disagree. Compare
`decay20` too: the kit's `> 483` means its median sound is still sounding when
the next one arrives, while the skank parts fall 20 dB in about 50 to 60 ms. A
horn part that reads `> 1355` next to a skank at 60 ms holds long notes over
the short ones, which is the pattern behind a score that sounds like a drone
instead of a skank.

## JSON schema

`--json` prints one object. Field names are stable within the `format` value
`cicada.audition/1`. Decibel values are rounded to 0.1, other numbers to 2 or 3
decimals. Levels below the floor read -120.

| Field | Type | Meaning |
| --- | --- | --- |
| `format` | string | Always `cicada.audition/1`. |
| `title` | string | Score title. |
| `sample_rate` | number | Render rate in Hz (48000). |
| `score.tempo_bpm` | number | Tempo. |
| `score.written_key` | string | Key from the score, for example `A minor`. |
| `score.song_bars` | number | Bars in the whole song. |
| `score.from_bar`, `score.bars` | number | First analyzed bar (one-based) and the count. |
| `score.swing[]` | array | Patterns with swing: `pattern`, `swing_percent`, `tracks`. Omitted when none. |
| `heard_key[]` | array | Top three: `key` (for example `C major`), `score` (correlation). |
| `bars[]` | array | One per analyzed bar: `bar` (one-based), `written_notes` (pitch classes), `written`, `heard`, `heard_fit` (0..1 template similarity), `mix_rms_dbfs`, `match` (`agree`, `disagree`, `no-written-pitch`, `silent`). |
| `harmony_mismatches` | number | Count of bars with `match` `disagree`. |
| `tracks[]` | array | One per track, in score order: `id`, `silent` (true under -80 dBFS; the other fields except `rms_dbfs` are then zero), `rms_dbfs`, `level_vs_loudest_db`, `energy_share_pct`, `centroid_hz`, `attack_ms`, `decay20_ms`, `decay20_lower_bound`, `onsets`, `onsets_per_bar`, `swing_percent`, `rhythm` (16 numbers, 0..1, slot 1 first). |
| `sections[]` | array | `name`, `from_bar`, `to_bar` (inclusive, one-based), `rms_dbfs`, `centroid_hz`, `onsets_per_bar`, `tracks`. |
| `images[]` | array | File names written to `-o`. Omitted without `-o`. |

## Limits

The measures are heuristics from the rendered audio, not a transcription.
Note starts come from spectral flux and miss soft notes under loud ones.
`decay20` stops at the next detected attack, so a very dense part reports a
short gap. Written chords use onsets only: a note held from an earlier bar does
not count. Energy below 125 Hz does not affect the heard chord.
