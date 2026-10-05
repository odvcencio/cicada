# Cicada Studio

Studio is a GoSX workstation for the score on disk. Its editor, forms,
navigation, meters, and live performance use GoSX; Tymbal handles native audio devices.
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

## Live performance

![GoSX Live performance with scene launches, note input, and MIDI learning.](screenshots/studio-live-1440.png)

**Live** plays an acid track from the A/W/S/E/D/F/T/G/Y/H/U/J/K keys or the
onscreen keyboard. Drum pads use General MIDI notes. Press **Play** first;
changing tracks, leaving the panel, or losing focus releases held notes.
The launch matrix queues scenes or individual slots with the selected timing;
mint marks a playing slot and amber marks a queued slot. Its forms also work
without browser scripting.

**Enable MIDI** asks for browser permission without system-exclusive access.
Select a parameter, scene, slot, track stop, or transport target, then **Learn
next input**. Parameters learn controller messages; launches learn note
messages. **MIDI launch timing** chooses next beat, next bar, or one, two, or four
bars. Mapping and track selections are saved in this browser. **Clear mappings**
removes them. Browsers without Web MIDI can still use keyboard and drum pads.

Choose each track's pattern, then **Record notes**. Acid and drum recordings
use separate pattern targets. **Finish note take** retains the notes for
review without changing the score. **Commit notes to patterns** quantizes them
to the nearest step; **Undo** restores the previous patterns. An interrupted
take is retained in the tab's session storage when available. A source conflict
keeps the preview; applying it to the current score requires explicit review.
Server previews expire after thirty minutes or when Studio closes.

## Mixer and instruments

**Mixer** edits track, bus, return, effect, and master fields supported by the
source patcher. Each **Set** writes one parameter. Unsupported fields explain
their limitation and remain available in **Score**.
Ranges, units, enum choices, and On/Off controls come from Cicada's parameter
registry. Existing track sends are included in their mixer strip.
For an edition 1 score, first check **Upgrade score to edition 2 for this change**.
The domain service performs the migration and parameter edit together.

The GoSX meter surface shows track and master peaks, RMS, momentary,
short-term and integrated LUFS, loudness range, true peak, and dropped blocks.
**Reset live loudness** begins a new measurement. The surface stops polling
when you leave the mixer. Offline export still provides the final loudness
verification.

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

**Takes** selects an audio track and scene for native input recording. Enable input in
**Audio**, then **Arm take**, **Record**, and **Finish take**. Recording begins
after a two-bar count-in; status shows saved frames or seconds and incomplete
input. The durable journal
retains failed or conflicting takes. **Select take** commits a selected take;
**Recover take** retries its recovery against the current revision.

**Audition sample** opens a private preview of a published take. Choose its
root and playback MIDI notes and optional full-region looping, then **Update
audition** and use the audio controls. The native sample voice renders the WAV;
audition leaves the score unchanged and stops when you leave the panel.

**Export** renders WAV with a target LUFS value, true-peak ceiling, tolerance,
sample rate, and PCM depth. Status and the output path update through GoSX.
Offline rendering uses the same Cicada kernel as Tymbal playback. See
[exporting](exporting.md) for stems, MIDI, and verification commands.

## Browser support

Forms, editing, navigation, and transport commands also work with JavaScript
disabled. Live bindings, editor enhancements, MIDI, note input, and meters require the
GoSX browser runtime. The audio service continues to run natively.

Input recording uses the native device selected in **Audio**. The current
Tymbal backend does not run in a browser AudioWorklet; native playback and
capture are independent of browser microphone permission. Legacy portable-host
adapters remain qualification fixtures and are excluded from Studio's
production routes.
