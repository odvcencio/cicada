# Cicada User Manual

This manual takes you from your first score to editing, arranging, and exporting
music in Cicada. You write a score as text, then use Studio's grids and
arrangement views to edit the same source.

The reference for every edition-1 construct, field, unit, range, default, and
diagnostic is the [Cicada Language Specification](../spec/README.md). Syntax
marked **accepted** there is under review and is not available in the current
build.

## Start here

### Install and hear a score

Cicada requires Go 1.25 or newer. Clone the public repository and build the
command-line tool:

```sh
git clone https://github.com/odvcencio/cicada.git
cd cicada
go build -o cicada ./cmd/cicada
./cicada validate examples/first-acid.cicada
./cicada play examples/first-acid.cicada
```

`play` opens the system audio device and loops the score. Press Ctrl-C to stop.
If you want a file instead, `render` writes a WAV without opening an audio
device; see [Exporting](exporting.md).

### Start a project

`new` makes a directory with a manifest and `main.cicada`. From the repository
root, run:

```sh
./cicada new night-circuit
cd night-circuit
../cicada check
../cicada studio main.cicada
```

The manifest sets the project name and edition. Your score can omit the
`cicada 1` header; existing headered scores still work. A score outside a
project also defaults to edition 1.

## Manual chapters

- [Writing music](writing-music.md) — build a score from tracks, patterns,
  pitches, drum rows, scenes, and songs.
- [Studio](studio.md) — edit source and grids, launch scenes, arrange the song,
  and inspect edit history.
- [Editors and notation tools](editors.md) — use the LSP, VS Code extension,
  formatter, quick fixes, highlighting, and symbol lookup.
- [Exporting](exporting.md) — render WAV, stems, and MIDI.
- [Live playback, mixing, and what comes next](next-level.md) — what the
  current engine does and which larger features are still ahead.
- [Hosting the engine](engine-host.md) — integrate Cicada's Go or TinyGo audio
  engine.
- [Troubleshooting](troubleshooting.md) — diagnose notation, editor, playback,
  and export problems.

The manual's `cicada` examples are checked by the real score validator in the
Go test suite. The specification explains the [example checks and
conformance](../spec/semantic-model.md#conformance).
