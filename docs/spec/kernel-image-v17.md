# Kernel image version 17: performed note gestures

Version 17 retains the complete [version 16 payload](kernel-image-v16.md),
sets the version field to 17, and appends a gesture table. Images without
performed gestures keep their existing version and bytes. The 2 MiB image
limit and command/message record sizes are unchanged.

Hosts must negotiate `CapabilityUnifiedImage` (bit 16),
`CapabilityPerformance` (bit 18), and `CapabilityGestures` (bit 19) before
uploading this image. The browser refuses an older cached kernel before
installing a project or sample bank.

| Field | Encoding |
| --- | --- |
| Entry count | `u32`, nonzero |
| Target per entry | `u8 track`, `u8 slot`, `u16 note ordinal` |
| Sample count per entry | `u32`, nonzero |
| Each sample | `u64 relative tick`, then five `f32` values |
| Float order | Pitch cents, pressure, timbre, vibrato rate Hz, vibrato depth cents |

Targets must identify distinct existing performed notes on acid or authored
graph tracks. A table contains at most 65,536 samples in total. Positions
must increase strictly and fall between zero and the note's duration,
inclusive. Endpoint samples remain in source and images but cannot affect
a released or replacement note. Every decoded sample has `Set=true`.

Pitch is −9600…9600 cents, pressure and timbre are 0…1, vibrato rate is
0…100 Hz, and depth is 0…9600 cents. All values must be finite. Values hold
until the next sample; there is no implicit interpolation. Notes start with
zero bend, pressure and vibrato, and timbre 0.5. Zero rate or depth pauses
vibrato phase. Authored polyphonic instruments use the existing four-voice
cohort pool for independent gesture ownership. Monophonic instruments
retain their note priority; gestures from an older held note cannot address
the current note or a live overlay.

The scheduler searches directly to the current block's samples and sorts
events in prepared storage without callback allocations. Note identity and
pattern generation guard every update. Seek retriggers held notes, restores
the last controller value, and starts vibrato phase again with the attack.
Moving a note keeps all gesture offsets. Shortening refuses to discard them.

Admission computes each track's busiest slot over a closed cyclic window of
eight 128-frame quanta at 300 BPM, rounded up by one musical tick. Across
tracks, at most 256 controller events may fall in that window. Native and
portable engines enforce this before playback; they also prepare enough
event storage for their configured render block. Version 16's separate
portable note-status admission still applies.
