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
  construct exists in edition 1. The [accepted syntax list](../spec/accepted.md)
  is not available in the current build.
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

## No sound or unexpected sound

- `cicada play` uses the native audio device. Check that the operating system
  has an output device selected and that the device is not held by another
  process. To test the score without live playback, render a WAV.
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
  values to `verify-wav` that you used for `render`.
- MIDI chance output is deterministic: the exporter uses the first seeded
  pass on each repetition. It does not encode a live probability generator.
- If a typed JSON field is rejected, inspect the public schema and field
  catalog using `cicada fields` or the links in
  [Semantic JSON](../spec/semantic-model.md#semantic-json).
