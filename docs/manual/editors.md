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

The envelope can include `revision`, `author`, `session`, and `dryrun`. A supplied
revision must match the exact score bytes; omit it to use the current revision.
`dryrun: true` prevents writes even with `--write`. `--author` and `--session`
override envelope attribution; the default author is `cli` when the envelope
has none. Successful writes append one record to `.cicada/edits.jsonl`.

Studio accepts the same envelope at `POST /api/intents`, with a required
`revision`. It commits the whole batch as one undoable edit. A dry run returns
`diff`, the current `revision`, `valid: true`, and `dryRun: true` without saving.
Unknown fields and intent kinds are rejected. A revision conflict returns
HTTP 409 with `error`, `revision`, `source`, and `playingRevision`.

## Highlighting and navigation

`highlight` draws source in the terminal, writes a standalone HTML page, or
prints token spans. It honors `NO_COLOR`. `symbols` lists definitions; add
`--refs` to include references and `--json` for machine-readable output:

```sh
cicada highlight examples/cicada-chorus.cicada
cicada highlight --html examples/cicada-chorus.cicada
cicada symbols --refs --json examples/cicada-chorus.cicada
cicada view examples/first-acid.cicada -o first-acid.html
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
