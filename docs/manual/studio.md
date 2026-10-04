# Cicada Studio

Studio is a GoSX workstation for the score on disk. Its editor, forms,
navigation, and live meters use GoSX; Tymbal handles native audio devices.
Changes to patterns, mixer settings, and arrangements write back to the score.

From a source checkout, build both executables and open a score:

```sh
make build
./build/cicada studio examples/first-acid.cicada
```

Open the printed loopback URL. Playback starts only when you select **Play**.
`--audio null` exercises transport without an output device. On Windows and
Linux, the default is Tymbal. Use **Audio** to select its devices and input
monitoring settings while stopped.

![GoSX Studio's desktop mixer and output meter surface.](screenshots/studio-desktop-1440.png)

![GoSX Studio's pattern controls on a narrow screen.](screenshots/studio-mobile-390.png)

## Transport and session

**Play**, **Pause**, **Stop**, and **Return to start** control native playback.
The bar, step, backend, and errors update through GoSX live bindings. Valid
score edits become active at the next bar; an invalid external edit leaves the
last valid score playing.

**Session** shows a scene-by-track matrix. Select a scene header to launch its
assignments, a pattern cell to queue one track, or **Stop** to stop a track.
Launches are quantized to the next bar. **Keep** preserves that track's slot.

The arrangement lists scene entries and their bar ranges. **Set bars** changes
an entry's duration; **Move earlier** and **Move later** change its order.
**Play from here** starts the song at that entry. All edits check the current
disk revision.

## Patterns

**Patterns** shows pitched steps and the declared drum lanes. Select a step to
toggle a note or hit. Pitched patterns also have a MIDI-pitch form. Step numbers
in the modifier and pitch forms start at zero.

Select **accent**, **slide**, **tie**, **ratchet**, or **chance**, choose a step,
and apply the modifier. Choose the lane ID for a drum edit. Ratchet and chance
use Cicada's existing articulation cycles. Successful edits refresh the score
projection. For reused phrases, the definition changes and every use follows.

## Phrase generation

**Generate** creates deterministic acid phrases from a 64-bit seed, key,
scale, structure, density, articulation, swing, and gate. Eight scales and
8, 16, 32, or 64 steps are available. Seeds remain exact even above JavaScript's
integer precision limit.

**Preview phrase** leaves the open score unchanged. The preview belongs to
your browser session and expires after thirty minutes. Open **Mutate or evolve
the first bar** to select an operation or generation index. Check the steps
that must remain locked. A variation preserves those steps and the preview's
other patterns and arrangement. If an operation requires changing a lock,
Studio reports the conflict.

**Replace open score with this phrase** applies the complete reviewed preview.
It checks the original revision; it cannot overwrite another editor's save.
**Undo** restores the previous score.

## Mixer and instruments

**Mixer** edits track, bus, return, effect, and master fields supported by the
source patcher. Each **Set** writes one parameter. Unsupported fields explain
their limitation and remain available in **Score**.
Ranges, units, enum choices, and On/Off controls come from Cicada's parameter
registry. Existing track sends are included in their mixer strip.
For an edition 1 score, first check **Upgrade score to edition 2 for this change**.
The domain service performs the migration and parameter edit together.

The GoSX meter surface shows track and master peaks during playback. It stops
polling when you leave the mixer. These dBFS readings do not replace offline
integrated-loudness verification.

**Instruments** lists declared voices, exposed parameters, and track overrides.
Edit instrument definitions in **Score**.

## Score and history

**Score** uses the GoSX code editor with syntax highlighting, a gutter, and
two-space indentation. **Save score** validates before writing. A rejected edit
keeps the file unchanged and retains the draft across reload and navigation.
Rejected drafts expire after thirty minutes or when Studio closes.

The file remains authoritative. If another editor saves first, Studio reports
a conflict and retains your draft. Reload **Score** to see the current file
beside it, combine the changes in the editor, and select **Save combined draft**.
Another save in the meantime causes a new conflict. **Discard draft** restores
the file without writing to it.

**History** shows source diffs and transport events. **Restore this revision**
restores a selected edit. **Undo** and **Redo** are available in the toolbar.
History changes also check the disk revision.

## Audio, takes, and export

**Audio** selects input and output devices, input enablement, monitoring mute,
gain, and stereo or mono channel selection. Stop playback before applying these
settings. Device errors stay visible; Cicada does not silently switch engines.

**Takes** selects a track and scene for native input recording. Enable input in
**Audio**, then **Arm take**, **Record**, and **Finish take**. The durable journal
retains failed or conflicting takes. **Select take** commits a selected take;
**Recover take** retries its recovery against the current revision.

**Export** renders WAV with a target LUFS value, true-peak ceiling, tolerance,
sample rate, and PCM depth. Status and the output path update through GoSX.
Offline rendering uses the same Cicada kernel as Tymbal playback. See
[exporting](exporting.md) for stems, MIDI, and verification commands.

## Browser support

Forms, editing, navigation, and transport commands also work with JavaScript
disabled. Live bindings, editor enhancements, and animated meters require the
GoSX browser runtime. The audio service continues to run natively.

Browser AudioWorklet capture, Web MIDI learning, and live note recording remain
portable-host qualification features. They are not exposed by this GoSX Studio
yet. The current Tymbal backend does not run in a browser AudioWorklet; its
native playback is independent of browser audio permission.
