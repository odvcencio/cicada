# AGENTS.md

This folder is a Cicada project: `main.cicada` is the song, written as a text score,
and `cicada.mod` makes the folder a project in edition 2 of the language.

- `cicada agents` prints the quick start for the installed `cicada`, and
  `cicada agents reference` the reference: every command, edit intent and
  diagnostic code.
- Check after every edit: `cicada check --json main.cicada`, until `valid` is
  true.
- Change the score one revision-checked batch at a time with
  `cicada apply main.cicada edit.json --write --json`. Set `revision` in
  `edit.json` to `scores[0].revision` from the last
  `cicada check --json main.cicada`, to `revision` from
  `cicada song --json main.cicada`, or to `revision_after` from the last
  `apply --write`. A batch whose revision is stale is refused and changes
  nothing. Without `--write`, `apply` prints the diff and writes nothing.
