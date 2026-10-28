# Hosting Cicada's audio engine

This chapter is for developers embedding Cicada in a native Go host or a
TinyGo/WebAssembly runtime. If you only want to write scores or use Studio, see
[Writing music](writing-music.md) and [Studio](studio.md).

## Native Go

On Windows, Linux, and macOS, `cicada play` and Studio select Tymbal by default
(`WASAPI` on Windows, `ALSA` on Linux, and `CoreAudio` on macOS). Cicada reports the selected backend
and device when playback starts, and it does not fall back silently. Use
`--audio oto` when you need Oto's system-default device, or set
`CICADA_AUDIO=oto`. Use `--audio null` for real-time rendering without an
audio device. Oto provides output only; native recording uses Tymbal.

Parse and validate source on the host, convert it to a typed project, and
compile the project before starting audio. `project.CompileEngine` creates an
engine configuration; `engine.New` validates and copies its track, pattern,
scene, and song tables. `engine.Render` accepts 1–4096 equal-length float32
stereo frames and allocates no memory after engine construction. Native hosts can call `EnableParallel(0)` before playback to prepare up to
four render lanes for sessions with at least 32 tracks. Smaller sessions and
short event intervals keep the serial path. Workers sleep between blocks and render independent track
sources and inserts between sample-accurate control boundaries. The caller
reduces tracks in their original order and processes sends, buses and master
effects. Browser kernels stay serial. `Retire` cancels jobs without waiting;
`Close` joins workers off the audio callback before releasing clip readers.
Retired engines cannot be reset or reused. Windows workers request MMCSS
Pro Audio on their own locked threads and restore it on exit; unavailable
priority remains visible through `ParallelPriority`. Other hosts use normal OS
scheduling. The bounded worker join protects ownership after a stalled job;
it does not promise that a callback meets the device deadline.

A render
fault silences the rest of that block and stops transport.

The native `cicada play` command follows the same boundary: compilation,
validation, and engine construction happen outside the audio reader. A valid
save replaces the active engine at a bar boundary and crossfades for five
milliseconds. A failed compile leaves the last valid project playing.

`cicada play` and Studio select Tymbal by default on Windows (WASAPI), Linux
(ALSA), and macOS (CoreAudio). Tymbal does not fall
back silently: if the selected device cannot open, `play` exits with a device
error and Studio leaves transport stopped. Use `--audio oto` or
`CICADA_AUDIO=oto` to choose Oto explicitly.

## Load a project in TinyGo

The project-image interface is the shortest path from a Cicada score to the
TinyGo audio module:

1. Initialize the WebAssembly module with `_initialize`.
2. On the host, compile the score and encode its engine configuration with
   `kernelimage.Encode`.
3. Allocate image memory with `gosx_audio_project_alloc`, copy the encoded
   bytes into it, then call `gosx_audio_init(sampleRate, maxBlock, 2)`.
4. Call `gosx_audio_render(frames)` to render planar float32 stereo output.

The image allocation accepts 32 bytes through 2 MiB and happens before audio
initialization. A new module instance is required to load a different project.
Legacy projects encode as version 13. Graph chords, schedules, prepared audio,
and the experimental guitar use version 15. Independent performed note gates
use [version 16](../spec/kernel-image-v16.md) and require
`CapabilityPerformance` (bit 18), together with `CapabilityUnifiedImage`.
Performed controller gestures use [version 17](../spec/kernel-image-v17.md)
and additionally require `CapabilityGestures` (bit 19).
Shared gesture lanes use [version 18](../spec/kernel-image-v18.md) and
additionally require `CapabilitySharedControls` (bit 21).
Versions 8–13 remain readable;
version 14 is rejected. See the [image format](../spec/kernel-image-v15.md). The little-endian image carries
mixer routing, effects, instrument graphs, authored kits, patterns, scenes,
and song entries. Use `kernelimage.Encode` instead of constructing the image
bytes yourself.

The guitar runs through the same production AudioWorklet callback as other
voices. Select it in an edition-2 score with `experimental = on` before
compilation. It uses an experimental physical model;
see [its controls and limits](../spec/edition-2.md#experimental-guitar-voice).
`gosx_audio_allocation_count` returns the TinyGo heap allocation count so
hosts can verify that rendering, including sequenced note/control changes,
does not allocate. Read it outside a timed callback; memory size alone cannot
detect allocations that fit within the existing heap.

The older direct-setup exports remain available when a host needs to set track
kinds and arrangement tables itself. Set up to sixteen track kinds with
`gosx_audio_track_kind(index, kind)`: 0 is off, 1 is acid, and 2 is drums.
These setup calls return 0 on success and −1 on invalid input. Call
`gosx_audio_arrangement_alloc(sceneCount, songEntryCount)` once before
initialization; it also returns 0 on success. Each scene is 16 bytes in track
order, with 0 for keep, 1 for off, and 2–17 for pattern slots 0–15. Each song
entry is four little-endian bytes: a 16-bit scene index followed by a 16-bit
bar count from 1–999. Call `gosx_audio_song_loop(1)` to loop at song end; the
default is to stop. Do not mix project-image loading and direct setup in one
module before initialization.

`gosx_audio_init(sampleRate, maxBlock, 2)` creates the engine and returns 0 on
success. It rejects invalid project data, channels, or block sizes and returns
−1. The block limit is 4096 frames.

## Live command and status buffers

`gosx_audio_cmd_ptr` exposes 512 fixed 24-byte little-endian command records.
Write a complete batch and commit it with `gosx_audio_cmd_commit(n)`; a
rejected batch faults the engine with code 42. `gosx_audio_msg_drain` reads
fixed 16-byte status records, up to 512 per call (the status ring size, `engine.MessageCapacity`), so size the view over `gosx_audio_msg_ptr` as 512 x 16 bytes. Drain messages regularly so the host can report
queued scenes, transport changes, and engine errors. See
[Fault codes](#fault-codes).

`OpNoteExpression` uses the existing 24-byte record. `Index` identifies the
note started by `OpNoteOn`; `Arg0`, `Arg1`, and the word at byte 12 carry
float32 pitch offset in cents, pressure, and timbre respectively. Pitch is
bounded to −9600…9600 cents; pressure and timbre are 0…1. Spatial commands also
use `Pad` for their third coordinate, as do shared live controls; other commands
require the byte-12 word to be zero. A note starts with zero bend and pressure
and centered timbre (0.5). Expression and note-off commands for an old identity
cannot change its replacement. Identity zero preserves the legacy path, and
65535 selects all-off. Live host adapters use identities 1…65534.

Graph instruments expose `pitch_bend` in cents and normalized `pressure` and
`timbre`. The existing `pitch` input includes bend and score vibrato. Built-in
acid voices apply pitch expression; pressure and timbre need a graph that uses
those inputs. Authored four-voice pools and eight-voice graphs also support
independent live notes. Negotiate `CapabilityLiveIdentity` (bit 20) before
sending note-on/off with `Arg1=1`; `Index` then names a live owner 1…65534,
separate from sequenced cohorts. An identity-mode off with index 65535
releases only live owners. Score switches preserve held live voices, subject
to the shared voice limit and stealing; transport Stop releases all voices.
Do not use the slide bit with identity-mode note-on.

Negotiate `CapabilityLiveGroups` (bit 22) for separate shared live controls.
`OpBindNoteGroup` (32) binds the held live note in `Index` to the group ID in
`Arg0`; both IDs are 1…65534 and `Arg1`/`Pad` are zero. It also supports the
current identity of monophonic acid and graph voices. `OpSetLiveGroup` (33)
uses `Index` for that group and the same three float32 words as note expression.
Send a complete group snapshot with each binding. Same-tick application orders
note-on, member controls/binding, then shared controls. Shared bend adds,
pressure takes the larger value, and centered timbre adds before clamping.
Member controls stop at gate release; the shared layer continues through audio
release. Stolen slots lose their binding, and Stop detaches groups. Keep group
IDs private to their input session. Reuse an ID only after all its voices are
gone or detached; the native audio reader can query `Engine.LiveGroupActive`.

GoSX Live records MIDI 1 bend, channel/poly pressure and CC74 snapshots with
their input ticks and note identity. Ordinary channels default to ±2 semitones;
RPN 0 sets sensitivity and RPN 6 configures lower/upper MPE zones. Manual zone
selection supports controllers that omit setup messages. Sustain retains gates
until pedal release. Ordinary channel bend, channel pressure and CC74 use a
shared channel layer; polyphonic aftertouch stays local to each note. Channel
and polyphonic pressure combine using the larger value. Local values freeze
under sustain after key release; channel and zone controls still affect held
gates and audio release tails. Reconnecting an input gets a separate shared address. Modeled keyboards, piano, drums and
samplers still lack gesture monitoring; retained unsupported gestures are
refused on apply. Raw takes retain up to 65,536 snapshots, including equal-tick
samples, before the edit service derives playable points. Private recovery
checkpoints with a capture project receipt use version 4 and retain
`sourceRevision` unchanged for the life of the take. Legacy version 3 ordinary
channel lanes (`kind: "channel"`), version 2 MPE lanes (empty kind), and version 1
note copies remain readable. Their missing project receipt requires reviewing
the current destinations before applying; it is never inferred during recovery. Live sends
coalesce controller snapshots between gate/action barriers and batch up to 64 messages.

[`host/midi`](../../host/midi) retains
32-bit MIDI 2.0 pressure and timbre until normalization to the float32 ABI.
It supplies pitch-sensitivity conversion, without a UMP transport or device
negotiation layer. MIDI 2.0 defines high-resolution controllers and per-note
pitch independently of MIDI 1.0 byte messages; see the
[MIDI Association overview](https://midi.org/what-musicians-artists-need-to-know-about-midi-2-0).

Images containing expression rows or graph inputs require
`kernelimage.ExpressionCapability` (bit 3) from `gosx_audio_capabilities`. Existing
images keep their layout; the capability appends expression records to each
pattern record and is rejected by older readers.

`gosx_audio_render(frames)` writes planar float32 stereo to
`gosx_audio_out_ptr`. The right channel starts `maxBlock` frames after the
left. Keep block sizes within the configured limit and drain messages while
rendering. A frame count outside 1 to the limit faults the engine with code 43.

For drum patterns, a step command's note field selects a lane index from 0 to
10 in this order: `bd sd ch oh cp rs lt mt ht cb cy`. Clear a rest for one
lane without changing the other lanes at that step. A built-in drum voice
allocates the added lanes when a pattern hits them or the track configures
their parameters. Native playback supports up to 64 tracks and 256 allocated
voices; browser and WAM images retain 16 tracks and 32 voices. Small native
projects keep the portable 32-voice configuration. Larger projects export as
WAV or stems; portable export rejects them before writing an image.
`cicada check --profile browser` applies the portable limits to a score and
lists the voices each track reserves; see
[Check the browser limits](wam2.md#check-the-browser-limits).

The engine can queue scene changes at a quantized boundary. Pattern-end
quantization waits until active patterns reach a shared end. With different
pattern lengths and restart offsets, that boundary may be later than the next
bar. A source save or manual launch can take precedence over a scheduled song
scene at the same boundary.

The runtime also has a per-track chain command for up to 32 `(slot, repeats)`
entries. The command's `Index` is the chain position; `Arg0` packs the slot
and repeat count as `slot | repeats<<8`, and `Arg1` is zero. Position zero
replaces and arms the list; later positions append or replace an entry.
Patterns finish before the chain begins, and each entry plays its pattern for
the requested number of full cycles. Seek restarts a configured chain from
its first entry. A direct pattern selection or scene assignment takes over
that track. Source-level `chain` declarations are not supported.

A layer-mask command can mute tracks at the scheduled bar while their
sequencers and voices keep advancing. At a quantized switch, a sliding note
can carry into a playable first note of the incoming pattern without
retriggering the acid accent envelope. A rest or missed chance leaves the
outgoing note at its normal gate length.

The authoritative buffer layouts and engine constants are in the public
[`host/kernelimage` package](../../host/kernelimage) and
[`kernel/engine` package](../../kernel/engine). The project format and its
fields are in the [semantic reference](../spec/semantic-model.md).

`make test-timing` checks musical boundary timing and block-size invariance.
`make test-alloc` checks render allocations, including a sixteen-track chain.
The amp cost check uses per-thread CPU time on Linux and macOS so scheduler
delays do not count as DSP work during the parallel test suite. `make test-timing`
also runs the separate 670 µs wall-clock check; run it on a quiet machine.

Audio WASM builds need Go, TinyGo, and Python 3. They use an explicit `WASMOPT`
executable when set, otherwise TinyGo's bundled `wasm-opt`, then Binaryen on
`PATH`. The core, vocals, and sampler builds retain their 180-second default
deadline and five-second termination grace on both Linux and macOS; GNU
`timeout` is not required. `KERNEL_WASM_BUILD_TIMEOUT` changes the deadline.

## Fault codes

A `Fault` message (kind 7) means the engine stopped. It silences the rest of the
block and stops transport, and its `A` field holds one of the codes below. In
Go, `Engine.Reset` clears the fault. The Go names are constants in
[`kernel/engine/faults.go`](../../kernel/engine/faults.go); codes 20 to 22 are
in [`kernel/cmd`](../../kernel/cmd/command.go). Match on the number, not on the
text.

| Code | Go name | Meaning |
|---:|---|---|
| 1 | `FaultRenderBlock` | A render call got empty or unequal buffers, or more frames than the block limit. |
| 2 | `FaultAcidVoice` | The acid voice reported a fault. |
| 3 | `FaultDrumVoice` | The drum voice reported a fault. |
| 4 | `FaultLimiter` | The master limiter reported a fault. |
| 5 | `FaultPendingFull` | The queue of commands that wait for a future tick is full. |
| 6 | `FaultSeek` | The transport rejected a seek. |
| 7 | `FaultTempo` | The transport rejected a tempo change. |
| 8 | `FaultNoteRange` | A live note-on or note-off named a pitch or drum lane that the track's voice rejects. |
| 9 | `FaultVoiceUnsupported` | The track's voice cannot take the command or note: the track is off, its voice kind lacks the feature, or the sampler cannot pitch its sample to the note. |
| 10 | `FaultUnhandledCommand` | The engine accepted a command that it cannot run. Validation must refuse such a command, so this signals an engine bug. |
| 11 | `FaultMessageOverflow` | The status queue filled with messages that cannot be dropped. Drain messages more often. |
| 20 | `cmd.FaultPolyLive` | A legacy `OpNoteOn`, `OpNoteExpression` or `OpNoteOff` went to a polyphonic track. Use the handle-aware commands. |
| 21 | `cmd.FaultDirectorQuantize` | A director state or stinger asked for phrase quantize before a phrase length was set (a length counts as set when it applies at the same tick as the request or earlier, whichever the host sent first; a length stamped for a later tick does not), or used a quantize value the sequencer rejects. |
| 22 | `cmd.FaultDirectorStinger` | A stinger targeted an off track, or a track that is playing a pattern that is not a stinger. |
| 23 | `FaultDriveInsert` | A track's drive insert reported a fault. |
| 24 | `FaultDelayTempo` | The delay send could not follow a tempo change. |
| 25 | `FaultDelay` | The delay send reported a fault. |
| 26 | `FaultReverb` | The reverb return reported a fault. |
| 27 | `FaultCompressor` | The music-bus compressor reported a fault. |
| 28 | `FaultMasterProcessor` | The master processor reported a fault. |
| 29 | `FaultPreparedVoice` | A prepared voice rejected a play, a slot selection or a note. |
| 30 | `FaultParam` | A parameter command or scene setting could not be applied: the parameter is unknown or not live, the value is out of range, the track or voice does not own it, or a processor refused the value. |
| 31 | `FaultQuantize` | A scene launch or pattern switch used a quantize value that the sequencer rejects. |
| 32 | `FaultSceneIndex` | A scene launch or state change named a scene that does not exist. |
| 33 | `FaultNoSharedPatternEnd` | A scene launch at pattern end found no tick where all active patterns end together. |
| 34 | `FaultClipStart` | A clip could not start: the clip is unknown, the clip voice rejected the note, or no clip voice was free. |
| 35 | `FaultChainPosition` | A chain entry was written past the end of the chain. |
| 36 | `FaultPatternEdit` | A step, chord, length or metadata command would make a pattern invalid. |
| 37 | `FaultPatternEvents` | A pattern produced more events in one block than the track can hold. |
| 38 | `FaultScheduledNote` | The track's voice rejected a note that a pattern scheduled. |
| 39 | `FaultChordUnsupported` | A chord step went to a track whose voice has no chord support. |
| 40 | `FaultPolyNote` | The polyphonic voice pool rejected a pattern note. |
| 41 | `FaultPolyGeneration` | A polyphonic track ran out of pattern generations. |
| 42 | `FaultHostCommandBatch` | The host refused a command batch: the count was out of range, a record was invalid, or the queue was full. The WASM exports send this. |
| 43 | `FaultHostRenderFrames` | The host asked for a frame count outside 1 to the block limit. The WASM exports send this. |
| 44 | `FaultSceneBinding` | A scene-binding command named a scene, track or clip the engine does not have. |
| 46 | `FaultTrackInsert` | A serial track effect reported invalid audio. |
| 47 | `FaultParallelWorker` | A native DSP worker failed or exceeded the bounded join time; replace the engine. |

Numbers 12 to 19 are retired. Earlier builds used each of them for two to five
unrelated faults, so a log line that holds one cannot be decoded with certainty.
No fault uses them now. `OpCue` (14) is reserved: `Validate` rejects it, so a
Go `Push` returns false, and a WASM batch that holds it faults with code 42.

## Position tracks for headphones

Track and listener commands work through the same native and browser kernel.
Check `CapabilitySpatial` (bit 17) in `gosx_audio_capabilities` or the worklet's
ready message before submitting opcodes 26–28.

```go
e.Push(cmd.TrackPosition(0, 1, 0, 0, 0)) // one metre ahead
e.Push(cmd.ListenerPosition(0, 0, 0, 0))
e.Push(cmd.ListenerRotation(.5, 0, 0, 0)) // turn left by 0.5 radians
e.Render(left, right)
```

Positions use metres with X forward, Y left, and Z up. Coordinates must be
finite and within ±10,000 metres. A positioned track folds stereo to mono,
applies its track gain and mute/solo state, and encodes into a first-order
B-format bus. Pan continues to affect stereo effect sends. Ordinary tracks
and effect returns keep their stereo routing. Music and SFX have separate
spatial buses and retain their bus gain, mute/solo, and compression behavior.

The bus uses [AmbiX ACN/SN3D](https://iem-projects.github.io/ambix/apiref/format.html)
channel order: W, Y, Z, X. Distance gain is `1/max(1, distance)`; coincident
sources encode as omnidirectional. Listener translation re-encodes track
positions. Listener rotation rotates the directional bus channels before
headphone decoding and leaves W unchanged.

| Command | Routing and payload |
| --- | --- |
| `OpSetTrackPosition` (26) | Track index; `Index=1`; X/Y/Z float32 bits in `Arg0`/`Arg1`/`Pad`. `Index=0` with zero payload restores stereo. |
| `OpSetListenerPosition` (27) | `Track=255`, `Index=0`; X/Y/Z float32 bits. |
| `OpSetListenerRotation` (28) | `Track=255`, `Index=0`; yaw/pitch/roll float32 radians, each within ±2π. |

Positive yaw turns left, positive pitch looks up, and positive roll raises
the left ear. The listener's local-to-world rotation order is yaw, pitch,
then roll. Commands apply at their absolute transport tick (960 PPQ);
`Tick=0` applies immediately. Position and rotation changes are immediate,
so interpolate them in the host when smooth motion is needed.

The decoder uses six virtual speakers with generic timing, head-shadow,
and front/back/elevation filters. It does not use measured or individualized
HRTFs. All coefficients, histories, and bus samples use bounded storage;
spatial commands, decoding, and `Render` allocate no objects. `Reset` and
seek clear decoder history and retain track positions and listener pose.

Native hosts can call `RenderWithBFormat(left, right, buses)` to capture both
listener-relative buses before decoding into caller-owned `[]spatial.Frame`
storage. Its length must match the output slices. Stereo tracks and effect
returns are absent from this capture. Per-track meters and taps for positioned
tracks report the mono signal before distance attenuation and decoding.

## Live edits

A host that sees the `gosx_audio_live_edits` WASM export (it returns 1) can edit a
playing engine through `gosx_audio_cmd_commit`; no capability bit changes.

| Command | Routing and payload |
| --- | --- |
| `OpSetStep`, `OpSetChordStep`, `OpSetPatternLen`, `OpSetPatternMeta` | As before. Set `Tick` to the next step boundary to land the edit there; `Tick=0` while playing waits for the next bar unless `Pad=1` (`cmd.PatternEditNow`), which applies the edit at once. Works on every pattern voice, including drum lanes (the lane is the step's `Note`). |
| `OpSetSceneBinding` (31) | `Track=255`, `Index=scene`; `Arg0 = track \| mode<<8 \| slot<<16` with mode 0 keep, 1 off, 2 slot, 3 clip; `Arg1=clip`. Changes the binding for the scene's next launch and faults with code 44 on an unknown scene, track or clip. |
| `OpMeterRate` | `Arg0` is the block interval. Bit 0 of `Arg1` adds per-channel track meters: quantities 2 to 5 are left peak, right peak, left RMS and right RMS beside the mono 0 and 1. The ring holds the quantities for every track of the build (512 messages in every build). An engine whose ring is already too full for the whole emission sends the mono pair for every track instead, and the host reports both channels at the mono level. |

Pattern edits run in the order they arrive, whatever their `Tick` values: an edit waits while an earlier edit still waits for its tick, and a seek (or a restart of the song schedule) first applies every edit that arrived before it, at once, because their step boundaries belong to the timeline it ends. A host can therefore check a batch by replaying the queue in order.

Numbers 29 and 30 are reserved for later live-edit commands; `Validate` rejects them.

## Regress audio changes

`make test-golden` compares an eight-bar render of
`examples/first-acid.cicada` against a spectral regression reference. It uses
100 ms windows summarized into 64 frequency bands. This can detect a render
change; it does not judge whether the music sounds good.

Each window uses a Hann taper and an 8192-point FFT at 44.1 or 48 kHz, or a
16384-point FFT at 96 kHz. The 64 log-spaced bands cover 40 Hz through 16 kHz;
left and right channel powers are averaged. Band levels are stored in tenths
of a decibel with a −120 dB floor. A passing comparison requires the sample
count and frame dimensions to match, mean band drift no greater than 0.5 dB,
and maximum band drift no greater than 2 dB.

The binary fixture starts with a 56-byte little-endian header: magic `CIFP`,
version 1, band count, sample rate, sample count, window size, frame count,
and a SHA-256 hash of the first 4096 interleaved stereo float32 frames. The
spectral values follow as signed 16-bit integers. On Linux amd64, the hash
must also match byte for byte; other platforms use the spectral limits.

After an intentional engine change, listen to a preview before updating the
reference with `go run ./cmd/cicada golden --update`. The command reports
spectral drift before it replaces the fixture. The
[conformance section](../spec/semantic-model.md#conformance) explains the
separate regression role of this fingerprint.
