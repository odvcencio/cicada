# Agents and JSON output

An agent can run the whole loop from the command line and read every result as
JSON: discover the commands, learn the edit intents, check a score, edit it,
render it to a loudness target and listen to it. This page describes each JSON
format, with an example that the binary produced.

The `--json` formats share these rules:

- Every run with `--json` prints exactly one JSON document on standard output,
  on success and on every failure, a usage error included. Warnings,
  diagnostics as text and error messages go to standard error.
- Every document has a `schema` field that names its format and version, in
  the form `cicada.<format>.v<version>`, such as `cicada.check.v1`. A field is
  added in a compatible way; a change that would break a reader gets a new
  version. `cicada audition --json` also keeps its older `format` field
  (`cicada.audition/1`) beside `schema` (`cicada.audition.v1`).
- Keys are snake_case. Lists whose order has no meaning are sorted, and no
  document carries a timestamp. The one older format outside these rules is
  `cicada symbols --json`: a list of symbols without a schema, whose positions
  use the keys `Line` and `Column`.
- Paths follow one rule in every document: a path inside the working folder is
  relative to it, with forward slashes (`sub/song.cicada`); a path outside it
  is absolute.
- A failure carries an `error` object with a stable `code` and the `message`
  the command prints. `check` also reports `valid` false, and its diagnostics.
- The exit status is the same as without `--json`:

  | Command | Success | Failure | Usage error or unknown name |
  | --- | --- | --- | --- |
  | `check`, `apply`, `intents`, `diagnostics`, `audition` | 0 | 1 | 1 |
  | `render`, `stems` | 0 | 1 | 2 |
  | `help`, `agents` | 0 | — | 2 |

Usage errors and unknown names have the codes `usage`, `unknown_kind`
(`intents`), `unknown_code` (`diagnostics`) and `unknown_command` (`help`).

`cicada audition --json` is described in [Audition](audition.md), and
`cicada verify-wav --report` in [Exporting](exporting.md). The loudness fields
use the names of `verify-wav --report` (`integrated_lufs`, `true_peak_dbtp`,
`clipped_count`); Studio's export report calls the integrated loudness
`achieved_lufs`.

## Reading the formats

A client that reads these documents should:

1. Check `schema` first and refuse a version it does not know: compare the
   whole string, such as `cicada.check.v1`.
2. Ignore fields it does not know.
3. Rely on this: within one version, fields are only added. A field's meaning
   or type changes only with a new version.

Every example below is checked: `make test-agent-surface` writes the files this
page shows into an empty folder, runs the commands in page order with the built
binary, and fails if a value shown here differs from the output. Long outputs
are shortened: an object may leave out fields and a list may stop early, but
nothing shown is changed.

## The example score

The examples edit this score, saved as `song.cicada`:

<!-- agent-example-file: song.cicada -->

```cicada
cicada 2
title "Agent example"
tempo 120
key a minor

track bass acid { cutoff = 620Hz }
track drums drums {}

pattern riff { a1 . c2 . e2 . a1 . a1 . c2 . g2 . e2 . }
pattern beat drums {
  bd: x... x... x... x...
  ch: ..x. ..x. ..x. ..x.
}

scene main { bass = riff drums = beat }
song { main*4 }
```

## agents --json

`cicada agents` prints the [quick start for agents](agent-quickstart.md): the
one page an agent that has never seen Cicada reads first. The binary carries
it, so it needs no repository checkout, and `cicada new` writes it into each
new project as `AGENTS.md`. `cicada agents reference` prints the
[core reference](agent-reference.md): the quick start, the language, sound and
formats in brief, and one line for every command, edit intent, diagnostic code
and effect route.

`cicada agents --json` gives the page asked for as `guide`, its name as `page`
(`quickstart` or `reference`) and its size in bytes as `guide_bytes`; in
`commands`, the commands the quick start offers for looking up the rest, each
with the `summary` of what it answers (placeholders such as `<kind>` are in
angle brackets); and in `pages`, each page the binary carries with its `name`,
`title` and `bytes`. The agent task suite reads this document and runs every
command in it.

## help --json

`cicada help --json` lists every command, sorted by name, with its one-line
summary, its usage lines and its flags. `argument` names the value a flag takes
and is absent for a switch. `cicada help <command> --json` gives one command.

<!-- agent-example: help --json -->

```json
{
  "schema": "cicada.help.v1",
  "usage": "cicada <command> [arguments]",
  "commands": [
    {
      "name": "agents",
      "summary": "print the quick start for AI agents"
    },
    {
      "name": "apply",
      "summary": "apply typed edit intents to a score",
      "usage": [
        "cicada apply <score.cicada> <intents.json> [--write] [--json] [--author <name>] [--session <id>]"
      ],
      "flags": [
        {
          "name": "--write",
          "description": "write the validated edits (default: print a diff only)"
        }
      ]
    }
  ]
}
```

## intents --json

`cicada intents` lists every edit intent that `cicada apply` accepts, with its
meaning; `cicada intents <kind>` shows one with its fields and an example. The
catalog is generated from the intent registry, so it lists every kind the
binary has.

`cicada intents --json` writes the whole catalog, and `cicada intents --json
<kind>` one kind. `envelope` describes the object that carries the intents.
For each intent:

- `meaning` is one line; `notes` adds what else the intent's documentation
  says.
- `reasons` lists the codes the intent's refusals can carry, the `reason` of
  an `apply --json` error (below). A test derives the list from the code, so
  an agent can match on it.
- Each field has its JSON `name`, its `type` (`string`, `integer`, `number`,
  `boolean`, `entity`, `object`, `array` with `items`, or `any`), whether it is
  `required`, and its `unit`, allowed `values` and `meaning` where the code
  knows them. An `entity` is a string such as `pattern:riff`,
  `step:riff/3` (steps count from 0), `step:beat/bd/0`, `param:bass.cutoff`,
  `placement:intro`, `song:0` or `setting:main/bass.cutoff`.
- `required` is true when `cicada apply` refuses the intent without the
  field. A field that is not required may be left out: it is then read as its
  zero value (0, "", false or empty), which the intent takes as the default
  its meaning states.
- `required_when` names a condition under which an optional field becomes
  required: another field of the intent has one of `values`, or, with
  `present`, is given at all. In the example below `setstep` needs
  `velocity` when its `mode` is `hit`.
- `one_of` lists groups of fields of which the intent needs at least one,
  always or only while its `when` holds. Field names in `one_of`, and in a
  condition's `field`, are dotted paths from the intent's top level; a path
  through a list, such as `notes.pitch`, names a field of each element.
- `values` is the list the intent's handler checks, so a string outside it is
  refused. A field the catalog does not list is refused too.
- `selects` marks a field that chooses the intent's mode or operation, such
  as `mode` or `op`.
- `examples` are payloads that `cicada apply` accepts: one for each value of
  every field that `selects`, the first also given as `example`. Examples name
  the entities of a small test score (track `bass`, pattern `riff`, scene
  `main`); use the names of your own score.

A test applies every example, removes each field it sets and checks that the
intent is refused exactly when the catalog says the field is required (or
required by a `required_when` or `one_of` the example meets), and replaces each
listed value with one outside the list.

<!-- agent-example: intents --json setstep -->

```json
{
  "schema": "cicada.intents.v1",
  "envelope_version": 1,
  "envelope": [
    {
      "name": "version"
    }
  ],
  "intents": [
    {
      "kind": "setstep",
      "fields": [
        {
          "name": "entity"
        },
        {
          "name": "mode",
          "type": "string",
          "selects": true,
          "required": true,
          "values": [
            "note",
            "hit",
            "tie",
            "rest"
          ]
        },
        {
          "name": "pitch"
        },
        {
          "name": "accent"
        },
        {
          "name": "slide"
        },
        {
          "name": "ratchet"
        },
        {
          "name": "chance"
        },
        {
          "name": "velocity",
          "type": "string",
          "required": false,
          "required_when": {
            "field": "mode",
            "values": [
              "hit"
            ]
          }
        }
      ]
    }
  ]
}
```

`editnotes` names each note in `notes` by its pitch or, in a drum pattern, its
lane, and its `velocity` operation takes a new value or a change:

<!-- agent-example: intents --json editnotes -->

```json
{
  "schema": "cicada.intents.v1",
  "envelope_version": 1,
  "envelope": [
    {
      "name": "version"
    }
  ],
  "intents": [
    {
      "kind": "editnotes",
      "one_of": [
        {
          "fields": [
            "notes.pitch",
            "notes.lane"
          ]
        },
        {
          "fields": [
            "velocity",
            "dv"
          ],
          "when": {
            "field": "op",
            "values": [
              "velocity"
            ]
          }
        }
      ]
    }
  ]
}
```

<!-- agent-example: intents --json setpatternsettings -->

```json
{
  "schema": "cicada.intents.v1",
  "envelope_version": 1,
  "envelope": [
    {
      "name": "version",
      "type": "integer",
      "required": true,
      "values": [
        "1"
      ],
      "meaning": "envelope format version"
    }
  ],
  "intents": [
    {
      "kind": "setpatternsettings",
      "meaning": "changes a pattern's swing, gate and transpose without changing its note text",
      "notes": "A drum pattern keeps transpose 0.",
      "fields": [
        {
          "name": "entity",
          "type": "entity",
          "required": true,
          "meaning": "the pattern, as pattern:<name>"
        },
        {
          "name": "swing100",
          "type": "integer",
          "required": true,
          "unit": "0.01 percent",
          "meaning": "swing in hundredths of a percent, 5000–7500 (50% to 75%)"
        },
        {
          "name": "gate",
          "type": "integer",
          "required": true,
          "unit": "percent",
          "meaning": "gate length, 10–100"
        },
        {
          "name": "transpose",
          "type": "integer",
          "required": false,
          "unit": "semitones",
          "meaning": "−24 to +24"
        }
      ],
      "example": {
        "kind": "setpatternsettings",
        "entity": "pattern:riff",
        "swing100": 5600,
        "gate": 60,
        "transpose": 0
      }
    }
  ]
}
```

`cicada intents --json --index` lists only each `kind` and its one-line
`meaning`, in less than a tenth of the bytes of the whole catalog, so an agent
can choose a kind before it asks for that kind in full. It is a format of its
own, `cicada.intents_index.v1`, with `index` in place of `envelope` and
`intents`. `--index` needs `--json`: `cicada intents` alone already lists each
kind with its meaning.

<!-- agent-example: intents --json --index -->

```json
{
  "schema": "cicada.intents_index.v1",
  "envelope_version": 1,
  "index": [
    {
      "kind": "addaudiotrack",
      "meaning": "declares a new, empty audio track in an edition-2 score"
    }
  ]
}
```

## diagnostics --json

`cicada diagnostics` lists every diagnostic code; `cicada diagnostics <code>`
shows one with its fix. The [diagnostic code reference](diagnostics.md) is
generated from the same catalog. `severities` holds `error`, `warning` or both.
`scopes` says where the code is reported: `score` (checking a score), `command`
(a command's error), `studio` (Studio and browser responses) or `edit` (a
refused intent). `CICADA-NOTES` also lists the `reasons` its refusals carry.

<!-- agent-example: diagnostics --json slide-rest -->

```json
{
  "schema": "cicada.diagnostics.v1",
  "diagnostics": [
    {
      "code": "CICADA-SLIDE-REST",
      "severities": [
        "warning"
      ],
      "scopes": [
        "score"
      ],
      "meaning": "a slide leads into a rest, so it has no effect",
      "fix": "slide into a note, or remove the slide"
    }
  ]
}
```

## check --json

`cicada check --json` runs the checks `cicada check` runs and reports, for each
score, whether it is valid, its `revision` and its diagnostics. Without a score
it checks the project. When `cicada.mod` lists the project's `source` files,
the project is one score: `scores` has one entry, the entry score, whose
diagnostics cover every listed file (each names its `file`) and whose
`revision` is that of the entry file alone. Otherwise each score in the project
folder is checked on its own and `scores` lists every one. `revision` is the
SHA-256 of the score file's bytes in hex, the form `cicada apply` compares with
an envelope's `revision`: put it in the next envelope for that score. It is
absent only when the score cannot be read.

Each diagnostic has its `code`, `severity`, `message` and `file`. `start` is
the position the checker reports: `line` and `column` count from 1, columns
count Unicode characters, and `offset` is the byte offset from the start of the
file. The checker reports a point, so `end` marks the end of the character at
`start`, as the language server does. `related` points at the first declaration
of a duplicate. `hint` is the advice `cicada check` prints. With
`--profile browser`, `browser_profile` lists the voices each track reserves
against the portable limits.

`fix`, where the checker knows one, is a repair: `intents` for `cicada apply`,
or a `command` to run. A fix is offered only when it is proven on the score
as it is: applying it alone removes its diagnostic and adds no other, even
when other errors remain. It holds only at `revision`, the score's revision
it was computed for: put that revision in the envelope, and check again after
applying. `cicada apply` refuses a batch that leaves the score invalid, so
when several errors have fixes, apply the fixes of one revision together in
one batch.

<!-- agent-example: check --json song.cicada -->

```json
{
  "schema": "cicada.check.v1",
  "profile": "desktop",
  "valid": true,
  "errors": 0,
  "warnings": 0,
  "scores": [
    {
      "file": "song.cicada",
      "revision": "a62c420996d8d6468a2dd2b27ef40cd8d3ea867e5c6a53b9746d4f841dedeca2",
      "valid": true,
      "diagnostics": []
    }
  ]
}
```

A placement of a step pattern must start on a step. In `snap.cicada` it starts
at tick 100:

<!-- agent-example-file: snap.cicada -->

```cicada-invalid CICADA-ARRANGEMENT
cicada 2
tempo 120
track bass acid {}
pattern pulse { a1 . . . e2 . . . }
arrange {
  place intro bass pulse { at = 100ticks length = 1bars }
}
```

The check fails and offers the intent that moves the placement to the nearest
step:

<!-- agent-example: check --json snap.cicada exit=1 -->

```json
{
  "schema": "cicada.check.v1",
  "profile": "desktop",
  "valid": false,
  "errors": 1,
  "warnings": 0,
  "scores": [
    {
      "file": "snap.cicada",
      "revision": "bb00827b7a0ef325edda51ba13ac8910483dd63feb59db92701fd5c7875c6852",
      "valid": false,
      "diagnostics": [
        {
          "code": "CICADA-ARRANGEMENT",
          "severity": "error",
          "message": "step pattern placement must start on a step",
          "file": "snap.cicada",
          "start": {
            "line": 6,
            "column": 3,
            "offset": 86
          },
          "end": {
            "line": 6,
            "column": 4,
            "offset": 87
          },
          "fix": {
            "summary": "move placement intro to the nearest step, tick 0",
            "intents": [
              {
                "attick": 0,
                "entity": "placement:intro",
                "kind": "moveclip"
              }
            ],
            "revision": "bb00827b7a0ef325edda51ba13ac8910483dd63feb59db92701fd5c7875c6852"
          }
        }
      ]
    }
  ]
}
```

## song --json

`cicada song --json` lists every score of the project with its `revision`.
`cicada song --json <score>` gives the score's `revision` and, as `--include`
asks (a comma-separated list), its `source` text (the default), the names it
defines as `symbols` (each `kind`, `name`, `line` and `column`), and its
compiled meaning as semantic JSON in `structure`. A score that cannot be read
is refused with `unreadable_score`, and one that does not compile, when
`symbols` or `structure` is asked for, with `invalid_score`.

<!-- agent-example: song --json --include symbols song.cicada -->

```json
{
  "schema": "cicada.song.v1",
  "file": "song.cicada",
  "revision": "a62c420996d8d6468a2dd2b27ef40cd8d3ea867e5c6a53b9746d4f841dedeca2",
  "symbols": [
    {
      "kind": "track",
      "name": "bass",
      "line": 6,
      "column": 7
    }
  ]
}
```

`cicada lib list --json` lists the libraries a score can import, each with its
`path` and `kind` (`std`, `project` or `user`), in `libraries`;
`cicada lib show <path> --json` gives one in `library`: its `kind`, `root`,
`sha256`, `manifest` text, `declarations` and `assets` (schema
`cicada.library.v1`; an unknown library is refused with `unknown_library`).
`cicada explain <construct> --json` and `cicada explain <construct>.<field>
--json` add `"schema": "cicada.explain.v1"` to the construct and its fields,
or to the one field.

## apply --json

`cicada apply <score> <intents.json> --json` reports what the batch did:
`revision_before` and `revision_after` (the SHA-256 of the score's bytes),
whether the score `changed` and was `written`, the unified `diff`, any other
`files` the batch changes, and for each intent its `index` (from 0), `kind` and
the `label` the edit history records. The batch is one revision, so the diff
covers all of it. Without `--write` nothing is written.

When the batch fails, `error.code` is one of:

<!-- apply-error-codes -->
- `usage`: the arguments are wrong.
- `read`: the score or the intents file cannot be read.
- `decode`: the JSON does not match the envelope or an intent's kind.
- `revision_mismatch`: the envelope's `revision` is not the score's.
- `intent_refused`: an intent is refused.
- `invalid_result`: the edited score does not compile.
- `write`: the edit could not be written.
<!-- /apply-error-codes -->

`index`, `kind` and, where the code knows it, `field` name the intent at
fault. When an intent states why it refused, `reason` carries a stable code
from the intent's `reasons` list in `cicada intents --json`. Where they
apply, `uses` lists what still refers to the entity, `file` names another
source file of the project that holds what the edit would have to change
(an edit writes only the score it is given), and `range` gives the
`parameter`, the `value` given, `min`, `max` and `unit` of a value outside
its range (reason `value_out_of_range`). A refused `editnotes` batch also carries
`diagnostic` `CICADA-NOTES`, its `reason` and the `note` (see
[Editors](editors.md)). The text error names
the intent the same way: `intents[1].entity (togglestep): unknown pattern
"rif"`.

<!-- agent-example-file: bad.json -->

```json
{"version": 1, "intents": [
  {"kind": "setparam", "entity": "param:bass.cutoff", "value": "900Hz"},
  {"kind": "togglestep", "entity": "step:rif/3"}
]}
```

<!-- agent-example: apply song.cicada bad.json --json exit=1 -->

```json
{
  "schema": "cicada.apply.v1",
  "score": "song.cicada",
  "ok": false,
  "written": false,
  "dry_run": false,
  "changed": false,
  "revision_before": "a62c420996d8d6468a2dd2b27ef40cd8d3ea867e5c6a53b9746d4f841dedeca2",
  "intents": [
    {
      "index": 0,
      "kind": "setparam"
    },
    {
      "index": 1,
      "kind": "togglestep"
    }
  ],
  "diff": "",
  "error": {
    "code": "intent_refused",
    "message": "unknown pattern \"rif\"",
    "index": 1,
    "kind": "togglestep",
    "field": "entity"
  }
}
```

The same batch with the pattern's settings instead applies and writes:

<!-- agent-example-file: edit.json -->

```json
{"version": 1, "intents": [
  {"kind": "setparam", "entity": "param:bass.cutoff", "value": "900Hz"},
  {"kind": "setpatternsettings", "entity": "pattern:riff", "swing100": 5600, "gate": 60, "transpose": 0}
]}
```

<!-- agent-example: apply song.cicada edit.json --write --json -->

```json
{
  "schema": "cicada.apply.v1",
  "score": "song.cicada",
  "ok": true,
  "written": true,
  "dry_run": false,
  "changed": true,
  "revision_before": "a62c420996d8d6468a2dd2b27ef40cd8d3ea867e5c6a53b9746d4f841dedeca2",
  "revision_after": "b4e6d37792e21a8f001b0f4e44401e14157070748178f0cea330bf4627f2b6c7",
  "intents": [
    {
      "index": 0,
      "kind": "setparam",
      "label": "Set bass.cutoff"
    },
    {
      "index": 1,
      "kind": "setpatternsettings",
      "label": "Pattern · riff timing and transpose changed"
    }
  ],
  "diff": "--- a/song.cicada\n+++ b/song.cicada\n@@ -3,10 +3,10 @@\n tempo 120\n key a minor\n \n-track bass acid { cutoff = 620Hz }\n+track bass acid { cutoff = 900Hz }\n track drums drums {}\n \n-pattern riff { a1 . c2 . e2 . a1 . a1 . c2 . g2 . e2 . }\n+pattern riff { swing = 56% gate = 60% transpose = 0  a1 . c2 . e2 . a1 . a1 . c2 . g2 . e2 . }\n pattern beat drums {\n   bd: x... x... x... x...\n   ch: ..x. ..x. ..x. ..x.\n"
}
```

## render --json

`cicada render ... --json` describes what it rendered: each file in `outputs`
as its WAV header gives it (`sample_rate`, `bit_depth`, `encoding` `pcm` or
`float`, `channels`, `frames` and `duration_seconds`), and the bar range
(`from_bar`, `bars`). A render without a loudness target adds `levels`: the
pre-limiter peak and overs, the output peak, and the counts of samples at the
ceiling and clipped (`ceiling_count`, `clipped_count`, as `verify-wav
--report` names them), with peaks as linear full-scale values. `cicada stems ...
--json` lists every stem file, with `role` `stem:<file name>`.

With a loudness target (`--loudness`, or `--export` with a profile that sets
one), `loudness` gives the target and tolerance, the measured
`integrated_lufs`, `true_peak_dbtp` and `sample_peak_dbfs`, the
`applied_gain_db`, whether `target_met`, the passes, where the gain went
(`gain_stage`), the master limiter's gain reduction and the DC correction. A
measurement is `null` when there is none.

The search runs up to six passes and synthesizes the song only once.
`syntheses`, `replays` and `repeats` count how the `passes` got their audio,
and add up to `passes`: a synthesis renders the song in full; a replay runs
only the master stage again on the first pass's stored mix; a repeat is not
rendered, because its gain differs from an earlier pass's by less than the
renderer can show, so its audio and measurement are that pass's. `pass_log`
has one entry per pass, in order: the `gain_db` it applied, its measured
`integrated_lufs` and `true_peak_dbtp`, and `render`, which is `synthesized`,
`replayed` or `repeated` (with `same_as`, the number of the pass it repeats).
The audio and every other number are the same as when each pass synthesizes
the song. With the environment variable `CICADA_RENDER_NO_REUSE=1` every pass
does: `syntheses` equals `passes` and nothing is replayed or repeated. Use it
to check whether reuse caused a difference you see.

<!-- agent-example: render song.cicada -o song.wav --bars 2 --loudness -14 --json -->

```json
{
  "schema": "cicada.render.v1",
  "command": "render",
  "score": "song.cicada",
  "ok": true,
  "from_bar": 1,
  "bars": 2,
  "outputs": [
    {
      "path": "song.wav",
      "role": "mix",
      "sample_rate": 48000,
      "bit_depth": 24,
      "encoding": "pcm",
      "channels": 2,
      "frames": 336000,
      "duration_seconds": 7
    }
  ],
  "loudness": {
    "target_lufs": -14,
    "tolerance_lu": 0.5,
    "true_peak_max_dbtp": -1,
    "integrated_lufs": -13.999999856123383,
    "true_peak_dbtp": -5.974329358809015,
    "applied_gain_db": 10.963850844317331,
    "target_met": true,
    "passes": 2,
    "syntheses": 1,
    "replays": 1,
    "repeats": 0,
    "pass_log": [
      {
        "gain_db": 0,
        "integrated_lufs": -24.96385084431733,
        "true_peak_dbtp": -16.938175787527175,
        "render": "synthesized"
      },
      {
        "gain_db": 10.963850844317331,
        "integrated_lufs": -13.999999856123383,
        "true_peak_dbtp": -5.974329358809015,
        "render": "replayed"
      }
    ],
    "gain_stage": "post_chain"
  }
}
```

A render that fails carries `error`, with a stable `code`, the `message` the
command prints and, when the documentation gives a fix for that cause, a
`remedy`. The codes are:

<!-- render-error-codes -->
- `usage`: the arguments are wrong.
- `unreadable_score`: the score cannot be read.
- `invalid_score`: the score has errors; `diagnostics` lists them.
- `export_settings`: the `--export` profile is unknown or cannot be applied.
- `output_exists`: the stems folder already exists.
- `sampler_memory`: the sampler packs exceed the resident PCM cap.
- `render_failed`: another render or write error; the message says which.
- `true_peak_and_dc_unmet`: no pass met the true-peak ceiling and the DC limit.
- `loudness_unmeasurable`: the render has no measurable loudness.
- `true_peak_over_ceiling`: the best pass is still above the true-peak ceiling.
- `limiter_reduction_over_maximum`: the master limiter would reduce more than 6 dB.
- `loudness_shortfall_at_limiter_maximum`: the target needs more than the master limiter's 6 dB maximum.
- `loudness_shortfall_at_input_gain_cap`: the target needs more than +12 dB ahead of the master limiter.
- `loudness_shortfall_at_true_peak_ceiling`: without a master limiter, the gain stops at the true-peak ceiling.
- `loudness_target_missed`: the best pass is outside the tolerance for another reason.
<!-- /render-error-codes -->

After a shortfall the best pass is still written and listed in `outputs`.

`remedy` has a `summary`, the `file` to change, the file's `revision` when the
render read it (the form `cicada apply` uses), and the `score_text` to put in
it: when `replaces` is set, `score_text` replaces that text, which occurs
exactly once in the file at that revision (the declaration it changes);
otherwise it goes at the end of the file. The remedy holds only while the file
is at `revision`: after any other change, render again. `intents`,
when present, makes the same change through `cicada apply`; it is absent when
no intent can. Today only `loudness_shortfall_at_true_peak_ceiling` has a
remedy: a limiter at the export's true-peak ceiling as the last master
insert. Its `intents` hold the `addeffect` intent that adds that limiter; put
them in an envelope with the remedy's `revision` and run `cicada apply
<score> <envelope> --write`. `score_text` describes the same change as text.
In an edition-1 score the intent carries `confirmupgrade`, because a mixer
write upgrades the score to edition 2, and `summary` says so; `score_text`
keeps edition 1. When the master chain already holds 16 inserts, the most it
takes, the remedy has neither `score_text` nor `intents`: `summary` says to
remove a master insert first.

This score has no master limiter, so its export to −9 LUFS stops at the
true-peak ceiling:

<!-- agent-example-file: loud.cicada -->

```cicada
cicada 2
tempo 138
key a minor
seed 4242
track bass acid { cutoff = 620Hz reso = 0.62 envmod = 0.7 decay = 380ms accent = 0.8 drive = 0.25 filter = diode }
track drums drums { bd_tune = 55Hz bd_decay = 400ms ch_decay = 60ms }
pattern bass-a { 1^ . 1~ 5 . 1 7, . | 1^ . 1'~ 1 5^ 3 1 . }
pattern beat-a drums {
  bd: X... .... X... ....
  sd: .... X... .... X...
}
scene main { bass = bass-a drums = beat-a }
song { main*4 }
fx tone eq { type = highpass frequency = 25Hz q = 0.707 }
master { level = 6dB insert = tone }
export loud { loudness = -9LUFS true_peak = -1dBTP normalize = off }
```

<!-- agent-example: render loud.cicada -o loud.wav --bars 4 --export loud --json exit=1 -->

```json
{
  "schema": "cicada.render.v1",
  "command": "render",
  "score": "loud.cicada",
  "ok": false,
  "outputs": [
    {
      "path": "loud.wav",
      "frames": 477914
    }
  ],
  "loudness": {
    "target_lufs": -9,
    "integrated_lufs": -10.862548607135627,
    "true_peak_dbtp": -1.050000030698375,
    "target_met": false,
    "gain_stage": "post_chain"
  },
  "error": {
    "code": "loudness_shortfall_at_true_peak_ceiling",
    "message": "loudness target shortfall 1.86 LU: achieved -10.86 LUFS at true-peak ceiling -1.05 dBTP (target -9.00 LUFS)",
    "remedy": {
      "summary": "add a limiter with ceiling = -1dBTP, the export's true-peak ceiling, as the last master insert, so the gain goes in ahead of it and it holds that ceiling (docs/audio/mix-chain.md, Export loudness targets): cicada apply with an addeffect intent (effectkind limiter, inserton master, settings ceiling), or the same in the score text",
      "file": "loud.cicada",
      "revision": "9a1336e210d4e6fddfde19678baf455fd45a31dbb38177789e133de2ac384d5f",
      "replaces": "master { level = 6dB insert = tone }",
      "score_text": "fx ceiling limiter { ceiling = -1dBTP }\nmaster { level = 6dB insert = tone -> ceiling }",
      "intents": [
        {
          "effectkind": "limiter",
          "inserton": "master",
          "kind": "addeffect",
          "name": "ceiling",
          "settings": {
            "ceiling": "-1dBTP"
          }
        }
      ]
    }
  }
}
```

The remedy is the one [the mix chain](../audio/mix-chain.md#export-loudness-targets)
describes. With its `score_text` in place of the master declaration, the same
export meets its target:

<!-- agent-example-file: loud-limited.cicada -->

```cicada
cicada 2
tempo 138
key a minor
seed 4242
track bass acid { cutoff = 620Hz reso = 0.62 envmod = 0.7 decay = 380ms accent = 0.8 drive = 0.25 filter = diode }
track drums drums { bd_tune = 55Hz bd_decay = 400ms ch_decay = 60ms }
pattern bass-a { 1^ . 1~ 5 . 1 7, . | 1^ . 1'~ 1 5^ 3 1 . }
pattern beat-a drums {
  bd: X... .... X... ....
  sd: .... X... .... X...
}
scene main { bass = bass-a drums = beat-a }
song { main*4 }
fx tone eq { type = highpass frequency = 25Hz q = 0.707 }
fx ceiling limiter { ceiling = -1dBTP }
master { level = 6dB insert = tone -> ceiling }
export loud { loudness = -9LUFS true_peak = -1dBTP normalize = off }
```

<!-- agent-example: render loud-limited.cicada -o loud.wav --bars 4 --export loud --json -->

```json
{
  "schema": "cicada.render.v1",
  "command": "render",
  "score": "loud-limited.cicada",
  "ok": true,
  "outputs": [
    {
      "path": "loud.wav"
    }
  ],
  "loudness": {
    "target_lufs": -9,
    "integrated_lufs": -9.28794649255763,
    "true_peak_dbtp": -1.7761707377820928,
    "applied_gain_db": 7.771192867260213,
    "target_met": true,
    "gain_stage": "master_input",
    "master_limiter_reduction_db": 3.3003585823440833
  }
}
```

## The MCP server

`cicada mcp [--root <dir>]` serves the read-only tools to an agent host that
speaks the Model Context Protocol (MCP). The host starts it as a subprocess and
talks to it on standard input and output, one JSON-RPC message per line, for
example with this entry in the host's server list:

```text
{"command": "cicada", "args": ["mcp", "--root", "my-song"]}
```

`--root` (default: the working folder) is the one folder the tools read, and
paths in every document are relative to it. The server answers protocol
revision 2026-07-28, which is stateless: each request carries `_meta` with
`io.modelcontextprotocol/protocolVersion` and
`io.modelcontextprotocol/clientCapabilities`, and `server/discover` lists the
versions the server supports. An `initialize` request selects 2025-11-25 or
2025-06-18 instead, for the rest of the stream. Another version is refused with
error -32022, which lists the supported ones.

| Tool | Arguments | Returns, as the command line prints it |
| --- | --- | --- |
| `song` | `score`, `include` | `cicada.song.v1` (`song --json`) |
| `check` | `score`, `profile` | `cicada.check.v1` (`check --json`) |
| `intents` | `kinds`, up to 8 | `cicada.intents_index.v1` without kinds (`intents --json --index`), `cicada.intents.v1` with them |
| `explain` | `diagnostic` or `construct` | `cicada.diagnostics.v1` (`diagnostics --json`) or `cicada.explain.v1` (`explain --json`) |
| `library` | `path` | `cicada.library.v1` (`lib list --json`, `lib show --json`) |

Every argument is optional except where the table says "or". A tool returns
its document in `structuredContent` and the same JSON as the one text block.
`isError` is true when the command would exit with an error, and the
document's `error.code` says why, as on the command line. A score path that is
absolute, contains `..`, leaves the root through a symbolic link or does not
end in `.cicada` is refused with `path_outside_root`; a field a tool does not
take, with `usage`. An unknown tool, or arguments that are not an object, is a
JSON-RPC error (-32602). The server writes no file, opens no connection, and
exits when its input closes. Editing, rendering and listening (`apply`,
`render`, `audition`) are not served yet: run those commands directly.

## The agent task suite

`make test-agent-surface` builds `build/cicada` and runs the agent task suite
in `internal/agentsuite`. It starts as a new agent does: it reads the quick
start from `cicada agents`, runs its loop with its score and its batch, and
makes its three mistakes, each of which must be refused as the quick start
says; every command, field and refusal it relies on must be named in the quick
start. Then it copies `examples/first-acid.cicada` into an empty folder and
drives the binary the way an agent does, reading only JSON: `help --json`,
`intents --json`, `symbols --json`, `check --json`, an `apply` of two intents
built from the catalog's examples, `render --json` to −14 LUFS and
`audition --json`. It then drives `cicada mcp` through the protocol in both
eras and requires every tool to return the command line's own documents. It
also runs the examples on this page and on the quick start. CI runs it on every pull request; it must finish in under two minutes.
