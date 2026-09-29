# Cicada

Cicada is a music language and workstation. A score describes tracks, patterns,
scenes, and a song. New projects use source edition 2.

## Quick start

Build the command-line tool with Go 1.25 or newer, then create and check a
project:

```sh
go build -o cicada ./cmd/cicada
./cicada new night-circuit
cd night-circuit
../cicada fmt --check
../cicada check
```

The starter score has bass, drums, and two scenes. Open it in Studio:

```sh
../cicada studio main.cicada --audio null
```

The null backend lets you use Studio transport and meters without opening an
audio device. For WAV output, render the score:

```sh
../cicada render main.cicada -o mix.wav --bars 8
```

## Audio

On Windows and Linux, `cicada play` and Studio use Tymbal by default: WASAPI
shared mode on Windows and ALSA on Linux. Cicada reports device errors and does
not switch engines silently. Use `--audio oto` to select Oto explicitly, or
`--audio null` for real-time transport without an output device. macOS uses Oto
by default. `CICADA_AUDIO` selects a backend when `--audio` is omitted.

## Learn Cicada

- [User manual](docs/manual/README.md) — write scores, use Studio, and export
  audio.
- [Language specification](docs/spec/README.md) — edition 1 and 2 syntax,
  defaults, diagnostics, and accepted designs that are not yet available.
- [Examples](examples/) — complete Cicada scores, including
  [first acid](examples/first-acid.cicada) and
  [circuit kit](examples/circuit-kit.cicada).

Run `cicada help` for the command list or `cicada help COMMAND` for usage and
flags.

## License

MIT. See [LICENSE](LICENSE).
