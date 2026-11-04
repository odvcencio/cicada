# Live playback and mixing

Cicada projects use source edition 2. A new project includes drums, a bass
line, chords, and two scenes. You can save mixer routes and scene parameter
changes in the score, record MIDI note takes and audio takes in Studio, and
render a WAV.

## Start playing

### Start and check a project

Create a project, then check its starter score:

```sh
cicada new night-circuit
cd night-circuit
cicada fmt --check
cicada check
```

If you paste the mixing example below into `main.cicada`, run `cicada fix
main.cicada`, then check and render it:

```sh
cicada fix main.cicada
cicada check
cicada render main.cicada -o preview.wav --bars 1 --tail 0s
```

The example already uses edition 2, so `fix` leaves its source unchanged.
When you paste older edition-1 spellings into an edition-2 project, `fix`
rewrites them. If another edition-1 score is in the project, use
`cicada fix --all` to migrate the project together.

### Named mixer and parameter paths

Save mixer routing and scene changes in the score:

```cicada
cicada 2
fx room delay { time = 1/8 feedback = 0.3 }
track bass fm_bass {
  level = -6dB
  mute = off
  solo = off
  send room = -12dB pre
}
pattern pulse { 1 . 1 5 }
scene verse {
  bass = pulse
  bass.pan = 0.3
  room.feedback = 0.4
}
song { verse*4 }
```

Check the score and inspect the pan at bar 3:

```sh
cicada check main.cicada
cicada explain main.cicada bass.pan @3
```

The parameter registry supplies the type, unit, range, and smoothing used by
the source checker, Studio, the language server, and `cicada explain`. A scene
setting takes effect when that scene starts and carries forward until a later
scene changes the same path. See the [edition 2 mixer reference](../spec/edition-2.md#named-mixer-forms).

### Host macros and track layers

Place one `live` block after all tracks. A host can change `intensity` to add
tracks at bar boundaries. This complete score starts from the bass, drums,
and lead layout in [live-intensity.cicada](../../examples/live-intensity.cicada):

```cicada
cicada 2
track bass piano {}
track drums drums {}
track lead piano {}

live {
  land = bar
  phrase = 8bars
  count_in = 2bars
  macro intensity = 0.3 smooth 400ms
  layers intensity {
    drums >= 0.25
    bass  >= 0.45
    lead  >= 0.70
    attack 1bar
    release 3bars
  }
}

pattern pulse { 1 . 1 5 }
pattern beat drums { bd: X... }
scene main { bass = pulse drums = beat lead = pulse }
song { main*8 }
```

Both pitched tracks use `piano`, which the core kernel in a browser or game
host plays. Keyboard patches such as `fm_bass` need the separate keyboard
module there (see [Electronic keyboards](keys.md#native-and-browser-playback)).
Run `cicada check examples/live-intensity.cicada` from the repository root.
Macro IDs follow declaration order. `project.LiveCommands` returns the kernel
setup commands; `project.LiveSurfaceOf` returns names, defaults, and smoothing
with `macro.SmoothingFrames(sampleRate)`. A host submits the setup commands
before playback and uses `OpSetMacro` for later values. Initial values apply
immediately; the 400 ms smoothing default applies to later host changes.

Layers rise one level per bar and fall one level after 3 quiet bars. Tracks
without a rule stay on. Values and thresholds are 0–1; the kernel rounds
thresholds to 8 bits, so nearby values can share a level. Studio and the director SDK use `land` as their default launch timing. Studio can override it for a launch; `phrase` requires a declared phrase length. `count_in` sets audio recording to 0–8 bars; the default is one bar. Tempo controls are not supported.

A macro can also control a continuous live parameter. For example, with an
acid track named `bass`, add this declaration to `live`:

```text
macro filter -> bass.cutoff 200Hz..4kHz curve exp smooth 150ms
```

The control starts at zero, corresponding to 200 Hz; one selects 4 kHz.
`curve linear` is the default, while `curve exp` requires positive endpoints.
Each target can have one mapped macro, and switch or integer parameters are
not supported. Parameter mappings run on the 128-frame modulation grid before
modulation routes. Studio's Live panel provides a slider for each declared
macro while playing. Browser playback of mapped macros uses the modular audio
kernel.

### Portable MIDI mappings

An edition-2 score can name logical input ports and map them to tracks, live
parameters, scenes, Play, and Stop:

```cicada
cicada 2
tempo 120
track bass acid {}
track lead acid {}
midi {
  port keys
  keys ch 1 -> lead
  keys cc 74 -> bass.cutoff 200Hz..4kHz takeover = pickup
  keys note c1 -> launch verse
  keys note d1 -> stop-track bass
  keys note e1 -> launch-slot bass phrase
}
pattern phrase { 1 . 3 . }
scene verse { bass = phrase lead = phrase }
song { verse*16 }
```

In Studio's Live panel, select **Enable MIDI**, choose a score port and a
connected device, then select **Bind device**. The binding is saved on this
machine in `cicada.local`. Bind the same logical port to a different device in
another copy of the project. This file is excluded from projects created by
`cicada new` and from bundles; the saved score never contains the device name.

You can also edit `cicada.local` in the project directory yourself, for example
`port keys = "KeyStep 37 MIDI 1"`. Use the exact device name Studio reports.
An absent or invalid binding is reported; select **Reload bindings** after an
external edit. If a binding changed while you were editing it, Studio asks you
to reload rather than overwrite the newer file.

In Studio's Live panel, choose a target and select **Learn next input**. When
the device has exactly one matching declared port, the learned controller or
note is saved in the score with Undo and revision checks. Scene launches,
track stops, and launches of a pattern or clip already in that track's slots
can be mapped. Without a `midi` block, Learn saves browser-local mappings.
Relearning a score-mapped input replaces its target. Remove score mappings in
Code.

CC mappings use jump takeover by default. Add `takeover = pickup` in Code to
wait until the controller reaches or crosses the current live parameter value
before it changes the sound. Pickup tracks automation and rearms when another
control changes that value. Start playback to make the live value available.
Other takeover modes and launch modes besides trigger are not yet supported.

### Live MIDI performance and note takes

Studio's **Live** panel has a launch matrix for scenes and track slots, an
onscreen keyboard, and drum pads. Select **Enable MIDI** to play from a
connected MIDI device. Choose a **Pitched track** and a **Drum track**, then
choose the pattern each one records into.

Press **Play**, then select **Record notes**. Play notes, select **Finish note
take**, and review the **Retained note take**. **Keep played timing** preserves
independent starts, lengths, and velocities, including pitched chords. Open
**Patterns** to edit those notes or loop the full performance independently of
the song. **Snap to the existing step grid** retains the grid workflow,
including drum lanes. Unsupported gates or expression keep the take intact;
check the message before choosing a timing mode. **Undo** restores the previous
source. Local desktop profiles retain private recovery copies across restarts.
See [note recording](studio.md#live-performance) for limits and
[recording instruments](recording-instruments.md) for audio input.

Open Studio without an audio device for a silent session:

```sh
cicada studio examples/first-song/main.cicada --audio null
```

The [Studio chapter](studio.md#live-performance) explains the controls and
what Live plays.

### Native audio

On Windows, Linux, and macOS, `cicada play` and Studio use Tymbal by default. Tymbal
uses WASAPI shared mode on Windows, ALSA on Linux, and CoreAudio on macOS. If the device cannot
open, Cicada reports the error; it does not switch engines silently. Select
Oto explicitly when you need its system-default device:

```sh
cicada play main.cicada --audio oto
CICADA_AUDIO=oto cicada play main.cicada
```

Use `--audio null` for real-time transport without an output device.

On Windows and macOS, Studio can open native input and output together. Input monitoring
starts muted. Change device selections while playback is stopped; Stop releases
both endpoints.
On Mac, enable input explicitly and allow the microphone permission request.
See [Cicada Studio on Mac](macos.md) for setup and shortcuts.

### Loudness and WAV export

The renderer can target integrated loudness and limit true peak, then verify
the result:

```sh
cicada render examples/first-song/main.cicada -o release.wav --rate 48000 --bits 24 --bars 8 --loudness -16 --true-peak-max -1
cicada verify-wav release.wav --rate 48000 --bits 24 --bars 8 --tail 3s --lufs -16 --true-peak-max -1
```

Bar numbers start at 1, so `--from 1` selects the first bar. The old
`--from 0` input still selects bar 1 and prints a deprecation warning.
Studio's Master panel also measures integrated loudness and exports at a
target level. If the score's `master` chain contains a limiter, the loudness
target applies its gain before that chain so your limiter holds the true-peak
ceiling; see [Export loudness targets](../audio/mix-chain.md#export-loudness-targets).
Use `--normalize` for peak normalization when you do not request
a loudness target.

### Custom-voice glide

Custom graph voices glide when a note marked with `~` slides to the next
pitch. The default is 60 ms. Set an instrument's `glide` duration and, if
needed, override it on a track. Notes without `~` keep their existing onset
behavior:

```cicada
cicada 2
instrument glassbass {
  glide = 120ms
  vibrato = 12ct
  vibrato_rate = 5Hz
  vibrato_delay = 200ms
  voice mono {
    let osc = saw(pitch)
    out = osc
  }
}
track lead glassbass { glide = 75ms vibrato = 18ct }
pattern glide-line { 1~ 3 . 5 }
scene main { lead = glide-line }
song { main }
```

The track uses a 75 ms exponential time constant; another track using
`glassbass` without an override uses 120 ms. A zero duration changes pitch immediately while preserving the slide's
held gate. Durations may be 0–10000 ms, in milliseconds or seconds. If an
existing instrument declares a synthesis parameter named `glide`, its track
value continues to control that parameter. Validate this score with `cicada
check glide.cicada`.

The lead track also overrides the instrument's baseline vibrato depth to
18 cents while inheriting its rate and delay. Custom graph instruments accept
all three settings, and graph and acid tracks can override each one. Depth is
0–9600 cents, rate is 0–100 Hz, and delay is 0–10000 ms. Defaults are 0 cents,
5 Hz, and no delay. A zero rate pauses baseline vibrato. Per-step
[expression rows](../spec/features.md#expression-rows) add their vibrato to
the baseline and independently control bend, pressure, and timbre. Browser
playback of baseline vibrato uses the modular audio kernel.
Instrument vibrato is not yet available inside authored drum kits.

### Language reference

The [edition 2 reference](../spec/edition-2.md) describes the current named
mixer forms. The [edition 1 reference](../spec/edition-1.md) documents the
earlier source spelling. The [language extensions](../spec/features.md) describe supported graph, expression, project, and preset features. The repository checks the
runnable Cicada examples in the README, manual, and specification with
`GOWORK=off go test ./cmd/cicada -run '^TestDocumentationCicadaExamples$' -count=1`.
