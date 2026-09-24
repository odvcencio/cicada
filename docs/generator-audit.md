# M1 generator audit

The reference set in [`examples/gen`](../examples/gen/) contains 24 fixed
A-minor sources and the seed-4242 PCG draw trace. The first eight raw draws
match Hyphae spec 04. `TestSeedFixturesAndReferenceTrace` checks byte equality.

`TestGenerateDefaultSeedPopulation` examines 10,000 seeds in each of the eight
scales with default AABA settings: 80,000 generated phrases and 320,000 bars.
The current code passes pitch range, slide target, interval, root presence,
and rhythmic active-cell checks. Root-class share is within the specified
30–45% range.

The **two-to-six accent gate remains open**. The audit found 779 bars outside
that range. The first is minor seed 97, B bar, with one accent: the A bar has
two, then the specified `ToggleAccent` mutation turns one off. The test logs
this count and example, while continuing to enforce the independent gates.

Changing `ToggleAccent` eligibility or adding an accent repair would change
the specified algorithm and draw trace. Hyphae spec 04 requires a documented
spec change for a failed distribution gate. Do not accept M1 generator audio
or replace the reference fixtures until that rule is resolved and the 10,000
seeds per scale are rerun.
