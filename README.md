# Cicada: programmable music notation

Cicada is an experimental music language for people, agents, and a future extensible DAW. A `.cicada` file describes music and can define the instruments that play it. [Grammargen](language/cicada.grammar) compiles the grammar into a parser blob consumed by gotreesitter; the compiler builds a typed score, deterministic note events, and bounded DSP graphs. The first offline renderer writes stereo 24-bit WAV from custom mono instruments.

Render the [circuit kit score](examples/circuit-kit.cicada). Its bass, kick, snare, and hat are all defined in the score:

```sh
go run ./cmd/cicada validate examples/circuit-kit.cicada
go run ./cmd/cicada render examples/circuit-kit.cicada -o circuit-kit.wav
```

The repository includes the [rendered 8.5-second circuit kit](circuit-kit.wav) and a [longer glassbass study](glassbass.wav), both generated from Cicada source.

The [first acid score](examples/first-acid.cicada) exercises the broader notation, including drum lanes, phrase reuse, and scenes:

```sh
go run ./cmd/cicada ast examples/first-acid.cicada
go run ./cmd/cicada graph examples/first-acid.cicada glassbass
go run ./cmd/cicada events examples/first-acid.cicada lead lead-a
```

`validate` checks syntax, references, and instrument types. `ast` prints the typed score; `graph` prints a compiled instrument; `events` shows a bar of sample-positioned note onsets. `make grammar-check` verifies the generated parser and highlighting query, `make test` runs the Go suite, and `make probe-wasm` builds the TinyGo sequencing probe.

The typed [project format](project/schema/project-1.json) supports canonical JSON interchange and semantic comparison:

```sh
go run ./cmd/cicada fmt examples/first-acid.cicada --check
go run ./cmd/cicada convert examples/first-acid.cicada -o first-acid.cicada.json
go run ./cmd/cicada convert first-acid.cicada.json -o first-acid-roundtrip.cicada
go run ./cmd/cicada compare --semantic examples/first-acid.cicada first-acid-roundtrip.cicada
```

`fmt` also accepts `--check` before the filename and formats project JSON. Source conversion expands phrases and normalizes pitch spelling; semantic comparison checks the resulting project rather than source text. An unedited source document can be printed byte for byte, including comments.

The render path currently supports custom **mono** instruments with graph expressions. Built-in acid and drum DSP, polyphony, note-off and complete slide behavior, sample assets, audio clips, and the DAW UI remain open work. The current Go module targets Go 1.25. This is an implementation slice, not a completed workstation.

Read the [language direction](docs/grammar-first.md) for the architecture and decisions still needed.
