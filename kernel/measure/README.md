# kernel/measure: engine measurement suite

This package measures every voice family and effect in the kernel and fails
when a gated metric gets worse. It is test-only: all code lives in `_test.go`
files, so nothing here links into a build.

```
make test-engine-measurements
```

The suite renders each voice and effect through its public constructor at
48 kHz with fixed seeds. It runs in about 2 seconds on a development machine
(budget: under 5 minutes). It also runs as part of `go test ./...`.

## What it writes

`build/engine-measurements.json` (set `CICADA_ENGINE_MEASUREMENTS=/path/file.json`
to write elsewhere). Everything except `cpu_ns_per_block` and `elapsed_seconds`
is identical from run to run on the same platform.

```
entries[].name         "voice/<family>" or "fx/<effect>"
entries[].metrics.<m>  status   "measured" or "not_applicable"
                       value    the result, in `unit`
                       gated    true if the test fails when value > limit
                       limit    the limit from testdata/tolerances.json (gated metrics)
                       reason   why a metric is not applicable
                       detail   per-note, per-level, or per-case numbers
                       points   frequency response: expected, measured and error per frequency
```

To compare with an earlier run, for example the base of a pull request, set
`CICADA_ENGINE_MEASUREMENTS_BASELINE` to its report. The test then fails on
each measured metric (`cpu_ns_per_block` aside) whose value changed, and logs
the metrics the earlier report lacks and how many are identical. Values are
compared as the report holds them, rounded to 0.001 in their unit. CI does not
set it; the gated limits in `testdata/tolerances.json` are what CI enforces:

```
# with the base commit checked out
CICADA_ENGINE_MEASUREMENTS=/tmp/base.json go test -count=1 ./kernel/measure -run '^TestEngineMeasurements$'
# with your commit checked out
CICADA_ENGINE_MEASUREMENTS_BASELINE=/tmp/base.json go test -count=1 -v ./kernel/measure -run '^TestEngineMeasurements$' | grep baseline:
```

Every entry carries all five metrics; a metric that does not apply says why.
A value of -200 means zero or below -200 dB (JSON cannot hold minus infinity).
For every gated metric, lower is better.

## Metrics

The full definitions are in the package comment in `doc.go`. In short:

| Metric | Applies to | Meaning |
|---|---|---|
| `aliasing_db` | pitched voices | Power off the expected partials divided by power on them, for held C7 and C8 (guitar C6 and C7, sample C7 only: see each entry's description). |
| `thdn_db` | effects | A 1 kHz sine at -18, -6 and 0 dBFS; power of everything except the fitted fundamental, relative to the fundamental. The worst level is gated. |
| `frequency_response_error_db` | filters, EQ | Largest absolute difference between measured gain and the design curve at the listed points. |
| `noise_floor_dbfs` | all | RMS of 1 s of output with silence in (idle voice: no note). |
| `cpu_ns_per_block` | all | Median of 9 runs of 200 blocks of 128 frames, one instance. Informational: never gated, because it depends on the machine. |

Three things to keep in mind when reading numbers:

- **Aliasing is measured in an "alias stress" setting** where a voice has one:
  filter open, detune, drift, chorus, vibrato and drive off. Otherwise a
  designed effect would count as aliasing. Each entry's `description` lists the
  setting. Real patches sound different.
- **THD+N includes designed modulation** for chorus, reverbs and tape (tape is
  measured with wow and flutter at 0). It is a regression meter, not a claim
  that the effect should be transparent.
- **Not applicable** is recorded, with a reason, for aliasing on drums, modeled
  drums and the DDSP voice (noise or inharmonic by design), and for frequency
  response on anything that is not a filter or EQ.

## Gate and tolerances

`testdata/tolerances.json` holds one limit per gated metric. The test fails
when a measured value is above its limit, when a gated metric has no limit, and
when a limit has no matching measurement (a stale entry).

Limits were set from the measurements with this margin, rounded up to 0.1:

| Metric | Limit |
|---|---|
| `aliasing_db`, `thdn_db` | measured + 3 dB, but never tighter than -110 dB |
| `noise_floor_dbfs` | measured + 3 dB, but never tighter than -120 dBFS |
| `frequency_response_error_db` | measured x 1.25 + 0.05 dB |

The floors keep limits from sitting at float32 rounding noise, which can move a
few dB between compilers and CPU architectures (for example fused multiply-add
on arm64).

### Changing a limit on purpose

1. Make the DSP change. Run `make test-engine-measurements`. Read the failure.
2. If the change is meant (for example you traded aliasing for CPU), regenerate:

   ```
   CICADA_MEASURE_UPDATE=1 go test -p 1 -count=1 ./kernel/measure
   ```

   This rewrites `testdata/tolerances.json` from the current measurements with
   the margins above, then runs the gate.
3. Review `git diff kernel/measure/testdata/tolerances.json`. Each changed limit
   needs a reason in the commit message. A limit that gets looser is a
   regression you are accepting; say so.
4. Adding a voice or effect: add a case in `voices_test.go` or `effects_test.go`,
   then regenerate as above. `TestEveryFamilyHasAnEntry` fails if a directory
   under `kernel/voice` or `kernel/fx` has no entry.

## Negative control

`TestNaiveSawFailsTheAliasingGate` renders a naive sawtooth (`2*phase - 1`, no
band limiting) through the same measurement and checks it against every voice's
aliasing limit. It measures about -9 dB at C7 and C8, worse than every limit
(the loosest is about -20 dB), so a non-band-limited oscillator fails the gate.
`TestBandLimitedSawPassesWhereNaiveFails` is the paired positive control.

## Not covered

- WASM timing and WASM output. The parity gate already proves WASM audio is
  identical to native, so the metrics above hold for both. WASM CPU is measured
  by `make budget-browser` and `make release-cpu-report`.
- The Acid voice's own ladder and diode filters are unexported; their cutoff is
  gated inside that package (`TestDecision0002AcidFilterCutoffGate`).
- Phase response, and filters inside voices (polykeys, strings, organ).
- Polyphonic load: CPU is one voice playing one note.
