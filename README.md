# Cicada: programmable music notation

Cicada is an experimental music language for people, agents, and a future extensible DAW. A `.cicada` file describes music and can define the instruments that play it. [Grammargen](language/cicada.grammar) compiles the grammar into a parser blob consumed by gotreesitter; the compiler builds a typed score, deterministic note events, and bounded DSP graphs. The offline renderer writes stereo 24-bit WAV from custom mono instruments, the built-in acid voice, and six synthesized drum lanes.

Render the [circuit kit score](examples/circuit-kit.cicada). Its bass, kick, snare, and hat are all defined in the score:

```sh
go run ./cmd/cicada validate examples/circuit-kit.cicada
go run ./cmd/cicada render examples/circuit-kit.cicada -o circuit-kit.wav
```

The repository includes a [rendered circuit kit](circuit-kit.wav) and a [longer glassbass study](glassbass.wav), both generated from Cicada source.

Render the [acid voice study](examples/acid-voice.cicada) to hear the built-in synthesizer path:

```sh
go run ./cmd/cicada render examples/acid-voice.cicada -o acid-voice.wav
```

The [first acid score](examples/first-acid.cicada) renders sixteen bars of acid, six available drum lanes, phrase reuse, and scenes:

```sh
go run ./cmd/cicada ast examples/first-acid.cicada
go run ./cmd/cicada graph examples/first-acid.cicada glassbass
go run ./cmd/cicada events examples/first-acid.cicada lead lead-a
go run ./cmd/cicada render examples/first-acid.cicada -o first-acid.wav --rate 48000 --bits 24 --bars 16 --tail 3s
go run ./cmd/cicada verify-wav first-acid.wav --rate 48000 --bits 24 --bars 16 --tail 3s --peak-max-db -0.3 --dc-max-db -60
```

`validate` checks syntax, references, and instrument types. `ast` prints the typed score; `graph` prints a compiled instrument; `events` shows a bar of sample-positioned note onsets. `make grammar-check` verifies the generated parser and highlighting query, `make test` runs the Go suite, and `make probe-wasm` builds the TinyGo sequencing probe.

The PCM24 renderer defaults to a three-second tail and reports its pre-limiter peak, limiter input overs, ceiling samples, and clipped output samples. `verify-wav` reads the PCM and Cicada timing chunk to check format, exact bar and tail duration, peak, and DC offset. Tracks accept `level` in dB (or `off`) and `pan` from -1 to +1. The dry music bus feeds a linked stereo master limiter with 1.5 ms lookahead and a -0.3 dBFS ceiling.

The typed [project format](project/schema/project-1.json) supports canonical JSON interchange and semantic comparison:

```sh
go run ./cmd/cicada fmt examples/first-acid.cicada --check
go run ./cmd/cicada convert examples/first-acid.cicada -o first-acid.cicada.json
go run ./cmd/cicada convert first-acid.cicada.json -o first-acid-roundtrip.cicada
go run ./cmd/cicada compare --semantic examples/first-acid.cicada first-acid-roundtrip.cicada
```

`fmt` also accepts `--check` before the filename and formats project JSON. Source conversion expands phrases and normalizes pitch spelling; semantic comparison checks the resulting project rather than source text. An unedited source document can be printed byte for byte, including comments.

The render path supports custom **mono** instruments with graph expressions and provisional built-in acid and six-lane drum voices (`bd sd ch oh cp rs`). The five remaining drum lanes produce explicit unsupported diagnostics until M1. Filter response, aliasing and cost gates, drum fidelity, mixer meters and realtime integration, subjective listening review, polyphony, complete scene-switch behavior, and the DAW UI remain open work. The current Go module targets Go 1.25. This is an implementation slice, not a completed workstation.

Read the [language direction](docs/grammar-first.md) for the architecture and decisions still needed.
