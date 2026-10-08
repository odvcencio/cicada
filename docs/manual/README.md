# Cicada User Manual

This manual takes you from your first score to editing, arranging, and exporting
music in Cicada. You write a score as text, then use Studio's grids and
arrangement views to edit the same source.

The [Cicada Language Specification](../spec/README.md) documents source
editions 1 and 2, with edition 2 used by new projects.

For game integration, see [Game Director and the Go/JS SDKs](game-director.md).

## Start here

### Install and hear a score

Cicada requires Go 1.25 or newer and `make`. Clone the public repository and
build the command-line tool and the Studio workstation:

```sh
git clone https://github.com/odvcencio/cicada.git
cd cicada
make build
./build/cicada validate examples/first-acid.cicada
./build/cicada play examples/first-acid.cicada
```

`make build` writes `build/cicada`, `build/cicada-workstation`, and the
workstation's browser files in `build/workstation`. `cicada studio` starts the
workstation, so it looks for `cicada-workstation` next to `cicada` and then on
your `PATH`. If you move `cicada`, move those three together. The workstation
module pins Go 1.26.4, which Go downloads when it needs it. To build only the
command-line tool, run `make build-core`.

`play` opens the system audio device and loops the score. Press Ctrl-C to stop.
If you want a file instead, `render` writes a WAV without opening an audio
device; see [Exporting](exporting.md).

### Start a project

`new` makes a directory with a manifest and `main.cicada`. From the repository
root, run:

```sh
./build/cicada new night-circuit
cd night-circuit
../build/cicada check
../build/cicada studio main.cicada
```

The manifest selects edition 2, so `main.cicada` does not need an edition
header. A loose score without a manifest still defaults to edition 1. Run
`cicada fix score.cicada` to migrate legacy spellings; if other edition-1
scores share the project folder, use `cicada fix --all` to migrate them
together.

## Manual chapters

- [Writing music](writing-music.md) — build a score from tracks, patterns,
  pitches, drum rows, scenes, and songs.
- [Studio](studio.md) — edit source and grids, launch scenes, arrange the song,
  and inspect edit history.
- [Editors and notation tools](editors.md) — use the LSP, VS Code extension,
  formatter, quick fixes, highlighting, and symbol lookup.
- [Managing libraries](libraries.md) — create, inspect, pin, and vendor reusable
  declarations and audio; copy projects with Save As.
- [Exporting](exporting.md) — render WAV, stems, and MIDI.
- [Live playback and mixing](next-level.md) — use native audio, host macros, MIDI, and mixer settings.
- [Hosting the engine](engine-host.md) — integrate Cicada's Go or TinyGo audio
  engine.
- [Engine scaling measurements](engine-metrics.md) — run synthetic sessions
  and compare CPU and offline render costs between builds.
- [Troubleshooting](troubleshooting.md) — diagnose notation, editor, playback,
  and export problems.

The manual's `cicada` examples are checked by the real score validator in the
Go test suite. The specification explains the [example checks and
conformance](../spec/semantic-model.md#conformance).
