# Engine scaling measurements

Run `make engine-metrics` to print `METRIC MACHINE`, `METRIC CPU`, and
`METRIC OFFLINE` lines. Redirect the output to a file outside the checkout.
The harness fixes `GOMAXPROCS=1`, locks its thread, and uses seed 4242.

The 640-row matrix covers 1, 4, 8, and 16 tracks; acid, a bass-drum kit,
glassbass-style graph synthesis, and stereo samplers at ratios 1 and 1.5;
128- and 256-frame blocks; 44.1 and 48 kHz; drive on/off; both returns
on/off; and constant scenes or a scene launch every bar. Scores and looping
PCM are constructed in memory. The drum kit enables only `builtin.bd`, so
each row uses one configured voice per track within the existing voice limit.
Tracks use a fixed -24 dB level; drive uses soft shape, 9 dB gain, 9 kHz tone,
and 0.7 mix; sends use 0.3 delay and 0.35 reverb. Returns are absent in the
rows without sends. The two scenes alternate patterns without changing tempo.

Main does not integrate samplers into `Engine.Render` or `render.WAV`.
`path=sampler_standalone` measures one looping sample voice per track with
the existing drive, returns, gain/pan, and limiter. It excludes engine
sequencing, transport, meters, and smoothers. Its scene variant retriggers
voices every bar at the same ratio. These rows are DSP comparators; their
offline lines say `status=unsupported`.

Defaults are 1,024 timed blocks after 128 warmup blocks, repeated across
three newly constructed sessions. CPU p50/p99 use nearest-rank percentiles
over all individual block durations across runs. Clock reads, command
submission, and message draining are included. Buffer construction, explicit GC,
sorting durations, and printing are outside the native timed loop. The
GC-percent target is -1 during that loop and restored afterward. The inherited
Go soft memory limit stays active and can still trigger collection. The machine
line reports `native_gc_percent=-1`, the actual inherited `offline_gc_percent`,
and `gomemlimit_bytes`; -1 disables the GC-percent trigger, and the maximum
int64 memory-limit value denotes no configured soft limit. Offline calls keep
both inherited controls. Compare builds with the same GC and memory-limit
settings. CPU `gc_cycles` and offline `gc_cycles_run` report collections
observed across their measurement windows. The harness reports allocations and
bytes there and exits unsuccessfully on any native allocation, fault, or
silent final block. Scene runs must contain more than
one bar of timed frames; `scene_changes` includes warmup and timed playback.
`ns_sample_track` and `ns_sample_voice` divide the median block duration by
frames times configured tracks/voices, rather than by active envelope count.

Offline timing covers a complete `render.WAV` call to `io.Discard`, including
construction, scheduling, event sorting, encoding, latency compensation,
and GC. It uses the same scores, rates, and block sizes, with two bars by
default. Its p99 is across complete calls; with three runs it is the slowest
call, not a callback tail estimate. `ns_block` divides that whole-call time
by the equivalent output block count; it is not a measured block percentile.
`allocs_run`/`bytes_run` include setup and event sorting. Main's full offline
renderer allocates; this harness does not impose a zero-allocation gate on
that existing behavior. The offline DSP loop is checked separately in tests.

To select a subset or increase the observation count:

```sh
make engine-metrics ENGINE_METRICS_ARGS='-blocks 4096 -runs 5 -filter rate_hz=48000,block_frames=128'
make engine-metrics ENGINE_METRICS_ARGS='-filter kind=graph,tracks=16 -offline=false'
```

`-filter` requires every comma-separated substring to occur in the scenario
key. Use `tracks=1 ` with a trailing space to select one track without also
matching 16. Use `-bars` for offline duration and `-warmup` for native warmup.

## Compare two builds

Build the same harness source against each engine revision in separate clean
worktrees, with the same Go version and flags. If a revision predates this
command, copy only `cmd/cicada-engine-metrics` into that disposable worktree.
Build binaries before measuring, so compilation does not affect the runs.

```sh
GOWORK=off go build -o /tmp/engine-before ./cmd/cicada-engine-metrics
# In the comparison worktree:
GOWORK=off go build -o /tmp/engine-after ./cmd/cicada-engine-metrics
/tmp/engine-before -blocks 4096 -runs 5 > /tmp/engine-before.metrics
/tmp/engine-after -blocks 4096 -runs 5 > /tmp/engine-after.metrics
```

Run the binaries sequentially on the same idle machine, preferably under
your local measurement lock and with identical CPU affinity. Alternate
before/after order when repeating to expose thermal or background-load bias.
Keep the machine line, source revisions, command, and affinity with the
results. Match CPU rows by path, kind, tracks, rate, block, drive, sends, and
scene variant; compare p50, p99, and normalized cost. Match offline rows by
the same key and compare full-call time and allocations. Report percent
change as `100 * (after / before - 1)`; compare slopes across track counts
as well as individual rows. Do not treat sampler comparator timings as
engine timings or full-call offline timings as callback latency.

Run `GOWORK=off go test ./cmd/cicada-engine-metrics` for fixture, allocation,
and native/offline PCM checks. After `make build-kernel-wasm`, run
the following for native/WASM/offline parity at the existing 1e-6 tolerance:

```sh
GOWORK=off go test -tags wasm_integration ./cmd/cicada-engine-metrics -run '^TestSyntheticWASMParity$' -timeout 20m
```

These checks do not modify goldens or budgets.
