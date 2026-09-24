# Cicada: programmable music notation

Cicada is an experimental music language for people, agents, and a future extensible DAW. A `.cicada` file describes music and can define the instruments that play it. [Grammargen](language/cicada.grammar) compiles the grammar into a parser blob consumed by gotreesitter; the compiler builds a typed score, deterministic note events, and bounded DSP graphs. The offline renderer writes stereo 24-bit WAV from custom mono instruments, the built-in acid voice, and eleven synthesized drum lanes. The [Cicada 1 language reference](docs/language-v1.md) describes the supported notation.

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

The [first acid score](examples/first-acid.cicada) renders sixteen bars of acid, drums, phrase reuse, and scenes:

```sh
go run ./cmd/cicada ast examples/first-acid.cicada
go run ./cmd/cicada graph examples/first-acid.cicada glassbass
go run ./cmd/cicada events examples/first-acid.cicada lead lead-a
go run ./cmd/cicada render examples/first-acid.cicada -o first-acid.wav --rate 48000 --bits 24 --bars 16 --tail 3s
go run ./cmd/cicada verify-wav first-acid.wav --rate 48000 --bits 24 --bars 16 --tail 3s --peak-max-db -0.3 --dc-max-db -60
```

`validate` checks syntax, references, and instrument types. `ast` prints the typed score; `graph` prints a compiled instrument; `events` shows a bar of sample-positioned note onsets. `make grammar-check` verifies the generated parser and highlighting query, `make test` runs the Go suite, and `make probe-wasm` builds the TinyGo sequencing probe.

`make test-golden` renders eight bars of the compiled first-acid project through the native float32 engine and compares its 100 ms, 64-band spectral fingerprint with `testdata/golden/first-acid.fp`. `go run ./cmd/cicada golden --update` regenerates it and reports drift from the previous fixture; review an audio preview and record a listening note before accepting a changed golden. The [fingerprint format](docs/golden.md) defines the comparison and same-platform sample hash.

The bounded live engine plays packed patterns with gate releases, all eleven drum lanes on the same step, sixteen slots per track, per-track pattern chains, quantized scene launches, and a song arrangement. Pattern-end scene launch finds the first shared end across active patterns with different lengths and restart offsets. Edits made during playback take effect at the next bar. `project.CompileEngine` lowers a typed project, including custom mono instruments, authored kits, and drive inserts, into engine configuration. `kernelimage.Encode` carries that complete configuration into the TinyGo module before playback. The engine renders through track inserts, the music mixer, and the limiter and emits 16-byte status messages. Build and exercise its TinyGo module with `make test-kernel-wasm`; the tests compare native and WASM musical event logs for live input, patterns, songs, and the compiled first-acid project. First-acid, authored-kit, and drive-insert renders are compared sample by sample at 44.1 and 48 kHz. The [live engine contract](docs/live-engine.md) covers the binary host buffers and current boundaries. DAW integration remains open.

The PCM24 renderer defaults to a three-second tail and reports its pre-limiter peak, limiter input overs, ceiling samples, and clipped output samples. `verify-wav` reads the PCM and Cicada timing chunk to check format, exact bar and tail duration, peak, and DC offset. Tracks accept `level` in dB (or `off`) and `pan` from -1 to +1. The dry music bus feeds a linked stereo master limiter with 1.5 ms lookahead and a -0.3 dBFS ceiling.

The [eleven-lane drum kit](examples/drums-kit.cicada) uses every built-in voice: `bd sd ch oh cp rs lt mt ht cb cy`. Render it with `go run ./cmd/cicada render examples/drums-kit.cicada -o drums-kit.wav --bars 1 --tail 0s`. The [authored kit](examples/authored-kit.cicada) maps `bd` to instrument code and `ch` to a built-in recipe; omitted lanes are silent. Render it with `go run ./cmd/cicada render examples/authored-kit.cicada -o authored-kit.wav --bars 1 --tail 0s`.

The [drive insert example](examples/fx/drive-insert.cicada) declares `fx drive` with `shape`, `gain`, `tone`, and `mix`, then sets `insert = drive` on the bass track. Drive supports soft, hard, fold, and diode shapes. The same score loads into the live native/TinyGo engine and renders to WAV; tracks without the insert are latency aligned. Render it with `go run ./cmd/cicada render examples/fx/drive-insert.cicada -o drive-insert.wav --bars 4`. Delay, reverb, compressor, sends, and effect graphs remain future work.

The typed [project format](project/schema/project-1.json) supports canonical JSON interchange and semantic comparison:

```sh
go run ./cmd/cicada fmt examples/first-acid.cicada --check
go run ./cmd/cicada convert examples/first-acid.cicada -o first-acid.cicada.json
go run ./cmd/cicada convert first-acid.cicada.json -o first-acid-roundtrip.cicada
go run ./cmd/cicada compare --semantic examples/first-acid.cicada first-acid-roundtrip.cicada
```

`fmt` also accepts `--check` before the filename and formats project JSON. Source conversion expands phrases and normalizes pitch spelling; semantic comparison checks the resulting project rather than source text. An unedited source document can be printed byte for byte, including comments.

The render path supports custom **mono** instruments with graph expressions, the built-in acid and eleven-lane drum voices, authored kits, and the drive insert. Filter response, aliasing and cost gates, drum fidelity, mixer meters and realtime integration, subjective listening review, polyphony, complete scene-switch behavior, and the DAW UI remain open work. The current Go module targets Go 1.25. This is an implementation slice, not a completed workstation.

Read the [language direction](docs/grammar-first.md) for the architecture and decisions still needed.
