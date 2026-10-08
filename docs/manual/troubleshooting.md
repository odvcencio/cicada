# Troubleshooting

Start with the command that reports the source location and stable diagnostic:

```sh
cicada check
cicada check main.cicada
cicada validate main.cicada
```

`check` validates all scores under the nearest `cicada.mod`. `validate` checks
one score. Both parse and compile the project; a score can be grammatically
valid but still fail a name, type, unit, range, or engine check.

## Notation errors

- **`CICADA-SYNTAX`** — check braces, spelling, separators, and whether the
  construct exists in edition 2; see the [language reference](../spec/README.md).
- **`CICADA-REFERENCE`** — check that a track, pattern, scene, instrument, kit,
  or phrase name exists and is spelled consistently.
- **`CICADA-PARAM`** or **`CICADA-UNIT`** — check the field's range and unit in
  the [edition-1 reference](../spec/edition-1.md). For example, frequency uses
  `Hz` or `kHz` and time uses `ms` or `s`.
- **`CICADA-SCALE-DEGREE`** — degrees 2 and 6 are not part of the `pent` or
  `blues` scale. Use 1, 3, 4, 5, or 7, or choose another scale.
- **`CICADA-SLIDE-REST`** — a slide has no adjacent playable target. Check the
  next cell, pattern loop, and any scene switch that should provide the note.
- **Too many or mismatched steps** — each pitch pattern has 1–64 expanded
  cells; drum rows in a pattern must have equal lengths. A declared `steps`
  value is a check and must match the expanded pattern.

## Studio does not start

- **`GoSX workstation executable is missing`** — Studio runs as two programs,
  `cicada` and `cicada-workstation` (`cicada-workstation.exe` on Windows).
  `cicada studio` looks for the workstation next to `cicada`, then on `PATH`.
  `go build ./cmd/cicada` builds only `cicada`. From the repository root, run
  `make build`, then start Studio with `./build/cicada studio main.cicada`;
  both programs are in `build/`. If you move `cicada`, move
  `cicada-workstation` and the `workstation` folder with it, because the
  workstation reads its browser files from that folder. The VS Code extension
  needs the same files, so set `cicada.serverPath` to the absolute path of
  `build/cicada`.

## Studio or editor does not update

- If a source save is rejected, read the status line. Studio validates before
  writing; correct the error or discard the draft.
- If Studio says the file changed on disk, reload the page and combine your
  changes with the external edit before trying again.
- If the LSP does not start, confirm that `cicada lsp` runs in a terminal and
  that your editor launches the correct binary. In VS Code, set
  `cicada.serverPath` when the executable is not on `PATH`.
- When Studio is open beside VS Code, save one editor at a time. Studio and
  the extension exchange changes through the score file.
- Windows Studio needs a filesystem that supports its atomic replacement
  operation. A WSL UNC share can report `ReplaceFileW: The request is not
  supported`; the score remains unchanged. Run Studio inside WSL for that
  checkout, or use a Windows filesystem for a native Windows score.

## No sound or unexpected sound

- On Windows and Linux, `cicada play` and Studio use Tymbal by default: WASAPI
  on Windows and ALSA on Linux. If Tymbal cannot open a device, Cicada names it
  and suggests `--audio oto`. On Linux, no ALSA default device or a device held
  by PipeWire or Pulse can cause this error. Use `--audio oto` or set
  `CICADA_AUDIO=oto` to select Oto explicitly. Use `--audio null` for real-time
  transport without an output device. macOS uses Oto by default.
- To check a score without live playback, render a WAV.
- If WSL has no ALSA playback device, use a native Windows build with Tymbal
  and WASAPI to audition the instruments. Keep its editable score on a Windows
  filesystem, as described above.
- If playback continues after an invalid save, Studio or `play` is keeping the
  last valid compiled score. Correct the reported diagnostic and save again.
- If a scene sounds incomplete, check that each track has a compatible
  pattern assignment. A missing track assignment means keep its previous
  pattern; it does not mean stop.
- If a sound is too quiet or clips, inspect track levels and the render summary.
  `--normalize` sets the file's sample peak; it does not balance the mix or
  control perceived loudness.

## Export problems

- A stem export's destination directory must not already exist. Choose a new
  name, then run `verify-stems` on the completed directory.
- When checking a WAV range, pass the same `--from`, `--bars`, and `--tail`
  values to `verify-wav` that you used for `render`. Bar numbers start at 1;
  `--from 1` selects the first bar. The old `--from 0` input still selects bar
  1 and prints a deprecation warning.
- MIDI chance output is deterministic: the exporter uses the first seeded
  pass on each repetition. It does not encode a live probability generator.
- If a typed JSON field is rejected, inspect the public schema and field
  catalog using `cicada fields` or the links in
  [Semantic JSON](../spec/semantic-model.md#semantic-json).
