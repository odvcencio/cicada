# Cicada: meet in the middle

Sep 24, 2026 · @Oscar

Drop `cicada 1` from files. The language edition moves to an optional `cicada.mod`, and the tool infers the edition when there is none. Change the syntax only where both audiences stumble. Put the rest of the teaching into the editor: hovers, inlay hints, and a `cicada play` loop that hot-swaps edits on the next bar.

The rule for the middle is to use musicians' nouns (bar, note, chance, scene) inside programmers' structure (named blocks, reuse, diffs). The file should never have to state something it already implies. Where the doc has to choose, it favors expressiveness, extensibility, and composability.

## Why the header exists

The header is insurance for a future that has not arrived. Only one edition exists, and the line has no job today beyond being required.

- The grammar requires it. `source_file` begins with the keyword `cicada` and an integer, so a file without the line is a syntax error.
- The parser keeps the integer as `Score.Version`. `Validate` rejects anything but 1 with `CICADA-VERSION`: "only cicada 1 is supported".
- `docs/grammar-first.md` states the intent: "Grammar and semantics are versioned by `cicada 1`." When an edition changes what existing text means, old files should keep their old meaning.
- It does not version the sound. The project JSON, the kernel image, and the golden fingerprint each carry their own format version. DSP changes such as the ones in [odvcencio/cicada#21](https://github.com/odvcencio/cicada/pull/21) and [odvcencio/cicada#22](https://github.com/odvcencio/cicada/pull/22) change renders without touching the header. They update the golden fingerprint instead.

So the line guards semantic changes to the language, which is a real need. But one line per file is the wrong place for it, and it guards nothing yet.

## Versions without a header

Use Go's model: the edition belongs to the project, not to each file. Syntax can be inferred from the text. Meaning cannot. If a later edition changed the default octave from 2 to 3, `c` would name a different note, and nothing in the file would say which one you meant. That case is what editions are for, and it needs one answer per project, not per file.

| Option | Where the edition lives | No edition means | Verdict |
| --- | --- | --- | --- |
| Header in every file (today) | Line 1 of each score | Syntax error | Drop. It is boilerplate for both audiences, and it duplicates across files |
| Infer from the text | Nowhere; features imply a minimum edition | The oldest edition that parses | Use as a hint only. It cannot see a meaning change |
| Project manifest (Go `go.mod`, Rust `edition`) | `cicada.mod` beside the scores | The newest edition this tool knows | **Adopt** |
| Fix on upgrade only | Nowhere | Whatever this tool speaks | Pair it with the manifest. On its own, it cannot tell what an old file meant |

The manifest is two lines, written by `cicada new`:

```
project night-circuit
cicada 1
```

The tool finds a score's edition in this order:

1. The nearest `cicada.mod` in the score's directory or a parent.
2. A legacy `cicada N` first line. It stays legal forever, and `cicada fix` moves it into the manifest.
3. The newest edition the tool knows. `render` and `play` then print one line, such as `no cicada.mod: reading as cicada 1`.

Every change of meaning between editions ships with a mechanical rewrite in `cicada fix`, as `go fix` and `cargo fix --edition` do. A project upgrades with one command and one reviewable diff. Renders and JSON exports record the edition they used, so a stray loose file can still be traced.

Sound stays a separate axis. The engine version decides the samples. A project that needs identical audio after a tool upgrade pins it in the manifest (`engine v0.4`) and checks it with a fingerprint, the same fingerprint `make test-golden` uses today.

## Where Cicada reads as foreign

Most of the notation already meets in the middle. Scale degrees are Nashville numbers, letter pitches are what a keyboard says, `x`/`X` rows are a drum machine, and scenes and songs are Ableton's session and arrangement. The friction sits in a handful of places where the file states something twice, or borrows a symbol that means something else to one audience.

| Construct | Today | A musician reads | A programmer reads | Proposal |
| --- | --- | --- | --- | --- |
| Version line | `cicada 1` | Noise | A doctype | Remove; the edition goes in `cicada.mod` |
| Pattern kind | `pattern bass-a acid`, `phrase hook acid` | "acid" twice, once for the synth and once for the notes | A type tag that the track already implies | Notes are the default. Only `drums` is named |
| Step count | `steps = 16`, which must equal the cells | Counting by hand | A checksum | Infer it. The editor shows the count as an inlay |
| Pattern settings | `acid steps = 16 swing = 56 gate = 60 {` | Unitless numbers | Arguments with no separators | Move them inside the braces with units: `swing = 56%`, as tracks already do |
| Chance | `3%70`, while `50%` elsewhere is a unit | Unreadable | Modulo | `3?70`, read as "maybe, 70". `%` becomes only a unit |
| Scene holds | `drums = keep` | Why say it | Fine | Omit the line. The engine already keeps an omitted track; say so in the docs and keep `keep` as optional |
| Instrument lines | `param cutoff: hz = 720hz;` | Code | A redundant type | `param cutoff = 720Hz`. The unit comes from the literal. No semicolons, because every statement starts with a keyword |
| Unit spelling | `720hz`, `-6db` | Wrong case | Fine | Accept `Hz`, `kHz`, `dB`; `fmt` prints SI spelling |
| Phrase transpose | `use hook transpose = 12` | Long | Keyword argument | `use hook +12`, alongside `use hook*2` |
| Separators | `;` ends drum rows, kit bindings, and instrument lines, but not track or scene lines | Arbitrary | Inconsistent | One rule: no terminators. A line ends where the next one begins |

Keep the rest: degrees and letter pitches, `'` and `,` octaves, `^` accent, `~` slide, `*n` meaning "times" everywhere, `x1`–`x9`, `|` bars, `1/8t` note values, `scene`, and `song`. They are dense because a grid is dense. The editor explains them on hover instead of the syntax spelling them out.

The `kit` block in [odvcencio/cicada#26](https://github.com/odvcencio/cicada/pull/26) is new syntax, so it can adopt the separator rule before it merges. It currently writes `bd = kick;` with semicolons, while `track` and `scene` lines have none.

## The middle, in one score

The after version keeps every note of `examples/cicada-chorus.cicada` and drops what the file already implied. It loses the header, every kind tag but `drums`, every step count, the `keep` lines, and every semicolon. Every remaining number carries its unit. It adds one line, `octave = 2`, which makes the instrument's register explicit. Each change is additive, so edition 1 can accept both spellings and `cicada fix` can rewrite old files. Removing the old spellings would be the first job of an edition 2.

Before (an excerpt, today's syntax):

```
cicada 1

title "Cicada chorus"
tempo 126.5
key e minor

instrument nightbass {
  param cutoff: hz = 480hz;
  param decay: ms = 0.3s;
  voice mono {
    let shape = env(gate, decay);
    // body, sweep, and dark as in the example
    out = highpass(diode(dark, sweep + 120hz, 0.2), 30hz) * (shape * velocity);
  }
}

track bass acid {
  cutoff = 540hz
  level = -2db
}

phrase chirp acid {
  1^ 1*2 . 5~ | 7, 1' - .
}

pattern bass-a acid steps = 16 swing = 56 gate = 60 seed = 7 {
  1^ . 1~ 3 . 1 4# 5 | 1 . 7,~ 1 5^*2 3%70 1 -
}

pattern bass-b acid steps = 16 swing = 56 gate = 60 {
  use chirp
  use chirp transpose = 7
}

pattern beat drums steps = 16 {
  bd: X..x..x.X..x.x..;
  sd: ....X.......Xx3..;
  ch: xx.xxx.xx*2x.xxx%60xx;
}

scene chorus {
  bass = bass-b
  drums = keep
  low = keep
  buzz = chorus
}
```

After (proposed):

```
title "Cicada chorus"
tempo 126.5
key e minor

instrument nightbass {
  octave = 2
  param cutoff = 480Hz
  param decay = 0.3s
  voice mono {
    let shape = env(gate, decay)
    // body, sweep, and dark as in the example
    out = highpass(diode(dark, sweep + 120Hz, 0.2), 30Hz) * (shape * velocity)
  }
}

track bass acid {
  cutoff = 540Hz
  level = -2dB
}

phrase chirp {
  1^ 1*2 . 5~ | 7, 1' - .
}

pattern bass-a {
  swing = 56%
  gate = 60%
  seed = 7
  1^ . 1~ 3 . 1 4# 5 | 1 . 7,~ 1 5^*2 3?70 1 -
}

pattern bass-b {
  swing = 56%
  gate = 60%
  use chirp
  use chirp +7
}

pattern beat drums {
  bd: X..x ..x. X..x .x..
  sd: .... X... .... Xx3..
  ch: xx.x xx.x x*2x.x xx?60xx
}

scene chorus {
  bass = bass-b
  buzz = chorus
}
```

The spaces between beats in the drum rows are legal today, because hits may be written apart. `cicada fmt` should insert them on every beat, so a row reads like a drum machine's four-step groups.

## Tooling

The tool should feel like `go`: one static binary, no configuration, and a fast loop from edit to sound. The pieces mostly exist. The live engine already lands edits on the next bar, `fmt` and `validate` exist, and the gotreesitter queries already classify every construct. The work is to wrap them in project-level commands with good defaults.

| Command | What it does | Today |
| --- | --- | --- |
| `cicada new <name>` | Writes `cicada.mod` and a `main.cicada` that plays eight bars | New |
| `cicada play [score]` | Plays through the live engine and lands each save on the next bar. A score that does not parse keeps the last good version playing and prints the error | New; the engine supports it |
| `cicada check` | Checks every score in the project, with caret diagnostics and a suggested fix | `validate`, per file; keep it as an alias |
| `cicada fmt` | Formats the whole project by default, grouping drum rows by beat | Exists, per file |
| `cicada fix` | Applies edition migrations and rewrites old spellings | New |
| `cicada render` | Defaults to 48 kHz, 24-bit, the song length plus the tail, and output beside the score | Exists, with required flags |
| `cicada export midi`, `stems`, `json` | Interchange with DAWs and other tools | JSON through `convert`; MIDI and stems arrive with [odvcencio/cicada#26](https://github.com/odvcencio/cicada/pull/26) |
| `cicada lsp` | Language server over the same parser and queries | New; `highlight` and `symbols` exist |

A diagnostic points at the step and says what to write:

```
live.cicada:21:26: chance needs a number
   ch: xx.x xx.x x*2x.x xx?xx
                          ^
   write ?50 for a 50% chance
```

The language server is where the notation gets taught, so the syntax can stay terse:

- Hover names the music: `3` is "G, the third of E minor", `x5` is "velocity 70", `*2` is "two hits in one step", and `?70` is "plays 70% of the time". A parameter shows its default and any track override.
- Inlay hints show the step count at the end of each pattern line and the bar and time after each song entry, such as `chorus*8` followed by `bars 5–12, 0:07.6`.
- Diagnostics come from the validator as you type, and every `cicada fix` rewrite doubles as a quick fix.
- Semantic tokens come from `highlights.scm`. Rename and go-to-definition come from `tags.scm` and `locals.scm`.

All of it is pure Go on gotreesitter, with no cgo. The same code builds to WASM for a browser editor, such as the Cicada Live concept, so the terminal, the editor, and the browser color a score identically.

### Where the live views run

`cicada studio` and a VS Code extension are one app in two frames. Both talk to one local Go process that does four jobs:

- It holds the project files as the source of truth.
- It plays through the native engine, so latency does not depend on the browser.
- It speaks LSP.
- It streams transport state (bar, step, launches, landed edits) over a WebSocket, as `cicada/*` notifications beside LSP.

| Frame | Text | Live views |
| --- | --- | --- |
| `cicada studio` | Any editor you like, watching the same files | Code, Session, History, and Voice in a browser tab |
| VS Code extension | The editor itself, backed by `cicada lsp` | The same views in a webview beside the score |

Flipping between notation and live editing is a split, not a mode switch. A grid click becomes a text edit on the file, so every editor sees it and git records it. The History view is that edit stream grouped by bar. [Cicada Live](https://claude.ai/artifact/HH7rHsHKX4p9QyRTkwcuVp) holds the design concept.

## Decisions

Each decision favors expressiveness, extensibility, and composability. Expressiveness means more music from the same few words. Extensibility means room to add words later without breaking scores. Composability means pieces written once work anywhere.

| Question | Decision | Why |
| --- | --- | --- |
| Manifest | `cicada.mod`, one directive per line, like `go.mod` | New directives add lines without changing old ones. Examples are `engine` to pin the sound, and `require` for shared instruments and kits |
| Octave | Explicit per instrument | A phrase written once lands in the register of whichever instrument plays it |
| `stop` and `off` | Two words with two roles: `stop` is a scene verb, and `off` is a setting's value | Scenes can grow to set any track property, and new verbs can join `keep` and `stop` |
| Drum rows without `;` | Proposed: the lane label becomes one token with its colon, `bd:` | Hits stay separate tokens, so new hit modifiers extend the grammar normally |
| `kit` separators | Yes: `bd = kick` with no `;`, before [odvcencio/cicada#26](https://github.com/odvcencio/cicada/pull/26) merges | One separator rule across every block |

### Octave

Every instrument states its home octave. The built-in voices declare theirs too; `acid` is 2.

```
instrument nightbass {
  octave = 2
  param cutoff = 480Hz
  ...
}
```

Scale degrees count up from the key's root in that octave. A letter pitch with no octave digit sits in it. `'` and `,` move from there, and `c#3` stays absolute, with C4 as MIDI 60. A track can override the octave like any parameter, so one instrument can play in two registers. In edition 1 the default of 2 still applies, and `cicada fix` writes `octave = 2` into each instrument. Edition 2 requires the line.

### Stop and off

| Word | Role | Example | At the switch |
| --- | --- | --- | --- |
| A pattern name | Launch | `bass = bass-b` | The track starts this pattern |
| `keep`, or no line | Carry on | `drums = keep` | Nothing changes |
| `stop` | Sequencer verb | `bass = stop` | The track starts no new notes, and the playing note releases. The engine releases the slot, which is what scene `off` does today |
| `off` | A setting's value | `drums.level = off`, `savage = off` | The setting is off. A muted track keeps sequencing, so it comes back in time. The engine uses its layer mask |

A scene line becomes `target = value`, where the target is a track or one of its settings, such as `drums.level` or `bass.cutoff`. Anything a track can set, a scene can set. Verbs are a reserved set that editions can grow, for example with `fade` or `restart`. When a new verb collides with a pattern name, `cicada fix` renames the pattern. Edition 1 reads scene `off` as `stop`, and `cicada fix` rewrites it. Edition 2 rejects `off` as a scene verb and suggests `stop`.

### Drum rows

The lane label becomes one token that includes its colon. A label needs its colon, so the lexer never reads `xx..` as a name. A row ends where the next label or the closing brace begins. Each hit stays its own token, so per-hit highlighting survives and new hit modifiers extend the grammar the normal way. The cost is that `bd :` with a space stops being a label; `fmt` already writes `bd:`. A grammargen test should confirm that the label token stays out of other lex modes, such as `param cutoff: hz`.

## Rollout

Ship in the order that removes the most friction for the least grammar risk. The first two steps make Cicada feel different without changing any score.

1. **Manifest and optional header.** Make `cicada N` optional in the grammar, add `cicada.mod` discovery and `cicada new`, and have renders and JSON record the edition they used.
2. **`cicada play`.** Watch, hot-swap on the next bar, and keep the last good version playing on an error.
3. **Additive spellings.** Add `?` for chance, settings inside pattern braces, inferred step counts, optional kind tags, unit case, `use hook +7`, no semicolons, `bd:` labels, `octave` on instruments, and `stop` and setting targets in scenes. Carry the separator rule into `kit` in [odvcencio/cicada#26](https://github.com/odvcencio/cicada/pull/26). Then add `cicada fix`, and update the docs and examples.
4. **`cicada lsp`, then `cicada studio`.** Hovers, inlays, and quick fixes come first. Then the live views arrive over the same server.
5. **Edition 2**, when a real meaning change needs one. It requires `octave`, rejects scene `off`, and drops the old spellings through `cicada fix`.
