# CLI reference

Run `cicada help` for the command list, `cicada help COMMAND` for command usage,
or `cicada help lib COMMAND` for a library subcommand. Each command also accepts
`--help`.

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
