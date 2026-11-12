# Cicada quick start for agents

Cicada turns a text score into audio. A command that takes `--json` prints one
JSON document on standard output, on success and on failure: decide from it and
the exit status (0 is success).

## The loop

1. Start: `cicada new demo` makes the project folder `demo` with a song in
   `main.cicada`. Run every other command inside `demo`.
2. Write: replace `main.cicada` with your score (next section).
3. Check: `cicada check --json main.cicada`. Go on when `valid` is true.
   `scores[0].revision` is what your next edit must send.
4. Render: `cicada render main.cicada -o mix.wav --loudness -14 --json`. Go on
   when `ok` and `loudness.target_met` are true.
5. Listen: `cicada audition main.cicada --json`.
6. Edit: put intents in `edit.json`, preview with
   `cicada apply main.cicada edit.json --json`, then add `--write`. Go on when
   `ok` and `written` are true; repeat from step 3.

Step 1 prints:

<!-- agent-example: new demo -->

```text
demo/main.cicada
```

<!-- agent-example-folder: demo -->

## The score

Eight bars, three tracks:

<!-- agent-example-file: main.cicada -->

```cicada
cicada 2
tempo 120
key a minor

track drums drums {}
track bass acid { cutoff = 700Hz }
track keys piano { octave = 3 level = -8dB }

pattern beat drums {
  bd: X... .... X... ..x.
  sd: .... X... .... X...
  ch: x.x. x.x. x.x. x.xx
}
pattern line { 1 . 1 . 5, . 1 . 1 . 1 . 3 . 5, . }
pattern chords { [1 3 5] - - - - - - . [6, 1 3] - - - - - - . }

scene verse { drums = beat bass = line keys = chords }
scene drop { drums = stop bass = line keys = chords }
song { verse*4 drop*2 verse*2 }

fx ceiling limiter { ceiling = -1dBTP }
master { insert = ceiling }
```

- Start every score with `cicada 2`. Outside a project, a score without it is
  edition 1, and some edits are refused.
- `track <name> <voice> { settings }`. Voices include `drums`, `acid`, `piano`,
  `fm_bass` and `poly_keys`. Values carry units.
- A pattern is 1 to 64 steps and repeats; 16 steps fill one bar. `.` is a
  rest, `-` holds the note before it, `|` only groups steps.
- `1` to `7` are degrees of the key's scale; `'` raises one an octave and `,`
  lowers it; they sound in the track's `octave` (default 2). `a1` and `c#3`
  are absolute (C4 is MIDI 60). `[1 3 5]` is a chord, except on `acid`, which
  plays it as three fast notes.
- A drum pattern has one row per lane (`bd sd ch oh cp rs lt mt ht cb cy`), all
  the same length: `x` hit, `X` accent, `.` silence.
- A scene gives each track a pattern (`stop` silences it). `song` plays scenes
  in order: `verse*4` is four bars of `verse`.
- The last two lines are a master limiter. Keep them (mistake 1).

<!-- agent-example: check --json main.cicada -->

```json
{"schema": "cicada.check.v1", "valid": true, "scores": [
  {"file": "main.cicada", "valid": true,
   "revision": "310f3c57244d24e1cf9eaf3e7185a80ff284c8a83af140b3901552fc07a15e32"}]}
```

## Hearing the result

<!-- agent-example: render main.cicada -o mix.wav --loudness -14 --json -->

```json
{"schema": "cicada.render.v1", "ok": true,
 "loudness": {"target_lufs": -14, "target_met": true}}
```

<!-- agent-example: audition main.cicada --json -->

```json
{"schema": "cicada.audition.v1", "score": {"written_key": "A minor"},
 "heard_key": [{"key": "A minor"}], "harmony_mismatches": 0, "tracks": [
  {"id": "drums", "masked": false},
  {"id": "bass", "masked": true, "level_vs_loudest_db": -27.9}]}
```

- Too quiet: `tracks[].masked` (buried in the mix), or `level_vs_loudest_db`
  far below 0: here the bass is 27.9 dB under the drums.
- Too bright or dull: `tracks[].centroid_hz`; higher is brighter.
- Silent: `tracks[].silent`, or a bar in `bars` whose `match` is `silent`.
- Out of key: `heard_key[0].key` differs from `score.written_key`, or
  `harmony_mismatches` counts bars whose `match` is `disagree`.
- Clipping: `target_met` includes a true peak at or below
  `true_peak_max_dbtp`. Without `--loudness`, `levels.clipped_count` must be 0.
- `--from <bar> --bars <n>` limits `render` and `audition` to some bars.

## The envelope

A batch is checked whole and written as one revision, or not at all:

<!-- agent-example-file: edit.json -->

```json
{"version": 1, "revision": "310f3c57244d24e1cf9eaf3e7185a80ff284c8a83af140b3901552fc07a15e32", "intents": [
  {"kind": "setparam", "entity": "param:bass.level", "value": "6dB"},
  {"kind": "setparam", "entity": "param:drums.level", "value": "-10dB"},
  {"kind": "replacedeclaration", "declaration": "pattern", "name": "line",
   "text": "pattern line { 1 . 1 . 5, . 1 . 1 . 3 . 5 . 1' . }"}
]}
```

<!-- agent-example: apply main.cicada edit.json --write --json -->

```json
{"schema": "cicada.apply.v1", "ok": true, "written": true,
 "revision_after": "157ddfb89b2ad22a8ce78f68ca2e0a8d65881efeb189390352474943261b9c2c"}
```

- `revision` is the SHA-256 of the score file in hex. Always send it: apply
  refuses (`revision_mismatch`) if the file changed since.
- Without `--write`, apply writes nothing and returns the `diff`.
- `entity` names what an intent changes: `param:<track>.<control>`,
  `pattern:<name>`, `step:<pattern>/<index>` (from 0), or on drums
  `step:<pattern>/<lane>/<index>`.
- When no intent fits, `replacedeclaration` replaces one declaration
  (`declaration` is `pattern`, `track_decl`, `scene_decl` or `song_decl`) and
  `replacetext` the whole score (`source`).

## Reading a refusal

A refusal exits non-zero; `apply` and `render` give `error` with a stable
`code` (branch on it) and a `message`.

- `check`: each of `scores[].diagnostics` has `code`, `message`,
  `start.line`, `start.column`, often a `hint`, and sometimes a `fix` whose
  `intents` you apply with `fix.revision`.
- `apply`: `error.code` is <!-- apply-error-codes -->`intent_refused`,
  `invalid_result` (the result does not check), `revision_mismatch`, `decode`,
  `read`, `write` or `usage`<!-- /apply-error-codes -->. `error.index` (from
  0), `error.kind` and, when known, `error.field` name the intent at fault, and
  `error.reason` a stable code for why. Nothing is written; resend the batch.
- `render`: `error.code` names the cause. A missed target still writes its
  best try, and `loudness.integrated_lufs` and `true_peak_dbtp` say what it
  reached: with no `remedy`, aim there or lower.

## Three mistakes to avoid

1. A loudness target with no master limiter: the song `cicada new` writes
   fails at -14 LUFS with `loudness_shortfall_at_true_peak_ceiling`. Way out:
   apply `error.remedy.intents` (the limiter) with `remedy.revision` and
   `--write`, then render again. The project then fails
   `check --profile browser`, as a browser image cannot carry a master insert
   yet; for the browser, keep the target at or below the error's
   `loudness.integrated_lufs`.
2. Reusing a revision after a write: `revision_mismatch`. Way out: send
   `revision_after` from the last apply, or run `check --json` again.
3. Guessing a name or a control: `apply` exits 1 with `intent_refused` and a
   message such as `unknown pattern "lines"` (`field` is `entity`) or
   `track keys does not expose sound parameter "cutoff"`. Way out: names from
   `symbols --json`. For a control, take a `path` from `cicada params` whose
   `voices` hold the track's engine (`fm_bass` is `fm`, `poly_keys` is
   `analog`, others their own name) and confirm it with `cicada explain`,
   which fails for a control the track lacks. `level` and `pan` fit any track.

## Look things up

- `cicada intents --json --index`: every kind and its meaning.
- `cicada intents --json <kind>`: a kind's fields and examples.
- `cicada symbols --json <score>`: the names a score defines.
- `cicada explain <score> <track>.<control>`: a control's value.
- `cicada params`: controls, with engine, unit and range.
- `cicada diagnostics --json <code>`: a code's meaning and fix.
- `cicada help --json <command>`: a command's usage and flags.
