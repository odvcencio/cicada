# Kernel image version 18: shared gesture lanes

Version 18 retains the complete [version 17 payload](kernel-image-v17.md),
sets the header version to 18, and appends the table below. Its version-17
member table may be empty, allowing shared controls without dummy member
points. Other images retain their existing version and bytes.

Hosts negotiate `CapabilitySharedControls` (bit 21), together with bits 16,
18 and 19. Command and message records, the 2 MiB image limit, and the fixed
kernel/worklet download budgets remain unchanged.

| Field | Encoding |
| --- | --- |
| Table count | `u16`, 1 to tracks × 16 |
| Target per table | `u8 track`, `u8 slot`, `u16 lane count` |
| Each lane | `u8 name byte length`, UTF-8 name bytes, `u32 point count` |
| Each point | `u64 pattern tick`, `f32 pitch cents`, `f32 pressure`, `f32 timbre` |
| Note bindings | `u16 note count`, then one `u16 lane ordinal` per existing note |

Targets are distinct performed patterns on acid or authored graph tracks.
A pattern has 1–512 lanes, each with a unique 1–64-byte name matching
`[a-z_][a-z0-9_-]*` and at least one point. Bindings are 1-based; zero means
unbound. The note count must match the existing performance exactly.
Points increase strictly in `[0, length_ticks)`. Bend is −9600…9600 cents;
pressure and timbre are 0…1. Every value must be finite. The whole image
admits at most 65,536 member and shared points combined. Shared points and
implicit loop resets also count toward the existing controller-density gate.

Shared bend adds to the member bend and clamps to ±9600 cents. Pressure
uses the larger value; timbre sums around 0.5 and clamps to 0…1. The note's
own vibrato keeps its phase. Member values freeze at gate release, including
a point exactly at the release tick; shared controls continue through the
audible tail. This endpoint behavior applies to grouped notes only.

A new loop resets the shared lane to neutral before its points. A tick-zero
point replaces that reset. Notes chase the lane at their onset; seek starts
a fresh voice and restores the current member and shared snapshots.
Pattern generations, note ownership and voice lifetime guard all updates.
Changing selection freezes outgoing tails; stolen voices cannot inherit an
old lane. Stop detaches shared ownership without cutting the release.

Grouped performances retain the authored instrument's eight-voice pool.
A track that also needs chord-step or legacy performed-expression cohorts
uses the existing four-voice pool throughout. Monophonic priority is unchanged.

The scheduler, expression composition and voice updates allocate nothing
during rendering. Reactor builds return numeric failures and omit unused
validation text; native and other TinyGo builds retain detailed diagnostics
with the same validation checks.
