# Live playback and mixing

Cicada projects use source edition 2. A new project includes bass, drums, and
two scenes. You can save mixer routes and scene parameter changes in the score,
record MIDI note takes in Studio, and render a WAV.

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
track bass acid {
  level = -6dB
  mute = off
  solo = off
  send room = -12dB pre
}
pattern pulse acid { 1^ . 1~ 5 }
scene verse {
  bass = pulse
  bass.cutoff = 900Hz
  room.feedback = 0.4
}
song { verse*4 }
```

Check the score and inspect the cutoff at bar 3:

```sh
cicada check main.cicada
cicada explain main.cicada bass.cutoff @3
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
track bass acid {}
track drums drums {}
track lead acid {}

live {
  land = bar
  phrase = 8bars
  macro intensity = 0.3 smooth 400ms
  layers intensity {
    drums >= 0.25
    bass  >= 0.45
    lead  >= 0.70
    attack 1bar
    release 3bars
  }
}

pattern pulse { 1^ . 1~ 5 }
pattern beat drums { bd: X... }
scene main { bass = pulse drums = beat lead = pulse }
song { main*8 }
```

Run `cicada check examples/live-intensity.cicada` from the repository root.
Macro IDs follow declaration order. `project.LiveCommands` returns the kernel
setup commands; `project.LiveSurfaceOf` returns names, defaults, and smoothing
with `macro.SmoothingFrames(sampleRate)`. A host submits the setup commands
before playback and uses `OpSetMacro` for later values. Initial values apply
immediately; the 400 ms smoothing default applies to later host changes.

Layers rise one level per bar and fall one level after 3 quiet bars. Tracks
without a rule stay on. Values and thresholds are 0–1; the kernel rounds
thresholds to 8 bits, so nearby values can share a level. `land` stores a launch preference. Tempo controls, saved MIDI mappings, and parameter-mapped macros are not supported.

### Live MIDI performance and note takes

Studio's **Live** mode has scene and track launch pads. Turn on **Enable MIDI**
to receive notes and controls from a connected MIDI device. Choose a note track
and drum track, then use **Record-arm** checkboxes to select which tracks
receive a note take.

Start playback before selecting **Record**. Play notes, select **Stop
recording**, review the take, then choose **Commit take** or **Discard**. The
take stays in page memory until you commit it. Drum hits keep separate lanes
and supported velocities. Acid notation keeps pitch and slides at its fixed
velocity; it cannot save arbitrary MIDI velocity. MIDI overdub and replace modes are not available. See the [recording chapter](recording-instruments.md) for audio input.

Open Studio without an audio device for a silent session:

```sh
cicada studio examples/first-acid.cicada --audio null
```

The [Studio chapter](studio.md#live-mode) explains the controls and shortcuts.

### Native audio

On Windows and Linux, `cicada play` and Studio use Tymbal by default. Tymbal
uses WASAPI shared mode on Windows and ALSA on Linux. If the device cannot
open, Cicada reports the error; it does not switch engines silently. Select
Oto explicitly when you need its system-default device:

```sh
cicada play main.cicada --audio oto
CICADA_AUDIO=oto cicada play main.cicada
```

Use `--audio null` for real-time transport without an output device. macOS
continues to use Oto by default.

On Windows, Studio can open WASAPI input and output together. Input monitoring
starts muted. Change device selections while playback is stopped; Stop releases
both endpoints.

### Loudness and WAV export

The renderer can target integrated loudness and limit true peak, then verify
the result:

```sh
cicada render examples/first-acid.cicada -o release.wav --rate 48000 --bits 24 --bars 16 --loudness -14 --true-peak-max -1
cicada verify-wav release.wav --rate 48000 --bits 24 --bars 16 --tail 3s --lufs -14 --true-peak-max -1
```

Bar numbers start at 1, so `--from 1` selects the first bar. The old
`--from 0` input still selects bar 1 and prints a deprecation warning.
Studio's Master panel also measures integrated loudness and exports at a
target level. Use `--normalize` for peak normalization when you do not request
a loudness target.

### Custom-voice glide

Custom graph voices glide for 60 ms when a note marked with `~` slides to the
next pitch. Notes without `~` keep their existing onset behavior:

```cicada
cicada 2
instrument glassbass {
  voice mono {
    let osc = saw(pitch)
    out = osc
  }
}
track lead glassbass {}
pattern glide-line { 1~ 3 . 5 }
scene main { lead = glide-line }
song { main }
```

Validate this score with `cicada check glide.cicada`. Per-step [expression rows](../spec/features.md#expression-rows) control bend, vibrato, pressure, and timbre.

### Language reference

The [edition 2 reference](../spec/edition-2.md) describes the current named
mixer forms. The [edition 1 reference](../spec/edition-1.md) documents the
earlier source spelling. The [language extensions](../spec/features.md) describe supported graph, expression, project, and preset features. The repository checks the
runnable Cicada examples in the README, manual, and specification with
`GOWORK=off go test ./cmd/cicada -run '^TestDocumentationCicadaExamples$' -count=1`.
