# AGENTS.md

This folder is a Cicada project: `main.cicada` is the song, written as a text score,
and `cicada.mod` makes the folder a project in edition 2 of the language.

- `cicada agents` prints the quick start for the installed `cicada`, and
  `cicada agents reference` the reference: every command, edit intent and
  diagnostic code.
- Check after every edit: `cicada check --json main.cicada`, until `valid` is
  true.
- Change the score one revision-checked batch at a time:
  `cicada apply main.cicada edit.json --write --json`, with the `revision` that
  the last `check --json` or `apply` gave. A batch whose revision is stale is
  refused and changes nothing.
