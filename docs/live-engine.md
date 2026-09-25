# Live engine and WASM host contract

## Native score playback

`cicada play [score.cicada]` opens the system audio device at 48 kHz and streams stereo float32 from the native engine. The command watches the score and its nearest `cicada.mod`. Parsing, validation, graph compilation, and engine construction run outside the audio reader. On a valid save, the newest compiled engine replaces the old one at the exact next bar sample; a five-millisecond crossfade joins their output. Invalid edits leave the previous score running. The song loops until Ctrl-C.

Studio scene pads send the newest requested scene name to the native player. The audio reader resolves that name against the score active at landing and launches it at the next bar. A score replacement landing on that bar takes priority; the scene launches one bar later against the new score, or reports that the scene no longer exists. A manual launch wins over a scheduled song scene at the same bar, and the song resumes at its following boundary.

Studio's scene matrix shows tracks as rows and scenes as columns. A scene header launches the whole scene; a pattern cell queues that track's named slot. The latest queued pattern per track lands at the next bar, including when several tracks are queued together. The player resolves track and pattern names against the score active at landing, and rejects a missing slot without faulting the engine. A score replacement at that bar defers those launches one bar. A manual pattern wins over the song's scene on its own track at the same boundary; other tracks follow the song, which resumes on all tracks at the next boundary.

The native reader accepts arbitrary byte sizes from the audio driver while keeping complete stereo frames in order. Its clock follows the engine's integer tick-to-sample mapping; a tempo edit reanchors at the landing bar. The host unit tests cover exact bar-frame landing, superseded edits, tempo changes, and last-good-score recovery without requiring an audio device.

The host compiles a validated Cicada project outside the audio callback. `project.CompileEngine` returns native `engine.Config` with owned track, pattern, scene, and song data; `engine.New` validates and copies those tables. `Render` accepts 1–4096 equal-length float32 stereo frames and allocates nothing after `New`. A fault silences the remaining block and stops transport.

## TinyGo startup

Call `_initialize` before any `gosx_audio_*` export. For a complete project, parse and typecheck the score in the host, call `project.CompileEngine`, then encode its configuration with `kernelimage.Encode`. Call `gosx_audio_project_alloc(imageLength)` once, copy the returned image bytes to that pointer, and call `gosx_audio_init(sampleRate, maxBlock, 2)`. The version 8 image has a little-endian header and contains track mixer and bus routing, drive insert, delay send, reverb send, and music compressor settings, acid/drum parameters, custom graph programs, authored kit lane bindings, sixteen pattern slots per track, all eleven drum lanes, scenes, and song entries. Allocation accepts 32 bytes through 2 MiB and must happen before initialization. A new module instance is required to load another project.

The older setup exports remain available for direct host control. Before `gosx_audio_init`, choose up to sixteen track kinds with `gosx_audio_track_kind(index, kind)` (`0` off, `1` acid, `2` drums). Call `gosx_audio_arrangement_alloc(sceneCount, songEntryCount)` once if an arrangement is present. It returns `0` on success. Then write the two arrays through `gosx_audio_scene_ptr` and `gosx_audio_song_ptr`:

- Each scene is sixteen bytes in track order. A binding is `0` keep, `1` off, or `2..17` for slot `0..15`. Unused tracks must be `keep`.
- Each song entry is four little-endian bytes: `uint16 sceneIndex`, then `uint16 bars` (1–999). `gosx_audio_song_loop(1)` enables looping; the default is stop at the song end.

`gosx_audio_init(sampleRate, maxBlock, 2)` validates the configuration. Its return is `0` on success and `-1` on failure. Project-image and older setup exports cannot be mixed before initialization.

## Audio and commands

`gosx_audio_cmd_ptr` points to 512 fixed 24-byte command records; `gosx_audio_cmd_cap` returns that record count. Write a whole batch and call `gosx_audio_cmd_commit(n)`. Commands are little endian and validated as a batch. A rejected batch faults the engine. `gosx_audio_render(frames)` writes planar float32 stereo to `gosx_audio_out_ptr`; the right channel starts `maxBlock` frames after the left. `gosx_audio_msg_drain` returns a count of 16-byte records at `gosx_audio_msg_ptr`. Drain them regularly to preserve status messages.

For a drum track, `OpSetStep` uses the packed step's note field as a lane index (`0..10` for `bd sd ch oh cp rs lt mt ht cb cy`). Send one record per lane and step; several lanes can fire at the same step. A rest record with that lane index clears only that lane. `OpSetPatternLen` and `OpSetPatternMeta` apply to every lane in the slot. `OpLaunchScene` switches a loaded scene at the requested quantization; `keep` retains a slot and `off` releases it. Pattern-end scene quantization waits for the first end shared by every active track, accounting for different pattern lengths and restart offsets. If their boundaries never meet, or the next shared end cannot fit in the transport's tick range, the engine emits fault 18.

`OpSetChain` builds a per-track list of up to 32 `(slot, repeats)` entries. `Index` is the list position, `Arg0` is `slot | repeats<<8`, and `Arg1` is zero. Position zero replaces and arms the list; later positions append contiguously or replace an existing entry. An active pattern finishes before the first entry starts. Each entry begins at its first step, plays for `repeats` complete cycles of its own length, and advances; the list loops. A direct pattern selection or scene slot/off binding takes over the track. Seek restarts a configured chain from its first entry.

A sliding note on the outgoing pattern carries into a playable first step of a quantized pattern, scene, or song switch. The incoming note changes pitch without retriggering the acid accent envelope. A rest or failed probability check in the incoming slot leaves the outgoing note at its ordinary gate length, including when swing puts that gate end after the switch boundary.

`OpSetLayerMask` excludes disabled tracks from the dry mix at its scheduled bar boundary. Their sequencers and voices keep advancing while muted, so an envelope release or drum tail does not freeze and reappear when a layer returns. The master limiter can still emit audio already in its lookahead buffer immediately after a mask change.

The offline PCM24 WAV renderer applies the same carry and normal-gate rule at song scene boundaries. Its boundary output is checked against the native live engine for a custom mono instrument with a playable target, a rest, and a missed probability step.

`make test-timing` checks exact musical boundaries and block-size invariance; `make test-alloc` checks render allocations, including a sixteen-track chain. Reference-host callback P99 and long soak gates remain for M2.
