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

The arrangement timeline shows scene blocks across all tracks, their bar
ranges, and inherited patterns. Click a scene header to play from that block,
or a pattern clip to open its editor. Expand a block to replace its scene,
set its length, duplicate it, remove it, or move it earlier or later.
**Add to arrangement** appends an existing scene. At least one block remains.
Structural changes to songs with intervening comments require a Score edit
so those comments keep their attachment. Local scene and bar edits preserve them.

**Project settings** changes the title, tempo, key, and scale. Tempo keeps up
to three decimal places. Changing the key retunes scale-degree notes; explicit
letter pitches keep their pitch. Every edit validates and checks the current
disk revision before saving, and has an Undo entry.

![GoSX Studio's arrangement timeline and block controls.](screenshots/studio-arrangement-1440.png)

**Scene automation** shows parameter lanes alongside the song's scene blocks.
Choose a parameter and **Inspect parameter**, then choose a scene and **Add or
replace point**. Track level and pan, sends, supported synth controls, and
effect parameters use the shared parameter catalog. Values accept their units
(for example `1.25kHz`, `250ms`, or `-6.25dB`), and controls report the allowed
range and alternatives. Existing points can be edited or removed.

A point applies each time its scene starts, with the engine's parameter
smoothing. **●** marks an explicit point; **↳** carries the previous value.
Removing a point keeps that inherited value; add the score's default value
explicitly to reset it. Repeated uses of a scene share its points. Playback,
seek reconstruction, WAV exports, and stems use the same scene settings.
Edits preserve surrounding source comments and support Undo. These lanes
place points at scene boundaries. Continuous curves and recording control movement are not supported.

![GoSX scene automation with explicit points and inherited levels.](screenshots/studio-automation-1440.png)

## Patterns

Choose a pattern from the library. The piano roll shows pitched patterns;
drum patterns show their authored lanes. Click a cell to write or clear a note
or hit. Choose a step number (starting at **1**) to inspect it without changing
it. **View register** moves the piano roll to another octave; the editor reports
notes outside the displayed register.

The step inspector sets note, tie, or rest; MIDI pitch; accent and slide;
ratchet from 1–8; and chance from 1–100%. Drum hits offer velocity levels
**1–9**, normal **x** (100), or accented **X** (127). Melodic dynamics use the
voice's accent behavior. **Pattern timing** edits swing, gate, and transpose,
and resizes patterns to 1–64 steps. Shortening removes the ending steps from
every lane; Undo restores them.

**Range editing** copies, clears, reverses, rotates, or transposes selected
steps. Copy writes at its destination without extending the pattern; overlapping
copies read the original range. Rotate wraps inside the selection. Transpose
changes note pitches while retaining articulation, rests, and ties. Drum lanes
support the first four operations. Each operation commits as one Undo entry.

**Create variation** duplicates a pattern with independent notes and no reserved
slot. Its current pitches are written explicitly, including a custom voice's
home octave. **Assign to scene** places it on a compatible track. Editing a
phrase-based pattern expands its phrase uses locally, preserving the shared
phrase and other patterns. Comments inside a phrase-use expression require a
Score edit instead of being discarded.

![GoSX Studio's piano roll and step inspector.](screenshots/studio-piano-roll-1440.png)

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

Live accepts MIDI Polyphonic Expression (MPE) lower and upper zones. Send the
controller's MPE zone setup (RPN 0,6) before playing. Member-channel pitch bend
uses a default range of ±48 semitones; pitch-bend sensitivity RPN 0,0 overrides
that range. Channel pressure, polyphonic pressure, and CC74 become per-note
pressure and timbre. Each input device, channel, and note-on has its own note
identity, so equal pitches on different channels remain independent.

Without a zone setup, expression received on a non-master channel enables the
default lower zone. Until expression or zone setup identifies MPE, channel 10
keeps its General MIDI drum routing. Choose a monophonic programmable instrument
track or an acid track as the note destination. Experimental poly tracks cannot
audition MPE input live. Web MIDI supplies MIDI 1 packets; this
input does not decode MIDI 2 Universal MIDI Packets.

#### Record and Arm

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
Ordinary MIDI take commits support acid, drum, and eight-voice programmable tracks with single-note takes. Expressive takes also
support programmable instrument tracks with scalar note patterns. Existing
chord patterns are refused without changing the score; edit their chords in
the grid or source pane. Chord recording is not available through the take editor.

Expression takes write `bend:`, `vibrato:`, `pressure:`, and `timbre:` rows and
use ties for held notes. Samples and note durations quantize to the pattern's
step grid. A one-way bend keeps its cents value. Pitch samples with at least
two direction reversals within a step become a bend center and vibrato depth;
playback uses the score's fixed 5 Hz vibrato rate. Other steps use `0ct` vibrato
depth. This estimate preserves depth rather than the original oscillation rate
or every controller sample. The raw samples remain in the page's take buffer
until commit or discard.

Expression takes use one note per step. Studio keeps the take buffered and
refuses to commit overlapping expressive notes, notes that occupy the same
quantized step, or notes longer than one pattern loop. Record those voices into
separate tracks and patterns without chord steps. Expression recording also
requires directly authored note steps rather than a reused phrase. Ordinary acid takes retain
their existing overlapping-slide behavior.

![A committed MPE take adds bend, vibrato, pressure, and timbre rows beside the pitch grid.](screenshots/studio-mpe-score.png)

![The recorded take preview and commit controls at 390 pixels wide.](screenshots/studio-mpe-mobile-390.png)

## Mixer and instruments

**Mixer** edits track, bus, return, effect, and master fields supported by the
source patcher. Each **Set** writes one parameter. Unsupported fields explain
their limitation and remain available in **Score**.
Ranges, units, enum choices, and On/Off controls come from Cicada's parameter
registry. Existing track sends are included in their mixer strip.
For an edition 1 score, first check **Upgrade score to edition 2 for this change**.
The domain service performs the migration and parameter edit together.
#### PCM capture and shared sampler audition

For tap slicing, velocity layers and round robins, use **Record your own
instrument** in the Record panel. See [recording instruments](recording-instruments.md)
for microphone, WAV import and offline pack commands.

Add an edition-2 audio track and a scene, stop transport, and open **Record**.
Choose the target **Audio track** and **Scene** before arming. Synth tracks
provide accompaniment during recording; recorded clips are auditioned separately.
The GoSX meter surface shows track and master peaks, RMS, momentary,
short-term and integrated LUFS, loudness range, true peak, and dropped blocks.
**Reset live loudness** begins a new measurement. The surface stops polling
when you leave the mixer. Offline export still provides the final loudness
verification.

**Instruments** offers six starting patches: Warm pad, Wide strings, Poly brass,
Silk pluck, Round bass, and Soft bell. **Add instrument and track** saves an
editable graph and its track in one Undo step. Five patches have eight-note
polyphony; Round bass is monophonic. Choose the new track in **Live** to play
it using the on-screen keyboard, typing keyboard, or MIDI. Bind a notes pattern
in **Session** to sequence it and include it in WAV exports.

Declared parameters show their default and current track override. Apply a
value to shape just that track; **Use default** removes its override. Values
use the parameter's declared unit. Each change is revision checked and can be
undone. Edit the complete graph in **Score**. Notes patterns and committed note
takes currently store one pitch per step; overlapping polyphonic takes explain
this limit instead of discarding chord notes.

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

Use **Add audio track** to create a recording lane in an edition-2 score. An
edition-1 score uses the explicit upgrade option in Mixer first. The Audio
tracks and clips section edits the source-frame start, exclusive end, gain, and
linear fades of each recorded region. **Place clip in scene** assigns it to an
audio track without modifying the recorded file. Region and assignment edits
check the source revision and support Undo. Arrangement audio blocks link here.

![GoSX audio track, clip region, gain, fade, and scene assignment controls.](screenshots/studio-audio-clips-1440.png)

**Audition sample** opens a private preview of a published take. Choose its
root and playback MIDI notes and optional full-region looping, then **Update
audition** and use the audio controls. The native sample voice renders the WAV;
audition leaves the score unchanged and stops when you leave the panel.

Audio clips and declared sampler instruments play through the native mixer,
effects, meters, and Tymbal output, and are included in WAV and stems exports.
An explicit clip assignment starts its region when the scene is launched or a
song entry begins; `keep` continues it, and `off` releases it. A multi-bar entry
does not retrigger the clip each bar. Pause retains the source position, and
playing from a later block reconstructs an inherited clip's elapsed position.
Clips play once at their source rate; they are not stretched to fit scene bars.
Sampler patterns use the declared root pitch, voice budget, and oneshot or loop
mode; note gates release their voices. Supported playback ratios are 0.125–8.
Live keyboard input currently targets the existing acid and drum controls.

Assets are verified and decoded before playback, confined to the project
directory, and shared immutably between regions. Native playback admits up to
64 MiB of resident PCM per prepared project and 16 clips per audio track. File
I/O and decoding never run in the audio callback. Existing audio accompanies
new native recordings; the old portable qualification image remains synth-only.

### Library

The Library panel lists public instruments, kits, FX chains and presets from
standard, project and user libraries. Filter by kind or search by name and
library path. Select a track, or enter a new track name, then choose **Insert**.
Studio adds the import once and updates the library pins before committing the
validated source. Drive binds as an insert, delay and reverb bind as sends, and
compression binds on the music bus. Unsupported combinations leave the score
unchanged.

**Preview** plays a fixed one-bar phrase at 120 BPM through the selected audio
engine without changing the score. Only one preview plays at a time; starting
transport or switching audio mode stops it. Stop playback and recording before
previewing. Browser previews use the same kernel and AudioWorklet processor as
score playback.

To save a selected track's stored values, enter a preset name and choose
**Save as preset**. Studio resolves any existing preset and writes the current
non-default registry values into a new declaration in the score. Source edits
and preset saves support History undo and redo. Pins remain available for redo.
Studio continues to refuse multi-file source editing.

### History

**Export** renders WAV with a target LUFS value, true-peak ceiling, tolerance,
sample rate, and PCM depth. Progress, measured LUFS, true peak, applied gain,
limiter reduction, and the output path update through GoSX. **Download WAV**
appears when the current job completes. A shortfall render is labeled explicitly
for inspection before delivery. Rendering checks the displayed source revision;
starting another job invalidates its previous download link. Audio stays outside
the public asset bundle, and long downloads stream through the private service.
Offline rendering uses the same Cicada kernel as Tymbal playback. See
[exporting](exporting.md) for stems, MIDI, and verification commands.

## Browser support

Forms, editing, navigation, and transport commands also work with JavaScript
disabled. Live bindings, editor enhancements, MIDI, note input, and meters require the
GoSX browser runtime. The audio service continues to run natively.

## Safe editing

Studio edits the same `.cicada` source you can open in a text editor. It
validates a proposed change before writing, checks that the file revision has
not changed, and refuses writes when it detects an unresolved external
recovery file. If Studio reports a conflict, read the message, compare the
named recovery file with your current score, and resolve the edits before
continuing.

If you want Studio beside VS Code, install or run the extension described in
[Editors and notation tools](editors.md#vs-code).

Continuous automation appears below the Song lane. Each graph labels its parameter, range and musical endpoints; point descriptions retain positions, values and shapes for assistive technology. Edit `automate` blocks in the score and reload to update the curves. See [automation blocks](../spec/features.md#automation-blocks) and [the runnable example](../../examples/continuous-automation.cicada).

Input recording uses the native device selected in **Audio**. The current
Tymbal backend does not run in a browser AudioWorklet; native playback and
capture are independent of browser microphone permission. Legacy portable-host
adapters remain qualification fixtures and are excluded from Studio's
production routes.
