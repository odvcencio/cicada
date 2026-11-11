# Editors and notation tools

The Cicada command-line tool validates source, formats it, and runs the
language server used by editors.

## Language server

Start the LSP over standard input and output:

```sh
cicada lsp
```

Configure your editor to launch that command for files ending in `.cicada`.
The server publishes syntax and semantic diagnostics as you edit. It also
supports pitch, chance, and drum-velocity hovers; pattern and song inlays;
semantic coloring; definition lookup; and scoped rename for instrument
parameters and `let` bindings.

The **Apply Cicada notation fixes** quick fix uses the same meaning-preserving
migration as `cicada fix`. If it removes a legacy edition header, it can add
the required `cicada.mod` manifest through a workspace edit.

Two more code actions come from the edit service:

- **Snap placement to the step grid** (quick fix) answers the diagnostic
  "step pattern placement must start on a step" by moving the placement to the
  nearest step. It changes only the `at` value.
- **Convert song to arrange** (refactor) appears when the cursor is in the
  `song` block and a test render shows that the converted score sounds the
  same. The server renders the song and the converted score once per document
  revision, so the action can take a moment on a long song. A song whose scenes
  carry settings, or one that renders differently as placements, gets no action.

## VS Code

The public repository includes a VS Code extension. From the repository root,
build the CLI and the Studio workstation, install the extension's dependencies,
and package it:

```sh
make build
cd editors/vscode
npm install
npx @vscode/vsce package
code --install-extension cicada-0.1.0.vsix
```

The extension runs `cicada studio`, which needs what `make build` writes to
`build/`: `cicada`, `cicada-workstation`, and the `workstation` folder. Set the
extension's `cicada.serverPath` setting to the absolute path of `build/cicada`,
or move those three together into a folder on `PATH`. The extension starts
Studio next to the open score with **Cicada: Open Studio Beside Score**. Its
setup and WSL notes are in the
[extension README](../../editors/vscode/README.md). The repository does not
currently publish this extension through the VS Code Marketplace.

## Formatting and migration

Use `cicada fmt score.cicada` to print canonical source, `cicada fmt -w
score.cicada` to write one file, and `cicada fmt --check` to check a project
without changing it. From a project directory, the command covers score files
under the nearest manifest; nested projects are handled separately.

Use `cicada fix score.cicada --check` to see whether legacy source spellings
need rewriting, even when the project manifest already selects edition 2. Omit
`--check` to apply the rewrite. If other edition-1 scores share the project
folder, `fix` lists them and refuses a single-file change; run
`cicada fix --all` from that folder to migrate them together. `fix` preserves
comments and checks that the compiled project keeps the same meaning. See the
[migration reference](../spec/semantic-model.md#cicada-fix-and-migrations)
for the complete list of rewrites.

## Applying intents from the command line

Save a version-1 envelope as `intents.json`:

```json
{
  "version": 1,
  "intents": [
    {"kind": "setpatternsettings", "entity": "pattern:pulse", "swing100": 5500, "gate": 55, "transpose": 0}
  ]
}
```

Validate the edits and print a unified diff:

```sh
cicada apply main.cicada intents.json
```

Add `--write` to save the validated changes. Writes check the source revision
again at the atomic exchange, so a concurrent save is preserved. Auxiliary
changes, including a confirmed edition upgrade in `cicada.mod`, appear in the
diff and are saved with the edit. Unchanged edits write nothing.

`duplicatepattern` takes `entity: "pattern:name"`, a new `name`, and an optional
`track`. The track resolves relative notes in the sound's register. Supplying it,
or copying from a secondary source or library, creates a resolved local pattern
in the entry score and requires an unchanged-song render check. Original
definitions, assignments and library pins remain intact. The copy has no
explicit slot; Studio supplies its full project receipt and opens the copy with
the chosen sound and scene context.

Tracks, scenes and patterns have structural intents: `addtrack`,
`deletetrack`, `settrackvoice`, `addscene`, `duplicatescene`, `deletescene`,
`deletepattern` and `rename`. Each edits every source file of the project
that names the entity (libraries apart) in one revision-checked change, one
Undo step. A delete is refused while something else still names the entity,
and the refusal's `uses` lists each place with its file and line. With
`cascade`, `deletescene` also removes the scene's song entries, and
`deletepattern` sets the pattern's scene bindings to `off` and removes its
placements. A removal takes a comment that follows the declaration on its
line. It also takes the comment lines directly above it (no blank line
between), unless they open the file or the declaration is directly followed
by another declaration of its kind: then they head a group and stay. A
comment set apart by a blank line always stays. `settrackvoice` changes nothing silently: a track setting the new
voice lacks is refused unless `dropparams` removes it, a parameter path or
pattern the new voice cannot take is refused, and a change that plays but
sounds different (drum lanes the new kit does not play, a row or mark the
old voice played and the new one ignores, such as a pressure row on acid) is
refused unless `acceptchanges` accepts it. The check knows every musical
input a pattern can carry (the marks and brackets on steps, the velocity
and expression rows, pattern settings, the velocity and gestures of
performed notes, drum hits) and what each voice does with it. Brackets, for
example, play as a tuplet on acid and as a chord on piano, so a change
between the two is refused. A pattern or voice it does not model is refused with
`voice_input_unmodeled` rather than let through. The
engine seeds chance and humanize by a track's position and a pattern's slot,
so a delete that moves a track or slot playing such a pattern is refused
unless `reseed` accepts it. What a flag accepts is listed in the intent's
`warnings` in `cicada apply --json`. `rename` updates every reference, and is
refused rather than written half done when the result would not validate.
`cicada intents --json` lists each one's fields and `reasons`.

The envelope can include `revision`, `author`, `session`, `dryrun` and
`revisions`. A supplied revision must match the exact score bytes; omit it to
use the current revision. `revision` covers only the score given to
`cicada apply`; `revisions` lists other source files of the project, each as
`{"path": …, "revision": …}`, at the revisions the intents were computed
against (a dry run with `--json` reports every file's revision in the same
form as `file_revisions`), and the batch is refused with
`revision_mismatch` when one of them has changed. Without `revisions`, a
change to another file between the dry run and the apply goes unnoticed: the
apply reads every file again and computes the edit from what it finds.

A batch that changes several files writes the score first, then each other
file, and checks each one against the bytes the apply read at its start; if a
write fails, the files already written are put back and the error names the
file. The writes are not atomic across a crash: a crash between two writes
can leave some files changed and others not. `cicada check` then reports the
references that no longer resolve (a renamed track still named in another
file, for example), and `.cicada/edits.jsonl` has no record of the batch,
because the record is appended after the last write.
`dryrun: true` prevents writes even with `--write`. `--author` and `--session`
override envelope attribution; the default author is `cli` when the envelope
has none. Successful writes append one record to `.cicada/edits.jsonl`.

Studio accepts the same envelope at `POST /api/intents`, with a required
`revision`. It commits the whole batch as one undoable edit. A dry run returns
`diff`, the current `revision`, `valid: true`, and `dryRun: true` without saving.

Committing grid edits, recorded takes, performed-note, gesture, lane, loop-length, pitch or time edits
through this HTTP endpoint also requires `X-Cicada-Source-Revision`, using the
`sourceRevision` from the workspace projection that supplied the edit. This
checks every project source before writing, including secondary definitions.
The pattern HTTP endpoint carries the same receipt as `source_revision` in its
JSON body; Studio's controls supply it automatically. Read-only dry runs do not
require the header. The local CLI envelope format is unchanged.

Grid cells, articulation, chords, range edits, timing and resizing write their
owning project source. The existing shared-phrase policies keep their meaning:
`pattern` expands uses in that pattern's file, `detach` expands one use, and
`definition` follows the shared token's source. An edit spanning different
source buffers refuses atomically; choose `pattern` to make the selection local.
Imported definitions remain read-only. `/api/toggle` also accepts the displayed
receipt as `source_revision`.

Grid, intent and history edit responses carry `X-Cicada-Write-Source-Revision`, captured
from the candidate before publication. Studio compares it with the subsequent
workspace projection before advancing queued gestures. A source-only outside
save requires a refresh even if the entry file's revision is unchanged.

`POST /api/record` carries the same receipt as `source_revision` in its JSON
body. Grid and played takes resolve all destination owners before the atomic
project write. A failure in any destination saves nothing. Owned secondary
files retain their comments and line endings; library sources require a local
variation first.

`shapeperformancepitch` takes `entity: "pattern:name"` and either
`operation: "transpose"` with a nonzero `semitones` from −48 to +48, or
`operation: "reflect"` with a `pivot` MIDI pitch from 0 to 127. The pivot is
the stored pitch, before the pattern's transpose. It changes all performed
note pitches while retaining timing, velocities and both gesture layers.
Out-of-range stored or sounding notes refuse the entire edit. Owned secondary
sources are supported; imported libraries require a local variation first.

Unknown fields and intent kinds are rejected. The arrange intents are
`moveclip`, `resizeclip`, `splitclip`, `duplicateclip` and `deleteclip`, each
with an `entity` such as `placement:intro`, plus `attick` or `lengthticks` where
they need a time. `convertsongtoarrange` replaces a `song` block with an
`arrange` block, but only after a deterministic render shows the same audio; it
refuses otherwise and names the cause when it can (for example scene settings,
a pattern that would restart out of phase, or chance steps that restart at a
new placement). A dry run of that intent answers `convertible: true` with the diff,
or `convertible: false` with a `cause`. A revision conflict returns
HTTP 409 with `error`, `revision`, `source`, and `playingRevision`.

`scaleperformance` takes `entity: "pattern:name"`, `numerator` and
`denominator` (each 1–16). It scales the loop, gates and both gesture layers
in musical time, rounding absolute positions to ticks. Colliding controller
samples retain the later value; gates retain at least one tick. A resulting
same-pitch overlap is refused. Owned secondary pattern sources are supported;
libraries remain read-only. The Studio route also requires `source_revision`.

### Note batches: `editnotes`

`editnotes` changes many notes of one pattern in one edit: one commit and one
Undo entry, however many notes it names. It works on played performances,
step patterns (including chords, holds and the `velocity:` row) and drum
lanes. A step pattern or drum lanes refuse what their grid cannot hold; the
intent never turns a step pattern into a performance.

| Field | Meaning | Unit and range |
| --- | --- | --- |
| `entity` | `pattern:name` | required |
| `op` | `move`, `resize`, `velocity`, `delete`, `insert` or `quantize` | required |
| `notes` | The notes to change, as the client last saw them: `at`, `pitch` (or `lane` for a drum hit), `length`, `velocity` | at most 512 |
| `notes[].at`, `notes[].length` | Start and length | ticks (960 per beat), 0 to 2^40 |
| `notes[].pitch` | Stored MIDI pitch, before the pattern's `transpose` | 0–127 |
| `notes[].lane` | Drum lane name | drum lanes only |
| `notes[].velocity` | The note's velocity | 1–127 |
| `dt` | `move`: change of start | ticks, ±2^40 |
| `dp` | `move`: change of pitch, or of lane in authored lane order for drums | semitones or lanes, ±127 |
| `dl` | `resize`: change of length | ticks, ±2^40 |
| `dv` | `velocity`: change, clamped to 1–127 | ±126 |
| `velocity` | `velocity`: the new value instead of `dv`; setting both is refused | 1–127 |
| `grid` | `quantize`: grid | ticks, 30–3840 |
| `strength` | `quantize`: how far each start moves to its grid line; 0 means 100 | percent, 1–100; 100 only in step patterns and drum lanes |
| `copy` | `move`: keep the originals | boolean |
| `insert` | `insert`: notes to add, with the same fields as `notes` | at most 512 |
| `shared` | Only `pattern`: phrase uses expand inside the edited pattern | optional |

In step patterns and drum lanes, `at`, `length`, `dt`, `dl` and `grid` must be
whole steps of the pattern (`step=`, 240 ticks by default). A note is
identified by its start and its pitch or lane; every named note must still
exist with the same length and velocity, or the batch is refused.
`quantize` with no `notes` acts on every note. Notes keep their own accent,
chance, ratchet, slide and velocity, and the velocity row moves with them;
expression rows (`bend:`, `vibrato:`, `pressure:`, `timbre:`) stay on their
steps. Notes that land on one step merge into a chord only when their length,
accent, chance and velocity match.

Studio's pattern route takes the batch as `notes`, without `entity` or
`shared`, and requires `source_revision`:

```json
{"revision": "<disk revision>", "source_revision": "<project receipt>",
 "action": "notes", "pattern": "take",
 "notes": {"op": "quantize", "grid": 240}}
```

The Studio window's typed action `POST /__actions/notes` carries the same
batch as the string field `edit`, with `"v": 1` added.

A batch that changes nothing returns 200 with `unchanged: true` and a
`message` (`Already on the grid.` or `Nothing changed.`) and writes no
history. A stale `revision` or `source_revision`, or a take being captured,
returns 409. A refused batch returns 422; when the refusal is about one
note, the body names it as data as well as in `error`:

```json
{"error": "step 2, e4: after quantize, it would join c4 in a chord on step 1 and lose its 50% chance and play at velocity 100 instead of 60; ...",
 "code": "CICADA-NOTES", "reason": "chord-modifiers",
 "note": {"at": 240, "step": 2, "pitch": 64}}
```

`note.at` is the start in ticks where the request found the note, `step` is
one-based (step patterns and drum lanes only), and `pitch` or `lane`
identifies it. The reasons are:

| `reason` | The note… |
| --- | --- |
| `invalid-note` | has a start, length, pitch or velocity outside its range |
| `note-changed` | is missing, or its length or velocity differs; select it again |
| `no-lanes` | names a lane in a pattern without drum lanes |
| `not-whole-steps` | does not start or end on the pattern's step grid |
| `outside-pattern`, `outside-lanes`, `outside-loop` | would leave the pattern's steps, its lanes, or the performance loop |
| `pitch-range` | would leave MIDI 0–127, counting the pattern's `transpose` |
| `too-short` | would become shorter than one step or one tick |
| `gesture-cut` | would end before its last gesture point |
| `overlap` | would overlap another note: the same pitch in a performance, or any note in a step pattern |
| `same-pitch`, `same-cell` | would land on the pitch or drum cell of another selected note |
| `chord-pitches-equal` | would equal another pitch of its own chord |
| `chord-size` | would make a chord of more than four pitches |
| `chord-articulation` | would make a chord with a note that slides or ratchets |
| `chord-length`, `chord-modifiers` | would join a chord of another length, or with another accent, chance or velocity |
| `acid-one-pitch` | would put a second pitch on an acid step |
| `fixed-length` | is a drum hit, which lasts one step |
| `unknown-lane` | names a drum lane the pattern does not have |

Other refusals (an unknown `op`, a grid that is not a whole number of steps,
deleting every note of a performance, acid tuplet groups) carry only `error`.

Examples, each one `editnotes` intent:

- Played performance, quantize to 1/16: `{"op": "quantize", "grid": 240}`
  turns `note 64 at=250ticks length=200ticks velocity=105;` into
  `note 64 at=240ticks length=200ticks velocity=105;`.
- Step pattern, move g4 of a chord two steps later:
  `{"op": "move", "dt": 480, "notes": [{"at": 0, "pitch": 67, "length": 480, "velocity": 80}]}`
  turns `[c4 e4 g4]^?70 - . . c5` into `[c4 e4]^?70 - g4^?70 - c5`.
- Drum lanes, set a hit's velocity:
  `{"op": "velocity", "velocity": 56, "notes": [{"at": 3120, "lane": "sd", "length": 240, "velocity": 126}]}`
  turns the `sd` hit `x9*2` on step 14 into `x4*2`.

Studio's arrangement panel uses a second set of arrange intents that keep the
position and length text you type, such as `@5.1.1` or `3bars`:
`updateplacement` (change `at`, `length`, `track` or `content` together, so a
move and a trim never pass through an overlap), `addplacement`, `copyplacement`,
`removeplacement`, `setmarker`, `addmarker` and `removemarker`. Each takes an
`entity` (`placement:name` or `marker:name`) or a new `name`, and a `settings`
object with the fields to set. `removeplacement` removes only the declaration;
`deleteclip` also removes the comment lines above it. `varyplacement` takes
`entity: "placement:name"` and a new pattern `name`. It makes a local copy of
the compiled pattern and rebinds only that placement in one atomic edit. A
render check must confirm unchanged audio; source and library definitions
remain intact, including when the arrangement lives in another source file.
Automation lanes use
`setautomationpoint` and `removeautomationpoint`, with an `entity` such as
`param:bass.level`, `at`, `oldat` (the point being moved), `value`, `shape`
and `curve`. `placesamplerprint` and `placevocalcomp` record the two Studio
actions that print audio first (resampled pads and comped vocal takes); they
name the audio file Studio already wrote, so use them for replay, not to print.

## Highlighting and navigation

`highlight` draws source in the terminal, writes a standalone HTML page, or
prints token spans. It honors `NO_COLOR`. `symbols` lists definitions; add
`--refs` to include references and `--json` for machine-readable output:

```sh
cicada highlight examples/cicada-chorus.cicada
cicada highlight --html examples/cicada-chorus.cicada
cicada symbols --refs --json examples/cicada-chorus.cicada
cicada view examples/first-song/main.cicada -o first-song.html
```

`view` writes a read-only HTML snapshot. Studio is the editable view.

The grammar ships five tree-sitter queries for editor integrations:

| Query | Use |
| --- | --- |
| [`highlights.scm`](../../language/highlights.scm) | Token and musical-note captures |
| [`locals.scm`](../../language/locals.scm) | Parameter and local-binding scopes |
| [`tags.scm`](../../language/tags.scm) | Definitions, references, and musical symbol kinds |
| [`folds.scm`](../../language/folds.scm) | Fold braced declarations |
| [`indents.scm`](../../language/indents.scm) | Indent braced bodies |

Each token has one capture. Its parent supplies context, so `-` is a tie in a
pattern and subtraction in an expression; `,` lowers a pitch by an octave or
separates call arguments depending on where it appears. Accent, slide, and
ratchet captures can also style the whole note or hit. The built-in Night
theme colors pitches, drum hits, and chance values for terminal highlighting.

For Go hosts, package [`language`](../../language) exposes `Highlight`,
`WriteANSI`, `WriteHTML`, `WriteSpans`, `Symbols`, `NewHighlighter`, and
`NewTagger`. The source queries are the complete capture and navigation
contract.

The language test fixtures cover every grammar token and check that each
token has exactly one capture. After changing a query intentionally, run
`go test ./language -update` and review the fixture diff. `make grammar-check`
checks the generated parser and editor queries together.
