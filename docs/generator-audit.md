# M1 generator audit

The reference set in [`examples/gen`](../examples/gen/) contains 24 fixed
A-minor sources and the seed-4242 PCG draw trace. The first eight raw draws
match Hyphae spec 04. `TestSeedFixturesAndReferenceTrace` checks byte equality.

`make test-phrase-wasm` builds the generator with TinyGo and runs it in Wazero.
For 24 fixed seeds, all eight scales, all five structures, and all four step
counts with alternate densities, the WASM source and serialized draw trace
match native Go byte for byte. TinyGo warns that one gotreesitter diagnostic
function has a large parameter list; Wazero accepts the module, while other
WASM runtimes have not been checked.

`TestGenerateDefaultSeedPopulation` examines 10,000 seeds in each of the eight
scales with default AABA settings: 80,000 generated phrases and 320,000 bars.
The current code passes pitch range, slide target, interval, root presence,
and rhythmic active-cell checks. Root-class share is within the specified
30–45% range.

The initial implementation missed the **two-to-six accent gate** in 779 of
320,000 bars. The first was minor seed 97, B bar, with one accent: the A bar
had two, then the specified `ToggleAccent` mutation turned one off.

This branch proposes a deterministic repair for default-density 16-step
generated bars after the base and structure passes. It adds an accent to the
strongest unaccented onset when a bar has fewer than two, or removes accents
from the weakest onsets (preserving an accented downbeat) when a bar has more
than six. Strength, octave class, and step index break ties in that order.
The repair makes no random draw and does not change the underlying mutation
operation. The seed-97 test confirms that the original mutation still occurs
and that the complete draw trace is unchanged.

With this candidate rule, `TestGenerateDefaultSeedPopulation` passes all
320,000 bars across 10,000 seeds in each of eight scales; root-class share
remains 42.4%. The 24 checked-in sources and seed-4242 trace remain byte
identical. This is a **proposed spec 04 change**, not an accepted M1 gate: the
spec requires a documented decision before a failed distribution rule may
change. Keep M1 generator acceptance open until the rule is adopted and the
owner reviews the listening set.
