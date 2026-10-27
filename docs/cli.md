# CLI reference

Run `cicada help` for the command list, `cicada help COMMAND` for command usage,
or `cicada help lib COMMAND` for a library subcommand. Each command also accepts
`--help`.

## Audition

`cicada audition SCORE [-o DIR] [--from BAR] [--bars N] [--json]` renders the
mix and stems and reports the heard key, written and heard chords per bar,
per-track level, brightness, attack, decay and rhythm, and per-section
loudness. With `-o` it writes `spectrogram.png`, `chroma.png` and
`rhythm.png`. See [Audition](manual/audition.md) for each field and the JSON
schema.

## Sampler pack memory

`cicada render SCORE -o FILE`, `cicada stems SCORE -o DIR` and `cicada play
[SCORE]` take `--max-resident-pcm SIZE`. It caps the decoded PCM of all sampler
packs the score loads, 1 GiB by default. SIZE is a whole number with a unit:
`KiB`, `MiB`, `GiB` or `TiB`, or `KB`, `MB`, `GB` or `TB`. A score over the cap
is refused before it renders, and the message names the cap and the size
measured. `cicada check` loads the packs and reports a note outside every zone
of its pack, but it does not apply the cap. See
[instrument packs](sampler/packs.md#memory-limits).

```sh
cicada render song.cicada -o song.wav --max-resident-pcm 2GiB
```

## Libraries

Run library commands from the project folder, except `new`, which needs no
project. Library paths use slash-separated namespace identifiers.

| Command | Behavior |
| --- | --- |
| `cicada lib list` | List available embedded, project, and user libraries with their resolution kinds. Includes duplicate paths; does not change pins. |
| `cicada lib show PATH` | Print the resolved manifest, declarations, audio paths, SHA-256 hash, and resolution directory. Does not change pins. |
| `cicada lib new PATH [--dir DIR]` | Create an edition-2 manifest and starter source in the user library. `--dir` selects a directory while keeping the library path. Absolute or dot-prefixed `PATH` selects a directory and uses its final name. Refuses existing destinations and the `std` namespace. |
| `cicada lib update [PATH]` | Accept current bytes and resolution kind for one imported library, or all imports when omitted. Prints old/new pins; removes unused pins when updating all. |
| `cicada lib vendor` | Verify pins, stage and verify imported user libraries in project `lib/`, and publish project pins. Includes transitive imports; refuses different existing libraries. |

`CICADA_LIBRARY` overrides the default user config directory's `cicada/lib`
folder. Library identity uses hashes; remote fetching and version requirements
are not supported. See [Managing libraries](manual/libraries.md) for examples
and pinning behavior.

## Save As

`cicada save-as SCORE TARGET` copies the score, project sources, audio,
retained takes, history, and imported libraries. It automatically vendors user
and project libraries and writes target pins before publishing the score.
Dependencies with different content and existing target scores are refused.
Studio Save As uses the same operation. Bundle is not available in this build.

## Inspect a musical moment

`cicada explain SCORE PATH @bar[.beat[.step]]` reports the parameter's default,
preset and track overrides, inherited scene setting, and active song automation.
The computed value includes the automation lane once it reaches its first point;
the lane holds its last value and takes precedence over later scene settings.
Positions use 4/4: bars start at 1, and beat and sixteenth values are 1–4.

```sh
cicada explain examples/songs/neon-causeway.cicada bass.cutoff @15
cicada explain examples/songs/neon-causeway.cicada bass.cutoff @17
```

The first command inspects the build; the second reaches its 2200 Hz target.
Reported automation values are float32 control targets; audio smoothing can lag
the target. Named-placement arrangements support parameter inspection within
their whole-bar export span; their live scene definitions do not contribute
settings to the arrangement.
