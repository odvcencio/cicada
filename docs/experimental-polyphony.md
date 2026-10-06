# Experimental opt-in graph chords

Opt-in graph tracks support bounded four-voice chords from source to audio.
Existing `voice mono` behavior and mono project images remain the default.

```cicada
instrument piano {
  voice poly { out = sine(pitch) * env(gate, 300ms) * velocity * 0.1 }
}
track keys piano {}
pattern harmony notes { [d4 f4 a4]^?70 - . [c4 e4 g4] }
scene verse { keys = harmony }
song { verse }
```

## Musical contract and limits

- `voice poly` is explicit and applies only to typed graph instruments. A track
  assigned chord patterns allocates four slots against the 32-voice project limit.
  Scalar polyphonic patterns retain the eight-voice Live instrument mode
- Chords contain 2–4 distinct resolved MIDI pitches. Octave marks and phrase
  transpose work. Suffix accent/chance applies to the complete cohort; `-`
  holds all its pitches with one gate and release
- Chord slides, ratchets, and slides whose target is a chord are unsupported.
  Across a scene/pattern boundary a scalar slide into a chord starts a fresh
  cohort instead of attempting polyphonic portamento
- One sequenced gate cohort is current at a time. A fresh non-slide onset closes
  the previous cohort, including a longer chord replaced by a shorter one.
  Release/declick tails may overlap within the same four fixed slots
- Voices choose the lowest free slot, then the quietest releasing slot estimated
  from peak level × remaining release plus its tail, then the oldest gated note.
  Equal rankings choose the lowest index. Sum order is fixed ascending-slot order
- Release is bounded to 30 ms; one 5 ms declick tail belongs to each stolen slot.
  Handles remain monotonic across reset; stale handles/generation releases cannot
  stop replacements. The opt-in pool reseeds graph noise on reset/reuse; legacy
  mono graph reset behavior is unchanged
- A chord stays one mixer track and one track stem. Legacy direct live `NoteOn`
  and `NoteOff` on poly tracks are rejected with fault20 and a handle-aware-command
  explanation. Polyphonic keyboard/MIDI-input editing is not supported
- Graph accent metadata is shared. As with legacy graph instruments, a graph
  author controls dynamics through the `velocity` input; there is no separate
  graph `accent` input. MIDI export maps accent to velocity127
- Studio pitch edits add or remove only the clicked chord pitch, preserving
  the other pitches and shared modifiers. Phrase edits update their shared source

## Interchange and host compatibility

Semantic steps add optional numeric `notes:[62,65,69]`; mono JSON omits `notes`.
The original scalar `note` remains the first pitch. Packed mono steps and all
existing 24-byte command records/opcode numbers remain unchanged.

Complete project image upload is the supported path for arbitrary gate/seed
metadata. Mono images stay byte-exact version13. An opted-in poly project uses
version15, which includes per-track polyphony, fixed chord payloads and an
explicit schedule/asset/clip footer, even when empty. Both incompatible
development-v14 dialects are rejected; recompile project source. Older
readers reject the new image version; the worklet checks capability before
allocation/upload and rejects unsupported kernels explicitly.
The host must call the reactor's `_initialize()` before querying capability;
TinyGo exports trap if called before runtime initialization.
The WASM export `gosx_audio_capabilities()` retains bit0 (`1`) for
`OpSetChordStep` (appended opcode22). Bit16 (`65536`) independently negotiates the
[unified v15 image layout](spec/kernel-image-v15.md); bit0 alone never authorizes
a v15 image. Opcode22 uses four 7-bit pitches in `Arg0`;
`Arg1` low4bits selects slot0–15 and bits4–6 contain count2–4. `Index` is step0–63.
Padding and reserved bits remain zero. It supplements a normal `OpSetStep` and
cannot turn a mono track into a polyphonic one.

`kernelimage.PatternCommands(pattern, loadedConfig, track, slot, capabilities)`
creates an ordered command batch. `loadedConfig` must describe the current
kernel, not the source project. Source gate/seed must match that slot's preloaded
metadata (blank-slot defaults are gate55 and the engine seed); mismatches reject
before producing any commands. Existing opcodes cannot represent a different
pattern gate/seed. Use a complete image when those values change. The batch
clears the loaded and replacement step ranges before replacing length/meta/notes,
avoiding invalid intermediate old-chord/new-slide or transpose states. Submit it
as one batch; do not interleave a playback callback between its records.

Fixed pattern banks are validated by address and then copied into engine
storage, so the engine owns its patterns without whole-bank scalarization.

## Inherited interchange limitations

Offline probability hashing uses the semantic assigned slot for every track,
including later scenes and pending gates.

Inherited mono probability behavior remains untouched. In particular, MIDI
export deliberately uses iteration0 on repeated pattern steps. Thus realized
MIDI chance events can differ from playback: the regression with seed7 and a
one-step `[d4 f4]?50` yields 10 audio cohorts and 16 MIDI cohorts in one bar. MIDI chance realization can therefore differ from audio playback.
Mono offline rendering also retains its slot0 hashing limitation.
