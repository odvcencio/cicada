# Cicada standard libraries

These MIT-licensed libraries ship as source embedded in the Cicada host binary.
Import a library, use its qualified names, then run `cicada lib update` from your
project directory to pin the exact bytes:

```cicada
cicada 2
import "std/synth"
track bass synth.glassbass { cutoff = 680Hz }
```

The layout groups related declarations into five libraries. Each directory has
its own `cicada.mod`, source edition 2, license `MIT`, and author `Cicada project`.

| Import | Public declarations | Source |
| --- | --- | --- |
| `std/synth` | `glassbass`, `nightbass`, `tymbal`, `subline`, `glass` instruments | `examples/glassbass.cicada`, `examples/cicada-chorus.cicada`, `examples/circuit-kit.cicada`, `examples/multifile/parts/voices.cicada` |
| `std/drums` | `kick`, `circuit-kick`, `snare`, `hat` instruments; `steel`, `circuit` kits | `examples/authored-kit.cicada`, `examples/circuit-kit.cicada` |
| `std/fx` | `drive`, `delay`, `reverb`, `comp` effects | `examples/fx-bus.cicada`, `examples/fx/compressor-bus.cicada` |
| `std/keys` | 21 original native keyboard presets: electric pianos, Clav, tonewheel organ, FM, analog poly keys, ensemble strings | [Keyboard patches](keys/README.md) |
| `std/presets` | `acid-squelch`, `acid-round`, `acid-bite`; `kit-tight`, `kit-roomy`, `kit-lofi` | Acid voice and built-in drum kit parameter values |

The graph voices preserve their example definitions. `circuit-kick` renames the
Circuit kit's kick so it can coexist with the Authored kit's pitched `kick`.
`steel` keeps its original bindings; `circuit` groups the three Circuit kit
voices. `glassbass` uses the Glassbass study's definition. Other examples have
score-specific variations of that voice.

The effects provide reusable defaults for a drive insert, delay and reverb sends,
and a music-bus compressor. Route them in the score to form a chain; libraries
cannot declare buses or track routing. The compressor uses the music bus as its
detector instead of depending on an example's track name. The kit presets target
built-in `drums`, whose lane recipe settings support presets. Authored kits retain
their graph definitions. `roomy` lengthens the built-in kit's decays; add the
standard reverb send when you also want space.

Resolution checks embedded std, project `lib/`, then the user library. A duplicate
path produces `CICADA-LIB-SHADOW`. `cicada lib list` shows paths and resolution
kinds, including duplicates; it does not change pins.

`cicada.sum` records `kind std` and a SHA-256 over the embedded manifest and source
bytes using the same sorted, length-prefixed format as other libraries. Libraries
are versioned with the binary. Upgrading Cicada leaves score pins unchanged. If
an imported std library's content changed, `cicada check` reports
`CICADA-LIB-HASH`, and playback and rendering refuse the changed content. Review
the change, then run `cicada lib update std/synth` (or omit the path for all
imports) to accept and re-pin it. A byte-identical std library needs no re-pin.

Try the new examples under `examples/std-synth`, `examples/std-drums`,
`examples/std-fx`, `examples/std-presets`, and `examples/keys/std`. Existing
examples remain unchanged.
The [manual](../../docs/manual/writing-music.md#standard-libraries) includes one
usage example for each library. Libraries compile on the host; they add no source
or bytes to the core WASM audio kernel. `std/keys` uses the separately loaded
keyboard module in the browser; its manifest requires capability bit 9 (512).

Planned additions after their PRs merge: the delay/comb pluck from #106, guitar
from #107, and PM voices from #108. These voices are not included here.
