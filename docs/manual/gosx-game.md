# Use a score in a GoSX game

A GoSX game can play a Cicada score live, in the browser, and switch it between
game states on the beat. The score compiles at build time. The page ships one
kernel module (the Cicada engine as WebAssembly) and one score file, and runs
the kernel inside an AudioWorklet.

GoSX does not depend on Cicada. It supplies a generic host for a WASM audio
kernel (package `game/audio/kernel` in `m31labs.dev/gosx`). Cicada supplies the
kernel, the score file, and a controller. Neither project imports the other.

## What you ship

| File | Made by | Size (example) |
| --- | --- | --- |
| `cicada-kernel.wasm` | `make build-kernel-wasm` | 329,587 bytes raw, 120,013 bytes Brotli |
| `music.cscore` | `cicada game-score` | 3,976 bytes for `examples/game-music.cicada` |

The kernel is the same module for every score that needs no optional features.
`cicada game-score` prints which module a score needs. Scores that use track
effects, keys, or modulators need `cicada-vocals.wasm`, `cicada-keys.wasm`,
`cicada-modular.wasm`, or `cicada-keys-modular.wasm`; build them with the
matching `make` target. The compiler does not run in the browser.

## 1. Write a score with a live block

The live block names the game states, the intensity macro and the stingers.
[`examples/game-music.cicada`](../../examples/game-music.cicada) is a complete
example. Its core is:

```text
live {
  land = bar
  phrase = 4bars
  macro intensity = 0.3 smooth 600ms
  layers intensity { drums >= 0.25  lead >= 0.70  attack 1bar  release 2bars }
  state explore = calm
  state combat = fight
  stinger pickup = cue.chime quantize beat crossfade 10ms
  transition explore -> combat quantize bar crossfade 300ms
  transition combat -> explore quantize phrase crossfade 800ms
}
```

A state maps to a scene. A transition says where a change lands (`beat`, `bar`
or `phrase`) and how long the crossfade lasts. The game asks for a change at any
frame; the kernel holds it until the boundary and applies it to the sample. See
[Game Director](game-director.md) for the full rules.

## 2. Build the score file

```sh
make build-kernel-wasm
./build/cicada game-score examples/game-music.cicada -o assets/music.cscore
cp build/cicada-kernel.wasm assets/
```

`--rate` picks 44100, 48000 (the default) or 96000 Hz. The file holds the compiled
kernel image and the control manifest at that one rate, so the game's
AudioContext must run at it. The pack starts with the magic `CICSCORE`, ends with
a CRC-32, and `gosxhost.ParsePack` refuses a damaged file.

## 3. Play it in the game

The game's `js/wasm` build imports `m31labs.dev/cicada/sdk/gosxhost`, which pulls
in only the kernel command codec and the Director manifest types (about 4
packages; a test pins this). It also imports GoSX's `game/audio` and
`game/audio/kernel`.

```go
//go:build js && wasm

import (
	"encoding/json"

	"m31labs.dev/cicada/sdk/gosxhost"
	"m31labs.dev/gosx/game/audio"
	"m31labs.dev/gosx/game/audio/kernel"
	"m31labs.dev/gosx/game/audio/worklet"
)

func startMusic(score, kernelWASM []byte) (*gosxhost.Music, error) {
	pack, err := gosxhost.ParsePack(score)
	if err != nil {
		return nil, err
	}
	var backend worklet.Backend
	if err := json.Unmarshal(gosxhost.BackendJSON(), &backend); err != nil {
		return nil, err
	}
	host, err := audio.NewHostJSWithOptions(audio.HostOptions{SampleRate: float64(pack.SampleRate)})
	if err != nil {
		return nil, err
	}
	mixer := audio.NewMixer(host) // buses: master, music, sfx, voice, ui
	tempo, caps := pack.Info()
	node, err := kernel.Start(kernel.Config{ // run in a goroutine, after a click
		Mixer:           mixer,
		Bus:             audio.MusicBus,
		Backend:         backend,
		WASM:            kernelWASM,
		Image:           pack.Image,
		Info:            worklet.ImageInfo{TempoMilli: tempo, RequiredCapabilities: caps},
		ProcessorPrefix: "/assets/", // where the game serves worklet.Handler()
	})
	if err != nil {
		return nil, err
	}
	music, err := gosxhost.NewMusic(pack, node.Send)
	if err != nil {
		return nil, err
	}
	node.OnMessages(func(records []byte) { _ = music.HandleMessages(records) })
	node.OnFault(func(code uint16) { /* report to telemetry */ })
	return music, music.Start("explore")
}
```

Serve `worklet.Handler()` from your server under the same prefix
(`ProcessorPrefix`). Fetch the kernel and the score with `audio.HTTPLoader` or
`fetch`, and resume the context from a click: `host.Resume()`.

The controller:

| Call | Effect |
| --- | --- |
| `music.Start(scene)` | Installs macros and layers, requests `scene`, starts the transport. |
| `music.SetScene("combat")` | Changes game state. Lands on the boundary the score's transition names. |
| `music.SetIntensity(0.8)` | Sets the `intensity` macro (0 to 1); layers fade in and out at their thresholds. |
| `music.SetMacro(name, v)` | Any other macro. |
| `music.Stinger("pickup")` | Plays a one-shot cue on the next beat (or as the score says). |
| `music.Stop()` | Stops the transport. |
| `music.Scene()` | The scene that has landed. It still reports the old scene until the change lands. |
| `music.Bar()`, `music.Layers()`, `music.Faults()` | Last bar line, track mask and fault codes. |

Call the controller from the game's update loop; each call posts one small
batch of 24-byte command records to the worklet. Music volume belongs to the
mixer: `mixer.SetVolume(audio.MusicBus, 0.6)`, and
`mixer.Duck(audio.MusicBus, "voice-line", 0.4, 0.2)` lowers the music while a
voice line plays.

## Replace the score

`node.Replace(newPack.Image, info)` swaps in a different score. With the Cicada
backend the swap lands on the next bar line with a 5 ms crossfade. The processor
computes that bar line from the opening tempo of the new image, so a score whose
scenes change tempo can land a few milliseconds off; scene changes inside one
score are not affected.

## Check it yourself

`sdk/gosxhost` has a test that plays one control timeline (start, intensity, a
scene change, a stinger, a return) twice: through the native engine and through
GoSX's processor running the TinyGo kernel. It compares every sample.

```sh
make build-kernel-wasm
GOSX_WORKLET_PROCESSOR=/path/to/gosx/game/audio/worklet/processor.js \
  GOWORK=off go test ./sdk/gosxhost -run TestWorkletMatchesNativeKernel -v
```

The test skips when the processor path or the kernel is missing. Measured on
2026-11-08 for both example scores (7,500 quanta, 20 seconds, 960,000 samples
per channel): 0 differing samples, maximum difference 0, and the message streams
(bars, scene landings, layer changes, stingers) identical. The harness runs the
processor under Node's `vm`; it does not run a browser audio thread.

## Limits

- One sample rate per pack, and the context must match it.
- At most 16 tracks and 32 voices (the portable kernel limit); `cicada check
  --profile browser` shows each track's share.
- `music.Start` must run after the worklet reports ready. `kernel.Start` waits
  for that.
- The kernel engine allocates nothing while rendering. GC pauses in the game's
  Go code do not touch the audio thread.
