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

`cicada render SCORE -o FILE`, `cicada stems SCORE -o DIR`, `cicada play
[SCORE]`, `cicada audition SCORE`, `cicada check [SCORE]` and `cicada studio
[SCORE]` take `--max-resident-pcm SIZE`. It caps the decoded PCM of all sampler
packs the score loads, 1 GiB by default. When the option is omitted, the
environment variable `CICADA_MAX_RESIDENT_PCM` sets the cap, which is how to
give Studio another cap when it starts from a launcher that passes no flags.
SIZE is a whole number with a unit: `KiB`, `MiB`, `GiB` or `TiB`, or `KB`, `MB`,
`GB` or `TB`, in capitals or in lower case (`Mb` is refused, because it can
read as megabits). The cap is counted from the pack manifests before any audio
is read, so a score over the cap is refused without allocating its audio, and
the message names the cap and the size. `cicada check` reports the same
refusal as `CICADA-SAMPLER-MEMORY`, loads the packs one at a time, and reports a
pack that does not load at 44.1, 48 or 96 kHz and a note outside every zone of
its pack. See [instrument packs](sampler/packs.md#memory-limits).

```sh
cicada render song.cicada -o song.wav --max-resident-pcm 2GiB
```

## CPU profiles

`cicada render`, `cicada stems`, `cicada apply`, `cicada check` and `cicada
audition` take `--cpuprofile FILE`. The command writes a standard Go CPU
profile of its whole run to FILE, also when it fails (a loudness shortfall
included), and its other output does not change. Read the profile with `go tool
pprof`:

```sh
cicada render song.cicada -o song.wav --loudness -14 --cpuprofile render.prof
go tool pprof -top render.prof
```

## Loudness targets and repeated renders

`cicada render --loudness` searches for the export gain in up to six passes.
The first pass synthesizes the song; every later pass replays that pass's mix
from a temporary file next to the output (8 bytes per frame, about 71 MB for a
three-minute song at 48 kHz) and runs only the master stage, the measurement
and the WAV writing again. A pass whose gain would not change the audio is not
rendered at all. The output and the reported numbers are byte for byte those of
a search that synthesizes every pass. The summary line counts the passes by how
they got their audio, and one line per pass gives its gain, loudness and true
peak:

```text
song.wav: 40 bars from bar 1, 4574770 frames at 48000 Hz, achieved -15.05 LUFS, true peak -1.05 dBTP, applied gain +11.41 dB, DC correction L +0.0000000/R +0.0000000 FS, passes 2 (full syntheses 1, cached replays 1, repeats 0), largest limiter gain reduction 0.00 dB
song.wav: pass 1: gain +0.00 dB, -26.45 LUFS, true peak -12.46 dBTP, synthesized
song.wav: pass 2: gain +11.41 dB, -15.05 LUFS, true peak -1.05 dBTP, replayed
```

`render --json` reports the same counts and passes (see
[Agents and JSON output](manual/agents.md#render---json)). `--normalize` renders
twice in the same way: its second render replays the first one's mix. When the
temporary file cannot be written, for example on a full disk, every render runs
in full.

Set `CICADA_RENDER_NO_REUSE=1` to turn all reuse off: every loudness pass and
both normalization renders synthesize the song in full, and the counts show
only syntheses. The output must be the same byte for byte; the variable is
there to work around a difference you find, and to show whether reuse caused
it.

```sh
CICADA_RENDER_NO_REUSE=1 cicada render song.cicada -o song.wav --loudness -14
```

On Linux and macOS the stored mix has no name in the folder, so its space
returns however the render ends. On Windows it keeps its name until the render
ends. If a render is interrupted (Ctrl-C), it removes its temporary files
before it exits; files that a killed render leaves behind (names that start
with `.cicada-render-`, `.cicada-loudness-` or `.cicada-master-cache-`) are
removed by the next render in that folder once they are a day old.

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
Studio Save As uses the same operation.

`cicada bundle SCORE OUTPUT.zip` creates a portable ZIP containing the Save As
copy and a `cicada-bundle.json` inventory of file hashes and bundled library
identities. It refuses an existing output. Retained takes and recovery files
are included and may contain unpublished work; see [Managing libraries](manual/libraries.md)
for their portability limits. After extraction, run
`cicada bundle --restore-history main.cicada` to attach inventoried recovery
versions without overwriting different files.

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
