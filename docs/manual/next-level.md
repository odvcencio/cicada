# Live playback, mixing, and what comes next

This chapter separates current behavior from the features still under review.
The detailed track and effect limits are in the
[language specification](../spec/edition-1.md#effects).

## Available on main

| Area | Current behavior |
| --- | --- |
| Live playback | `cicada play` watches a score and loops it through the native audio engine. Valid saves take effect at the next bar; an invalid save leaves the last valid score playing. |
| Studio transport | Studio can play or stop the song, launch scenes or individual track patterns at a bar boundary, and start the song at a selected block. It shows track and master peak levels in dBFS while playing. Use `--audio null` for real-time rendering without an audio device. |
| Live controls | The page's `window.cicadaAudio` API lists typed live addresses and can set a parameter, mute a track, or solo a track during playback. Values are validated; overrides do not write the score and follow a track with the same ID across valid source edits. |
| Mixing | Track source supports level, pan, a drive insert, delay and reverb sends, pre-fader sends, and the music or SFX bus. A music-bus compressor can use a track or the SFX bus as its sidechain. |
| Master output | A linked stereo limiter protects the output at −0.3 dBFS with 1.5 ms lookahead. WAV export can also peak-normalize to −1 dBFS. |
| Recording | There is no audio-input recording or take lane in the current build. |
| Automation | There are no automation lanes or source automation blocks in the current build. |

Mixing settings live in the score, for example:

```cicada
fx drive { shape = hard gain = 9dB }
fx delay { time = 1/8 feedback = 0.35 }
fx reverb { decay = 2.4s }

track bass acid {
  level = -3dB
  pan = -0.2
  insert = drive
  send_a = 0.3
  send_b = 0.2
  send_pre = true
  bus = music
}

track drums drums { bus = sfx }
pattern bassline acid { 1^ . 1~ 5 }
pattern beat drums { bd: X... }
scene main { bass = bassline drums = beat }
song { main*4 }
```

The fixed effect graph is drive on a track, delay on send A, reverb on send B,
an optional music-bus compressor, then the master limiter. Studio has no
interactive mixer panel or dedicated mastering chain. Its peak meters do not
measure integrated loudness. Use the [WAV chapter](exporting.md#wav) for export
controls and verifiers.

## Coming next

These descriptions set user-facing direction; they are not a release schedule.
Only items marked **accepted** have an owner-approved notation design. Every
accepted syntax example is labeled unavailable in the
[specification](../spec/accepted.md).

| Feature | Status | What it will do |
| --- | --- | --- |
| Expanded live mode | Proposed | Extend the current next-bar transport into a fuller performance workflow while keeping source edits and playback state together. |
| Recording | Proposed | Capture an audio or performance take so you can keep it with a project and arrange it with your score. No recording syntax or file format is available yet. |
| Parameter paths | Accepted | Give each track, bus, or effect setting a typed address that scenes, automation, and tools can refer to consistently. |
| Named mixing | Accepted | Give effects and buses names, route tracks and buses explicitly, and order inserts on tracks, buses, and the master. |
| Mastering | Proposed | Add a dedicated final-stage workflow for shaping and checking a delivery render. The current limiter and peak normalization are the only final-stage controls. |
| Automation | Accepted | Change a typed parameter over song time with lanes that can live at song, scene, or pattern scope. The accepted design is not in the parser, semantic JSON, or Studio. |
| Flexible grids and chains | Accepted | Choose a step duration, use subdivided groups and per-step parameter rows, and arrange patterns into a repeating chain. |
| Multi-file projects | Accepted | Let scores in one project share declarations while imports expose reusable libraries. Files still compile independently today. |

For automation, named mixing, flexible grids, chains, and multi-file source,
read the syntax and current limits in
[accepted but unavailable designs](../spec/accepted.md). The
[engine-host chapter](engine-host.md) covers the current native and TinyGo
runtime for developers.
