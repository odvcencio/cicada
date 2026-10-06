# Hosting Cicada's audio engine

This chapter is for developers embedding Cicada in a native Go host or a
TinyGo/WebAssembly runtime. If you only want to write scores or use Studio, see
[Writing music](writing-music.md) and [Studio](studio.md).

## Native Go

On Windows and Linux, `cicada play` and Studio select Tymbal by default
(`WASAPI` on Windows and `ALSA` on Linux). Cicada reports the selected backend
and device when playback starts, and it does not fall back silently. Use
`--audio oto` when you need Oto's system-default device, or set
`CICADA_AUDIO=oto`. Use `--audio null` for real-time rendering without an
audio device. macOS continues to use Oto by default.

Parse and validate source on the host, convert it to a typed project, and
compile the project before starting audio. `project.CompileEngine` creates an
engine configuration; `engine.New` validates and copies its track, pattern,
scene, and song tables. `engine.Render` accepts 1–4096 equal-length float32
stereo frames and allocates no memory after engine construction. A render
fault silences the rest of that block and stops transport.

The native `cicada play` command follows the same boundary: compilation,
validation, and engine construction happen outside the audio reader. A valid
save replaces the active engine at a bar boundary and crossfades for five
milliseconds. A failed compile leaves the last valid project playing.

`cicada play` and Studio select Tymbal by default on Windows (WASAPI) and Linux
(ALSA). macOS uses Oto until Tymbal supports CoreAudio. Tymbal does not fall
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
Existing voices encode as version 13; an opted-in experimental guitar uses
version 15. Shipped versions 8..13 remain readable. Version 14 belongs to the
in-flight chord/schedule lanes. The little-endian image carries
mixer routing, effects, instrument graphs, authored kits, patterns, scenes,
and song entries. Use `kernelimage.Encode` instead of constructing the image
bytes yourself.

The guitar runs through the same production AudioWorklet callback as other
voices. Select it in an edition-2 score with `experimental = on` before
compilation. It remains a research prototype without listening acceptance;
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
rejected batch faults the engine. `gosx_audio_msg_drain` reads fixed 16-byte
status records. Drain messages regularly so the host can report queued scenes,
transport changes, and engine errors.

`OpNoteExpression` uses the existing 24-byte record. `Index` identifies the
note started by `OpNoteOn`; `Arg0`, `Arg1`, and the word at byte 12 carry
float32 pitch offset in cents, pressure, and timbre respectively. Pitch is
bounded to −9600…9600 cents; pressure and timbre are 0…1. Other commands still
require the byte-12 word to be zero. A note starts with zero bend and pressure
and centered timbre (0.5). Expression and note-off commands for an old identity
cannot change its replacement. Identity zero preserves the legacy path, and
65535 selects all-off. Live host adapters use identities 1…65534.

Graph instruments expose `pitch_bend` in cents and normalized `pressure` and
`timbre`. The existing `pitch` input includes bend and score vibrato. Built-in
acid voices apply pitch expression; pressure and timbre need a graph that uses
those inputs. Live expression targets monophonic tracks. [`host/midi`](../../host/midi) retains
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
rendering.

For drum patterns, a step command's note field selects a lane index from 0 to
10 in this order: `bd sd ch oh cp rs lt mt ht cb cy`. Clear a rest for one
lane without changing the other lanes at that step. A built-in drum voice
allocates the added lanes when a pattern hits them or the track configures
their parameters; the realtime cap remains 32 voices.

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
that track. This command is an engine interface, not the accepted but
unavailable source-level `chain` syntax.

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
