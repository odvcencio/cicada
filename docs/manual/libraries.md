# Managing libraries

A library holds reusable instruments, patterns, phrases, presets, effects,
kits, samplers, and audio assets. Import it by its slash-separated path:

```text
import "demo/tone"
track lead tone.tone {}
```

## Create and inspect a library

```sh
cicada lib new demo/tone
cicada lib list
cicada lib show demo/tone
```

`new` creates a valid edition-2 `cicada.mod` and a starter instrument in
`main.cicada`. Edit the source and set the manifest's license and author before
sharing it. Names beginning with `_` are private. Library audio belongs under
`audio/` and asset declarations carry the same SHA-256 metadata as score assets.

The user library defaults to `os.UserConfigDir()` plus `cicada/lib`: the user
config folder on Linux, Application Support on macOS, and AppData on Windows.
Set `CICADA_LIBRARY` to use another directory. Paths in imports and manifests
always use `/`; the commands convert them to the operating system's disk paths.

To keep the full library name in a chosen directory, use:

```sh
cicada lib new demo/tone --dir ./lib/demo/tone
```

An absolute or dot-prefixed directory also works as `PATH`; its final directory
name becomes the library name. Existing destinations are refused. The `std`
namespace belongs to embedded libraries.

`list` reports all available library paths and their resolution kinds (`std`,
`project`, or `user`), including duplicate locations. `show` resolves one path
and prints its original manifest, declaration names (including private names),
audio file paths, content hash, and resolution directory. Neither changes pins.

## Pin an intended change

After adding an import, run these commands from the project folder:

```sh
cicada lib update
cicada check
```

`cicada.sum` records each imported library's resolution kind and SHA-256 hash
over its manifest, declared sources, and every audio file. Imports include
transitive dependencies. A changed or missing pin is a `CICADA-LIB-HASH` error;
the score cannot silently pick up a different sound. Inspect the change before
running `cicada lib update demo/tone` to accept it. With no path, `update` pins
all imported libraries and removes unused pins. Versions and remote fetching
are not available.

## Keep a project portable

```sh
cicada lib vendor
cicada check
```

`vendor` verifies the current pins, copies imported user libraries (including
transitive imports) into project `lib/`, verifies copied hashes, and changes
their pins from `user` to `project`. Hashes and source bytes stay the same.
Embedded standard libraries stay in the host binary.

Copies are staged before directory renames; the sum is published last. A
failure removes newly published copies and leaves the sum unchanged. An
existing library at the same path must have the same hash; different content
is refused. Repeating the command is safe. A project pin explicitly selects
its vendored library even while a user copy exists or changes. Unpinned
duplicate locations still produce a shadowing error.

Studio Save As and `cicada save-as` automatically copy imported user and project
libraries and write project pins before publishing the target score. The copy
then loads and renders without the original project or user library. There is
no `cicada bundle` command in this build; use Save As to copy a project.

See the [library CLI reference](../cli.md#libraries) for command syntax.
