# WAM2 instruments

Export a score as a Web Audio Modules 2 instrument, then load its `index.js`
in a WAM2 host:

```sh
make build-wam2
cicada wam2 examples/live-intensity.cicada -o build/live-wam --midi-track bass
python3 -m http.server 8000 --directory build/live-wam
```

Open `http://localhost:8000/host.html` to try the instrument. This small host
uses the [official WAM SDK host example](https://github.com/webaudiomodules/sdk/tree/main/host)
with instrument output wiring. A host initializes its WAM group and imports
the plugin as follows:

```js
const { initializeWamHost } = await import('./live-wam/sdk.js');
const audioContext = new AudioContext();
const [groupId] = await initializeWamHost(audioContext);
const { default: Plugin } = await import('./live-wam/index.js');
const instrument = await Plugin.createInstance(groupId, audioContext);
instrument.audioNode.connect(audioContext.destination);
mount.append(await instrument.createGui());
```

The export includes the existing Cicada kernel, compiled score images for
44.1, 48, and 96 kHz, the GUI, and the official WAM SDK 0.0.12. The plugin
needs no score compiler, Studio server, or network connection after download.
`make build-wam2` downloads the SDK; `--kernel` and `--sdk` accept local build
artifacts. Export writes a new directory and refuses to replace an existing one.

Every `live` macro becomes a float `WamParameterInfo` with its score name as
the parameter ID, range 0–1, and the declared default. Changes through
`setParameterValues` or `wam-automation` use the score's smoothing duration.
Automation and MIDI events take effect at their scheduled audio sample.
The GUI refreshes its controls after host automation or state restoration.

`getState` returns JSON containing the score identity, macro values, tempo,
and playback switch. Pass that object to `setState` or `createInstance` to
restore the instrument. Restoration clears pending events and held notes,
restores macros immediately, and restarts score playback from the beginning
when requested. A state from a different compiled score is rejected. The
immutable instrument and assets stay in the exported package.

MIDI note on/off, zero-velocity note on, and controllers 120/123 are supported.
All MIDI channels play the selected track; melodic identities distinguish
channels and pitches. Piano input accepts notes 21–108; drums use the engine's
General MIDI drum notes. `--midi-track` selects a track; the default is the
first track supporting live notes. Graph polyphony and audio clip tracks
cannot be MIDI targets. The descriptor does not advertise MPE, MIDI output,
OSC, or sysex. Pitch bend, sustain, and other controllers are currently ignored.

`wam-transport` controls playback and tempo. Host timeline seeking is not
implemented. MIDI can play the instrument while score playback is stopped.
Destroy the GUI with `destroyGui(gui)` and the audio node with
`audioNode.destroy()` when the host unloads the instrument.

Run `make test-wam2` for packaging and worklet checks. Run
`go test -tags browser ./cmd/cicada -run '^TestBrowserWAM2$' -count=1`
for Chrome verification with the SDK reference host.
