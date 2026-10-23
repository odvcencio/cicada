# Kernel image version 16: performed note gates

Version 16 keeps the complete [version 15 layout](kernel-image-v15.md), with
the version field set to 16, and appends one performance table after the unified
footer. Images without performed gates retain their existing version and bytes.
The image limit stays 2 MiB. Command and message record sizes stay unchanged.

Hosts must negotiate both `CapabilityUnifiedImage` (bit 16) and
`CapabilityPerformance` (bit 18) before uploading a version-16 image. Bit 18 is
in the reactor's 32-bit capability export, outside the legacy 16-bit image
header flags. An older kernel must reject the image before changing its state.

The additive table contains:

| Field | Encoding |
| --- | --- |
| Entry count | `u16` |
| Track, slot | `u8`, `u8` per entry |
| Loop length | `u64` musical ticks per entry |
| Note count | `u16` per entry |
| Each note | `u64 Tick`, `u64 EndTick`, `u8 Pitch`, `u8 Velocity` |

Each target is a unique, active one-cell placeholder slot with no gated grid
steps, chords, expression, swing, or alternate grid. The placeholder does not
determine the pattern duration: the explicit loop length does. Length is
30 through 2^40 ticks, with 1–512 notes. A note starts at or after zero, ends
strictly after its start and no later than the loop end, and has MIDI pitch
0–127 and velocity 1–127. Gates for the same pitch cannot overlap in one slot.
An authored transpose must keep every pitch in range. Instrument-specific
admission still applies; modeled keyboards require MIDI 21–108.

Every start and release is scheduled independently at 960 PPQ using the
integer clock. Releases precede new starts at equal samples, including a loop
boundary. The engine admits event and message storage before rendering and
copies caller-owned note data. Stop, reset, and switching release owned gates;
seek and resume within a held gate retrigger its attack. Offline range renders
warm from the beginning to preserve the original sound state.

The existing voice policy still determines mono priority and voice stealing.
The score and image retain all admitted note events. Unsupported instrument
types and per-note expression are explicit errors. Incremental step opcodes
cannot carry this immutable table: editing a performance requires a complete
project revision, rather than a step upload that would lose its gates.

Portable image admission also bounds sustained performed-note status traffic.
For each track, take the busiest slot's `ceil(2 × note count × window ticks /
loop length)`, using eight 128-frame quanta at 300 BPM. Their sum must not
exceed 128 records, leaving the remaining status throughput for other events.
An excessive rate is refused on encode and decode; native playback and WAV
renders retain their independently prepared capacity. This avoids accepting a
very short, dense loop that would later halt browser audio with message overflow.
