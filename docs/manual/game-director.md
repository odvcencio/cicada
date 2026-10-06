# Drive game music with Game Director

Declare game states, transition timing, and one-shot cues in an edition-2 score.
The Go and JavaScript SDKs resolve names to the same 24-byte kernel commands.
The audio thread schedules their landings at exact musical sample boundaries.

```text
live {
  land = bar
  phrase = 8bars
  macro intensity = 0.3 smooth 400ms
  state explore = calm
  state combat = fight
  stinger pickup = cue.hit quantize beat crossfade 10ms
  transition explore -> combat quantize bar crossfade 200ms
  transition combat -> explore quantize phrase crossfade 400ms
}
```

See [the complete score](../../examples/game-director.cicada) for its tracks,
patterns, scenes, and macro-controlled layers. `state` maps a game state to a
scene. A transition names two states; its rule uses the last state reported by
the kernel. Before the first landing, or when no rule matches, `land` applies
(default: bar) with no crossfade. A queued request does not change observed state.

`quantize` accepts `beat`, `bar`, or `phrase` and defaults to bar. Phrase landing
requires `phrase` (1–64 bars). A request on a boundary lands there; a request
between boundaries lands on the next one. `crossfade` accepts milliseconds or
seconds, defaults to zero, and is limited to 60 seconds. The host rounds it once
to sample frames and includes that integer in the shared manifest.

Transitions crossfade tracks linearly: outgoing patterns keep playing until
the fade ends, and incoming tracks fade in from silence. Shared tracks use the
normal pattern switch and parameter smoothing. Put different patterns on
separate tracks when you want their audio to overlap. Music bus effects retain
their tails. A state launch takes control from the score's automatic song.

Stingers play the named pattern once from its first step, then stop. They use
dedicated tracks that are off or omitted in every scene and have no macro layer
rule. The fade applies at the beginning and end of the pattern; short patterns
may begin fading out before their attack completes. Retriggering replaces the
previous cue on that track and reports its end before reporting the new start.
Stop and seek cancel active stingers and queued director commands. Commands
with the same landing tick run in submission order; the final state wins.

## Minimal Go game

The Go package is `m31labs.dev/cicada/sdk/director`, shipped in this module.
Load and compile the score outside the audio callback, then build a manifest:

```go
surface, err := project.DirectorSurfaceOf(p, 48000)
if err != nil { return err }
game, err := director.New(surface, enqueue)
if err != nil { return err }
if err := game.Setup(); err != nil { return err }
if err := game.SetState("explore", 0); err != nil { return err }
// Start the engine with OpPlay after setup.

// Game events:
if err := game.SetMacro("intensity", 0.9, 0); err != nil { return err }
if err := game.SetState("combat", 0); err != nil { return err }
if err := game.TriggerStinger("pickup", 0); err != nil { return err }

// Forward drained kernel messages to the SDK:
game.Handle(message)
```

`enqueue` has type `func(cmd.Command) error`. It must enqueue a record or report
an error. A real game's bounded queue transfers records to the goroutine that
owns `Engine.Push` and `Engine.Render`; do not call them concurrently. Keep SDK
calls and `Handle` on one goroutine too. Setup installs macros, layers, and phrase
length before playback. If setup fails partway, reset and set up a fresh engine.

Run [the complete Go game loop](../../examples/game-director/main.go):

```sh
GOWORK=off go run ./examples/game-director -manifest build/game-director.json
```

It logs eight bars, enters combat, raises intensity, and triggers a pickup cue.
The output JSON is the control manifest for JS. Compile the same project to a
kernel image with `project.CompileEngine` and `kernelimage.Encode` for the browser.

## JavaScript and TypeScript

The MIT package in [`sdk/js`](../../sdk/js) is `@cicada/game-director`, version
0.1.0. It has no runtime dependencies and includes TypeScript declarations.
Use `npm pack ./sdk/js` to create an installable tarball; install that tarball in
a game. Package registry publication is separate from the repository release.

```ts
import { GameDirector, workletSender, decodeMessages } from '@cicada/game-director';

const game = new GameDirector(manifest, workletSender(musicNode.port));
game.setup();
game.setState('explore'); // Start OpPlay after setup.
game.setMacro('intensity', 0.9);
game.setState('combat');
game.triggerStinger('pickup');

for (const message of decodeMessages(messageBytes)) game.handle(message);
```

[The browser game example](../../examples/game-director/game.mjs) initializes a
worklet, returns its reusable message buffer, starts playback, and exposes
`enterCombat`, `leaveCombat`, and `collectPickup` game events. Supply the kernel
module, project image, manifest, and processor URL. The audio context and manifest
must use the same sample rate. `workletSender` targets the existing `t: 'c'`
command protocol. Supply a custom sender for another engine transport; it
receives one `Uint8Array` command and must throw if it cannot enqueue it.

| Go | JavaScript | Meaning |
| --- | --- | --- |
| `SetState(name, tick)` | `setState(name, tick?)` | Quantized state-to-scene request |
| `SetMacro(name, value, tick)` | `setMacro(name, value, tick?)` | Macro target 0–1 with score smoothing |
| `TriggerStinger(name, tick)` | `triggerStinger(name, tick?)` | Quantized one-shot cue |
| `Handle(message)` | `handle(message)` | Update landed state and layer observation |
| `State()` | `state` | Last acknowledged game state |
| `LayerMask()` | `layerMask` | Last observed effective track mask |

Tick zero means submit now. Positive ticks are absolute transport ticks, at 960
per quarter note and 3,840 per bar. Use `bigint` in JS for exact 64-bit ticks.
Queue events before their requested time; late arrivals report `Late` and cannot
retroactively change audio. SDK calls validate names, ranges, and ticks before
sending. The manifest owns IDs, sample rate, smoothing frames, and fade frames;
use the same manifest across clients.

Forward all messages, including `Fault`, `Overload`, and `Late`, to game telemetry.
The SDK updates observations and leaves delivery and error reporting to the host.
The layer mask initially includes all tracks; the first bar reports the
configured mask through `LayerChanged`, which also reports conductor changes. Its bits gate authored tracks, including tracks whose scene is off.
The mask alone does not indicate which scene patterns are currently playing.

## Command and message additions

The command and message records retain their 24-byte and 16-byte layouts.
All reserved bytes remain zero. Native and WASM use identical records.

| Record | Payload |
| --- | --- |
| `OpSetState` (23) | Track 255; Index state ID 0–63; Arg0 scene ID in low 16 bits, quantize in high 16 bits; Arg1 crossfade frames |
| `OpTriggerStinger` (24) | Track cue track; Index pattern slot; Arg0 quantize; Arg1 attack/release fade frames |
| `StateChanged` (13) | A state ID; B scene ID; Tick actual landing |
| `StingerStarted` (14) | Track cue track; A pattern slot; Tick actual landing |
| `StingerEnded` (15) | Track cue track; A pattern slot; Tick end or replacement |

Quantize values are 0 now, 1 beat, 2 bar, and 21 live phrase. The SDK also preserves
legacy `land = 2bars` and `4bars` with values 6 and 8. The existing `Bar`,
`PhraseEnd`, `LayerChanged`, and `MacroReached` messages keep their meanings.
Old kernels reject the new opcodes; deploy the matching kernel with the SDK.
No project-image version change is required; setup travels as commands.

## Check host interoperability

Run `GOWORK=off make test-director`. The check runs the shipped Go and JS APIs
against native and WASM engines as four independent clients for 64 bars.
Each client consumes its own messages and logs bar, landed state, and layer
mask. The test compares every row and control event, exercises rising and falling
macro layers, beat stingers, bar/phrase state changes, and crossfades, and compares
PCM within the existing native/WASM tolerance. It checks zero WASM render
allocation bytes and stable memory. Separate tests require block-invariant
native PCM, exact fade endpoints, sample-accurate cue onset/end, and zero native
render allocations. `make budget-size` enforces the existing kernel limits.
