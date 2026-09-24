# Live engine and WASM host contract

The host compiles a validated Cicada project outside the audio callback. `project.CompileEngine` returns native `engine.Config` with owned track, pattern, scene, and song data; `engine.New` validates and copies those tables. `Render` accepts 1–4096 equal-length float32 stereo frames and allocates nothing after `New`. A fault silences the remaining block and stops transport.

## TinyGo startup

Call `_initialize` before any `gosx_audio_*` export. Before `gosx_audio_init`, choose up to sixteen track kinds with `gosx_audio_track_kind(index, kind)` (`0` off, `1` acid, `2` drums). Call `gosx_audio_arrangement_alloc(sceneCount, songEntryCount)` once if an arrangement is present. It returns `0` on success. Then write the two arrays through `gosx_audio_scene_ptr` and `gosx_audio_song_ptr`:

- Each scene is sixteen bytes in track order. A binding is `0` keep, `1` off, or `2..17` for slot `0..15`. Unused tracks must be `keep`.
- Each song entry is four little-endian bytes: `uint16 sceneIndex`, then `uint16 bars` (1–999). `gosx_audio_song_loop(1)` enables looping; the default is stop at the song end.

`gosx_audio_init(sampleRate, maxBlock, 2)` then validates the configuration. Its return is `0` on success and `-1` on failure. The current module requires a new instance to load a different arrangement.

## Audio and commands

`gosx_audio_cmd_ptr` points to 512 fixed 24-byte command records; `gosx_audio_cmd_cap` returns that record count. Write a whole batch and call `gosx_audio_cmd_commit(n)`. Commands are little endian and validated as a batch. A rejected batch faults the engine. `gosx_audio_render(frames)` writes planar float32 stereo to `gosx_audio_out_ptr`; the right channel starts `maxBlock` frames after the left. `gosx_audio_msg_drain` returns a count of 16-byte records at `gosx_audio_msg_ptr`. Drain them regularly to preserve status messages.

For a drum track, `OpSetStep` uses the packed step's note field as a lane index (`0..5` for `bd sd ch oh cp rs`). Send one record per lane and step; several lanes can fire at the same step. A rest record with that lane index clears only that lane. `OpSetPatternLen` and `OpSetPatternMeta` apply to every lane in the slot. `OpLaunchScene` switches a loaded scene at the requested quantization; `keep` retains a slot and `off` releases it. Pattern-end scene quantization currently requires active patterns of the same length and no restart offset; unsupported combinations fault explicitly.

The module currently loads acid and drum voices through this ABI. Native `engine.Config` can also preload custom mono graphs and complete pattern banks. Equivalent WASM graph and typed-project loading, chain commands, full cross-switch slide behavior, and whole-engine parity and soak gates remain to be implemented.
