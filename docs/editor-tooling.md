# Editor tooling

Five tree-sitter queries sit beside the [grammar](../language/grammar/grammar.go). They run on gotreesitter against the generated parser blob, and they follow Neovim's query conventions, so other tree-sitter hosts can load them as well.

| Query | Purpose |
| --- | --- |
| [`highlights.scm`](../language/highlights.scm) | One capture for every token, plus whole-note style layers |
| [`locals.scm`](../language/locals.scm) | Instrument scopes: parameter and `let` references take their definition's capture |
| [`tags.scm`](../language/tags.scm) | Definitions and references with musical kinds, for outlines and navigation |
| [`folds.scm`](../language/folds.scm) | Every braced body folds |
| [`indents.scm`](../language/indents.scm) | Braces indent one level, as `cicada fmt` does |

```sh
cicada highlight examples/cicada-chorus.cicada           # 24-bit ANSI color
cicada highlight --html examples/cicada-chorus.cicada    # standalone HTML page
cicada highlight --spans examples/cicada-chorus.cicada   # line:column, capture, text
cicada symbols examples/cicada-chorus.cicada             # definitions
cicada symbols --refs --json examples/cicada-chorus.cicada
```

`highlight` honors `NO_COLOR`. Both commands also work on a score that does not parse: they report its syntax errors and exit 1, and `highlight` covers the unparsed text with `error` spans. The [cicada chorus](../examples/cicada-chorus.cicada) example uses every construct the renderer plays; [`ahead.cicada`](../language/testdata/ahead.cicada) holds the syntax the grammar accepts before the renderer supports it.

The semantic project model also exposes a versioned [field catalog](../project/schema/cicada.fields-1.json) for editor property panels and agent tooling. `cicada fields` emits the catalog as JSON; `cicada explain pattern` prints a construct table and `cicada explain pattern.steps --json` selects one descriptor. The catalog comes from tags on the typed project IR and supplies required-field lists to JSON decoding. The optional `project.edition` field defaults to 1 when reading older project JSON; new exports record it explicitly. Other semantic JSON fields remain required. The catalog currently describes semantic JSON fields; source syntax field roles and editor actions are still being designed.

`cicada view examples/first-acid.cicada -o first-acid.html` exports a read-only score view. It keeps syntax highlighted source beside pitch and drum grids, with the song and tracks below. The same command accepts validated semantic JSON and prints its canonical Cicada source. `cicada studio examples/cicada-chorus.cicada` opens the editable source-linked view with native playback and next-bar source reloads. In Studio, clicking a pitch row sets that step's note in the score file; clicking the active pitch clears it. Accent and Slide cells toggle `^` and `~` on existing notes. Ratchet cycles 1–8 hits and Chance cycles 100%, 75%, 50%, and 25%; exact chance values can still be written in source. Note modifiers stay with a changed pitch. A grid edit on a phrase step changes its shared source token, so every use updates together. The chorus example uses the concise notation from the owner's language design.

## Captures

Each token receives exactly one capture. When one token means different things in different places, its parent decides: `-` is a tie in a pattern and subtraction in an expression, and `,` lowers an octave in a step and separates arguments in a call. Predicates split built-in names from user names. As a result, editors that let the first matching pattern win and editors that let the last one win produce the same colors. Musical meaning is carried by dotted suffixes, so an editor that does not know `@constant.pitch.degree` falls back to `@constant.pitch`, then `@constant`.

| Construct | Example | Capture |
| --- | --- | --- |
| Version directive | `cicada 1` | `@keyword.directive`, `@number.version` |
| Title | `title "Night circuit"` | `@keyword`, `@markup.heading` |
| Key root and scale | `key c# dorian` | `@constant.pitch.root`, `@type.builtin` (an unknown scale is `@type`) |
| Seed | `seed 4242` | `@number.seed` |
| Instrument | `instrument glassbass` | `@keyword.type`, `@type.definition` |
| Authored kit | `kit steel { bd=kick; ch=builtin.ch; }` | `@type.definition` for the kit, `@tag.builtin` for lanes and built-in voices, `@type` for an instrument target |
| Parameter | `param cutoff: hz = 720hz;` | `@variable.parameter`, unit `@type.builtin` |
| Voice | `voice mono` | `@keyword.function`, `@keyword.modifier` |
| Let and out | `let osc = ...;` `out = ...;` | `@keyword` and `@variable`; `@keyword.return` |
| Voice input | `pitch gate velocity sample_rate` | `@variable.builtin` |
| Graph primitive | `saw` ... `clamp` | `@function.builtin` (any other call is `@function.call`) |
| Track | `track lead glassbass` | `@variable.member`; the voice is `@type.builtin` for `acid` and `drums`, otherwise `@type` |
| Track parameter | `cutoff = 900hz` | `@property` |
| Enum and boolean values | `filter = diode` `savage = on` | `@constant`, `@boolean` |
| Effect | `fx echo { ... }` | `@keyword.type`, `@type.definition` |
| Phrase | `phrase hook acid` | `@keyword.directive.define`, `@function.macro` |
| Phrase use | `use hook*2 transpose = 12` | `@keyword.import`, `@function.macro`, `@operator.repeat`, `@number.repeat`, `@attribute.builtin` |
| Pattern | `pattern bass-a acid` | `@keyword.function`, `@function`, `@type.builtin` |
| Pattern attribute | `steps = 16` | `@attribute` |
| Scale degree | `1` ... `7`, `4#` | `@constant.pitch.degree` |
| Letter pitch | `c#3`, `bb1`, `e` | `@constant.pitch.letter` |
| Octave marks | `'` `,` | `@operator.octave.up`, `@operator.octave.down` |
| Accent and slide | `^` `~` | `@operator.accent`, `@operator.slide` |
| Ratchet | `*2` | `@operator.ratchet`, `@number.ratchet` |
| Probability | `?50` (legacy `%50`) | `@operator.probability`, `@number.probability` |
| Rest, tie, bar | `.` `-` `\|` | `@punctuation.special.rest`, `@punctuation.special.tie`, `@punctuation.delimiter.bar` |
| Drum lane | `bd:` | `@tag.builtin` for the eleven built-in lanes, otherwise `@tag` |
| Drum hits | `x` `X` `x1`-`x9` | `@constant.hit`, `@constant.hit.accent`, `@constant.hit.velocity` |
| Scene | `scene main { bass = bass-a }` | `@label`; track `@variable.member`; pattern `@function`, or `@constant.builtin` for `off` and `keep` |
| Song | `song { main*8 }` | `@label`, `@operator.repeat`, `@number.bars` |
| Numbers | `620hz` `380ms` `3s` `-6db` `50%` `1/8t` | `@number.frequency`, `@number.duration`, `@number.decibel`, `@number.percent`, `@number.fraction`; unitless numbers are `@number` or `@number.float` |

Three captures style a whole note or hit over its tokens: an accented note or `X` hit is `@markup.strong`, a slide is `@markup.italic`, and a ratchet is `@markup.underline`. Editors combine them with the token colors, so an accented tonic is bold and gold.

## The Night theme

`cicada highlight` draws with `language.Night`. Keywords are amber and graph primitives are teal. Scale degrees follow the circle of fifths around the hue wheel: the tonic is gold, the dominant and subdominant sit beside it, and the leading tone lands farthest away. Letter pitches take the hue of their pitch class the same way, with C in gold, and grow lighter by octave. Drum hits glow from a dim ember at `x1` to a bright flame at `X`. A probability fades toward the background as its chance drops, and rests recede.

## Go API

`cicada lsp` serves these queries and the score validator over stdio JSON-RPC. It supports full-document change notifications, diagnostics, pitch/chance/drum hover, pattern and song inlays, semantic tokens, definition lookup, rename, and a notation quick fix. Parameter and `let` renames stay within their instrument. The server validates a renamed score before returning edits. The quick fix shares `cicada fix`'s meaning-preserving rewrite, uses a document-versioned edit, and creates `cicada.mod` when removing a legacy header from a loose score. It is offered only when the client supports the needed workspace edit operations. Clients should register `.cicada` as the Cicada language and launch the CLI with `lsp`.

Package [`language`](../language) embeds the queries and runs them on gotreesitter:

- `Highlight(src)` returns nested spans, with parameter and `let` references resolved through `locals.scm`.
- `WriteANSI`, `WriteHTML`, and `WriteSpans` draw spans with a `Theme`.
- `Symbols(src)` lists definitions and references from `tags.scm`. It resolves a track's voice reference to an instrument or kit. Kinds keep namespaces apart, so a track named `bass` and a pattern named `bass` stay distinct.
- `NewHighlighter` and `NewTagger` return gotreesitter's `Highlighter` and `Tagger` for hosts that paint flat ranges or index tags themselves.

The language tests hold the contract. Every token in the grammar appears in a fixture, every token in every fixture has exactly one capture, and only the three layers capture larger nodes. The tests also lock each fixture's captures in `language/testdata/*.spans`. After an intended query change, run `go test ./language -update` and review the diff. `make grammar-check` runs these tests against the regenerated parser.
