# Cicada: programmable music notation

Cicada is an experimental music language for people, agents, and a future extensible DAW. A `.cicada` file describes music and can define the instruments that play it. The [grammar](language/grammar/grammar.go) is written in the grammargen Go DSL and compiles into a parser blob consumed by gotreesitter; the compiler builds a typed score, deterministic note events, and bounded DSP graphs. The offline renderer writes stereo 24-bit WAV from custom mono instruments, the built-in acid voice, and eleven synthesized drum lanes. The [Cicada 1 language reference](docs/language-v1.md) describes the supported notation.

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
go run ./cmd/cicada stems examples/sfx-bus.cicada -o sfx-stems --bars 1 --tail 0s
go run ./cmd/cicada verify-stems sfx-stems --tap pre-comp --residual-max-db -80
go run ./cmd/cicada midi examples/first-acid.cicada -o first-acid.mid
go run ./cmd/cicada verify-midi first-acid.mid --ppq 960 --type 1
```

`validate` checks syntax, references, and instrument types. `ast` prints the typed score; `graph` prints a compiled instrument; `events` shows a bar of sample-positioned note onsets. `make grammar` regenerates the parser blob from the Go DSL grammar, `make grammar-check` verifies the blob and the editor queries, `make test` runs the Go suite, and `make probe-wasm` builds the TinyGo sequencing probe.

The [cicada chorus](examples/cicada-chorus.cicada) tours a noise-and-ring tymbal voice, thirteen graph primitives, degrees and letter pitches, step modifiers, dense drum rows, phrases, and scenes. `highlight` draws a score in color, and `symbols` lists the names a score defines and where each is used. Both run the [editor queries](docs/editor-tooling.md) in `language/` on gotreesitter, including the authored-kit constructs:

```sh
go run ./cmd/cicada highlight examples/cicada-chorus.cicada
go run ./cmd/cicada highlight --html examples/cicada-chorus.cicada > cicada-chorus.html
go run ./cmd/cicada symbols --refs examples/cicada-chorus.cicada
go run ./cmd/cicada render examples/cicada-chorus.cicada -o cicada-chorus.wav
```

`make test-golden` renders eight bars of the compiled first-acid project through the native float32 engine and compares its 100 ms, 64-band spectral fingerprint with `testdata/golden/first-acid.fp`. `go run ./cmd/cicada golden --update` regenerates it and reports drift from the previous fixture; review an audio preview and record a listening note before accepting a changed golden. The [fingerprint format](docs/golden.md) defines the comparison and same-platform sample hash.

The bounded live engine plays packed patterns with gate releases, all eleven drum lanes on the same step, sixteen slots per track, per-track pattern chains, quantized scene launches, and a song arrangement. Pattern-end scene launch finds the first shared end across active patterns with different lengths and restart offsets. Edits made during playback take effect at the next bar. `project.CompileEngine` lowers a typed project, including custom mono instruments, authored kits, drive inserts, both effect sends, and music-bus compression, into engine configuration. `kernelimage.Encode` carries that complete configuration into the TinyGo module before playback. The engine renders through track inserts, music and SFX buses, send A's delay return, send B's reverb return, an optional music-bus compressor, and the limiter and emits 16-byte status messages. Build and exercise its TinyGo module with `make test-kernel-wasm`; the tests compare native and WASM musical event logs for live input, patterns, songs, and the compiled first-acid project. First-acid, authored-kit, drive-insert, delay-send, [FX bus](examples/fx-bus.cicada), [compressor bus](examples/fx/compressor-bus.cicada), and [SFX bus](examples/sfx-bus.cicada) renders are compared sample by sample at 44.1 and 48 kHz. The [live engine contract](docs/live-engine.md) covers the binary host buffers and current boundaries. DAW integration remains open.

The WAV renderer writes stereo PCM16, PCM24 (default), or IEEE float32 with a three-second tail by default. Integer output uses seeded TPDF dither unless `--dither=false`; `--normalize` sets the post-limiter sample peak to -1 dBFS, and `--block` selects 1–4096 render frames without changing the result. The report includes pre-limiter peak, limiter input overs, ceiling samples, and clipped output samples. `verify-wav` reads every sample and the Cicada timing chunk to check format, exact bar and tail duration, peak, DC offset, nonfinite float data, and full-scale integer samples. Tracks accept `level` in dB (or `off`), `pan` from -1 to +1, and `bus = music|sfx`. The music bus sums its tracks and both effect returns before optional compression; the SFX bus joins at master before the linked stereo limiter with 1.5 ms lookahead and a -0.3 dBFS ceiling.

Use `--from N --bars M` to render a bar range starting at zero-based bar `N`. `--bars 0` renders from that bar through the song end. WAV and stem exports process earlier bars to preserve instrument and effect state; only the selected range and requested tail are written. Pass the same `--from` value to `verify-wav`. The stem manifest records the start bar for standalone `verify-stems` checks.

`stems` creates a new directory of stereo float32 WAV files in one render pass: `01-<track>.wav` onward, `return-a.wav`, `return-b.wav`, `music.wav`, `sfx.wav`, and `master.wav`. `manifest.json` records the track routing so the directory can be verified on its own. Track and return taps include the music bus's -3 dB gain where applicable. `music.wav` is the pre-compressor tap; `master.wav` includes compression and limiting. `verify-stems` checks timing, finite samples, and that the pre-compressor music and SFX taps equal their component stems within the requested residual threshold. The output directory must not already exist.

`midi` exports the arrangement as SMF type 1 at 960 PPQ, with tempo, meter, key signature, one track per Cicada track, GM drum notes, swing and ratchets in tick positions, and one-tick overlap for slides. `--pattern <name>` exports a single loop instead. Probability uses the first seeded pass on every repetition. `verify-midi` decodes, re-encodes, and compares normalized tempo and note events. `compare-midi <a.mid> <b.mid>` compares those events across files; [first-acid.mid](testdata/golden/first-acid.mid) pins the encoder's reference output. `import-midi` reports that pattern import is scheduled for M6.

The [eleven-lane drum kit](examples/drums-kit.cicada) uses every built-in voice: `bd sd ch oh cp rs lt mt ht cb cy`. Render it with `go run ./cmd/cicada render examples/drums-kit.cicada -o drums-kit.wav --bars 1 --tail 0s`. The [authored kit](examples/authored-kit.cicada) maps `bd` to instrument code and `ch` to a built-in recipe; omitted lanes are silent. Render it with `go run ./cmd/cicada render examples/authored-kit.cicada -o authored-kit.wav --bars 1 --tail 0s`.

The [drive insert example](examples/fx/drive-insert.cicada) declares `fx drive` with `shape`, `gain`, `tone`, and `mix`, then sets `insert = drive` on the bass track. Drive supports soft, hard, fold, and diode shapes. The same score loads into the live native/TinyGo engine and renders to WAV; tracks without the insert are latency aligned. Render it with `go run ./cmd/cicada render examples/fx/drive-insert.cicada -o drive-insert.wav --bars 4`. The [delay send example](examples/fx/delay-send.cicada) routes bass through send A to a tempo-synced stereo delay. The [FX bus example](examples/fx-bus.cicada) routes bass and drums through send B to an eight-line reverb, alongside drive and delay. The [compressor bus example](examples/fx/compressor-bus.cicada) ducks the music bus from a drum track sidechain. The [SFX bus example](examples/sfx-bus.cicada) routes drums to SFX and uses that bus as the music sidechain. Track compressor inserts and authored effect graphs remain future work.

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
