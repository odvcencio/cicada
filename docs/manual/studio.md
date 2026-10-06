# Cicada Studio

Studio shows your source and the project it compiles into. When you change a
pitch, drum hit, or song block in the grid, Studio writes that change back to
the score file.

Open a score from the repository root:

```sh
./cicada studio examples/first-acid.cicada
```

Studio prints a loopback URL. Open it in your browser. The transport starts
stopped; the page does not play sound until you select **Play**.

![Studio at 1440 pixels wide: source is on the left, with the scene matrix, song lane, and pitch grid on the right; the top transport includes a master peak meter.](screenshots/studio-desktop-1440.png)

![Studio at 390 pixels wide: the transport and master peak meter sit above the stacked source and session panes, with the scene launch matrix below.](screenshots/studio-mobile-390.png)

## The Studio page

The top bar shows the score title, tempo, key, track count, and pattern count.
The navigation links jump to **Code**, **Session**, **History**, and **Voice**.
The transport button and its bar-and-step position sit beside the navigation.

### Code: the source pane

The left pane names the score file and marks it as the source of truth. Select
**Edit source** to open the text editor. **Save score** validates and compiles
the source before writing it; a rejected edit leaves the file unchanged.
**Discard** restores the last saved text. The status line reports validation,
save, and conflict results.

The file on disk remains authoritative. If another editor saves first, Studio
rejects its stale revision so you can reload and combine the changes.

### Session: scenes and pattern grids

The scene matrix has one column per scene and one row per track. A scene header
launches all of its assignments. A pattern cell queues that pattern on one
track. These launches take effect at the next bar while the transport runs.
Cells marked **keep** or **stop** describe the scene but are not launch buttons.

Open a pattern card to see its step grid:

- In a pitched pattern, click a pitch-row cell to set that step to the row's
  note. Click the active pitch again to clear it. The **Note** row toggles a
  note and a rest; the **Velocity** row displays the compiled velocity.
- Existing chord steps show every pitch in the grid and Note row. Click a lit
  chord pitch to remove only that note, or an empty pitch to add one. A chord
  holds at most four pitches; remove one before adding a fifth. These gestures
  edit individual pitches, not the whole chord's transposition. The **Note**
  row explicitly clears the whole chord to a rest. If only one pitch remains,
  it becomes a normal mono step with the set/clear behavior above; ordinary
  mono cells never gain extra pitches implicitly. Write bracket chords in the
  source pane to create a new chord. Untouched pitch spellings and shared
  modifier values stay intact, and following ties continue the remaining pitches.
  When a chord becomes a single note, spaces in its modifier suffix are removed
  because mono notation requires adjacent modifiers.
- **Accent** and **Slide** toggle their modifiers on an existing note.
- **Ratchet** cycles through one to eight hits.
- **Chance** cycles through 100%, 75%, 50%, and 25%.
- In a drum pattern, click a lane cell to switch between silence and a normal
  hit. Edit drum velocity and modifiers in the source pane.

Each successful grid edit changes the source file and refreshes the
projection. If the step came from a reused phrase, the edit changes the phrase
definition, so every use changes with it. The source editor and grid cannot
save over one another: save or discard an open source draft before editing a
grid cell.

### Session: song lane

The song lane shows each scene entry, its bar count, and its song-bar range.
Use the left and right arrows to move an entry by one position, and the minus
and plus buttons to shorten or lengthen it by one bar. Drag the grip to reorder
an entry or the edge handle to resize it. These controls edit the score's
`song` declaration.

Select the play button on a song block to start the arrangement from that
entry. Studio resolves the selected entry against the current score and
continues through the later entries.

### Live transport

Select **Play** to start the score's song; the button becomes **Stop**.
Studio shows the current bar and step, and reports when a scene, pattern slot,
edit, or song start is queued or has landed. Selecting **Stop** stops the
native transport.

The native player watches the score file. A valid save becomes active at the
next bar with a short crossfade. If a save has a syntax or semantic error, the
last valid score keeps playing and the status reports the problem. The
[live-playback chapter](next-level.md#available-on-main) covers the
boundaries of the current engine.

#### Live mode

Select **Live** in the top bar to open the Live performance panel. It shows the
bar and beat, a launch-quantize menu, a scene-by-track launch grid, MIDI note
and drum track selectors, record-arm controls, and the take preview. Scene and
track launches follow the selected quantize value.

#### Enable MIDI

Select **Enable MIDI** in the top bar and allow the browser to access MIDI
devices. The page shows connected-device count and MIDI activity. Choose a
note track and drum track in Live mode. Right-click or long-press a scene pad,
track pad, or live parameter to learn a MIDI note or controller; use **Remove**
in MIDI mappings to clear a mapping.

MIDI permission and mappings belong to the browser session. The source-level
`midi {}` mapping syntax is accepted for later work; it is not available in the
current parser.

#### Record and Arm

Start playback before recording. Check **Arm** for each track that should
receive notes, then select **Record** and play the MIDI device. Select **Stop
recording** to preview the buffered notes. Choose **Commit take** to write the
notes into the score or **Discard** to clear the buffer. Nothing reaches the
score before Commit.

MIDI take commits currently support acid and drum tracks. Custom/poly tracks
are refused without changing the score; edit their chords in the grid or source
pane instead. Chord recording is not available through the scalar take editor.

Drum takes keep simultaneous hits on separate lanes and quantize velocity to
the nearest supported drum level. Acid takes save pitch and overlapping slides
at the score format's fixed velocity. The preview explains that arbitrary MIDI
velocity cannot be saved for acid notation. Live MIDI playback still uses
incoming velocity. Live records MIDI note takes. Browser PCM capture is in the
Record panel.

#### PCM capture and shared sampler audition

Add an edition-2 audio track and a scene, stop transport, and open **Record**.
Choose the target **Audio track** and **Scene** before arming. Synth tracks
provide accompaniment during recording; recorded clips are auditioned separately.

For browser capture, select **Browser** audio, choose mono or stereo, then
**Arm microphone**. Arming requests microphone permission with
echo cancellation, noise suppression, and automatic gain control disabled.
The effective settings appear below the buttons; a browser may keep processing
enabled or leave a setting unreported. The input remains active while armed,
including when transport is stopped. Input monitoring stays off.

Choose **Record PCM** for a one-bar count-in, then **Stop and save**. Stopping
transport also finalizes an active browser recording. The worker stores raw interleaved
float32 PCM, including count-in, alongside lane D's capture block descriptors
and placement metadata. A bounded 32-buffer transfer pool admits mono/stereo
callbacks of up to 2,048 frames. A full pool or missing input marks the take
incomplete and records a known frame gap, including any trailing gap.

Storage uses OPFS with synchronous worker handles when available, or durable
IndexedDB transactions otherwise. Recording is refused when neither works.
PCM is flushed before its journal entry; recovery ignores uncommitted tails.
Browser storage remains subject to the browser's quota and eviction policy.
**Recover last take** reopens the last admitted take across reloads, including
an interrupted take, using a small local storage index. Every take gets a new
storage ID; old PCM is never overwritten.

**Stop and save** imports committed browser PCM and timing into the project take
journal, publishes a verified immutable float32 WAV asset, and selects its clip
on the chosen audio track and scene. Browser PCM is retained if publication fails.
If the source changes during recording, the published take remains available and
the source edit is preserved. **Recover last take** explicitly selects that take
against the current source revision. Selection is one undoable source edit, and
Save As carries the retained takes and their assets into the copied project.

For native capture, select **Native**, enable duplex input in the device controls,
and use the same arm, record and stop buttons. The configured device supplies the
input layout. Native takes write directly to the project journal. Native monitoring
uses the device controls; browser monitoring stays off.

Web Audio does not expose microphone first-frame timestamps or a qualified
duplex latency measurement. The status therefore shows **timing unavailable**
and **uncalibrated**. Reported device settings are retained, but no latency
correction is silently applied to the take.

After publication or recovery, **Play sample** auditions one region with one voice. Choose a
root MIDI note, audition MIDI note, and optional basic loop. Audition skips
negative count-in placement and inserts silence for known gaps; stored PCM
stays unchanged. The shared Go sample voice prepares pitch conversion on the host;
the browser plays the resulting float32 WAV at unity playback rate. Assets and
rendered output are each limited to 8,388,608 frames; browser imports also have a
64 MiB PCM limit. Imported mono/stereo PCM16/24/32 and float32 WAVs can be prepared
as sampler regions. Non-finite samples and changed hashes are rejected before
playback. Clip sequencing in the native/WASM arrangement, hardware calibration,
and real-browser soak qualification remain separate work.

#### Live keyboard shortcuts

With Live mode on and focus outside a text field:

- **F1–F8** launch scenes 1–8.
- **Left/Up** and **Right/Down** select the previous or next scene.
- **Enter** or **Shift+Space** launches the selected scene.
- **Escape** closes the MIDI learn menu.

While transport runs, Studio shows track and master peak levels in dBFS. These
are peak meters, not integrated-loudness measurements. Run
`./cicada studio examples/first-acid.cicada --audio null` to render silently
in real time without opening an audio device.

On Windows and Linux, Studio uses Tymbal with WASAPI and ALSA, respectively.
The **Audio devices and input monitor** panel lists available input and output
devices, sample rate, stream period, callback count, and dropout counters.
Cicada opens the selected endpoints when playback starts and releases them when
it stops. Change device selections while stopped. Input capture is enabled by
default on Windows; input monitoring starts muted to prevent feedback. Use the
gain and stereo/mono channel controls to monitor an input. Device latency is
shown only when the host reports it. macOS keeps Oto's system-default output.
While the transport runs, the audio status line names the active backend and
output device. If Tymbal cannot open a device, Studio shows the device error in
its status bar and keeps playback stopped. Use `--audio oto` or
`CICADA_AUDIO=oto` to choose Oto explicitly.

Studio also exposes live controls to same-origin page tools through
`window.cicadaAudio`. Call `params()` to read the supported addresses and their
types, units, and ranges. `setParam(address, value)`, `setMute(track, on)`, and
`setSolo(track, on)` apply validated runtime changes during playback. They do
not write the score; the source file remains authoritative. Live overrides
follow a track when a valid score edit keeps that track's ID. Source-level
parameter paths are available in scene settings; see
[parameter paths](../spec/accepted.md#parameter-paths-and-scene-settings).

### History

History groups this Studio session's edits by transport bar. It is a short
session record held in memory and is cleared when Studio exits; the score file
and Git remain your durable history. Studio retains at most the latest 512
events.

### Voice

Voice cards explain each sound source in the score. Depending on the voice,
you can inspect its declared parameters and track overrides, the tracks that
use it, its signal bindings and output graph, or the drum-lane routing for an
authored kit. These cards are a read-only view of the validated project.

## Safe editing

Studio edits the same `.cicada` source you can open in a text editor. It
validates a proposed change before writing, checks that the file revision has
not changed, and refuses writes when it detects an unresolved external
recovery file. If Studio reports a conflict, read the message, compare the
named recovery file with your current score, and resolve the edits before
continuing.

If you want Studio beside VS Code, install or run the extension described in
[Editors and notation tools](editors.md#vs-code).
