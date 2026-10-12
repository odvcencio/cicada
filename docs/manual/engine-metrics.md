# Measure engine performance

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
`allocs_run`/`bytes_run` include setup and event sorting. The offline renderer allocates during setup; these measurements include that cost. The offline DSP loop is checked separately in tests.

To select a subset or increase the observation count:

```sh
make engine-metrics ENGINE_METRICS_ARGS='-blocks 4096 -runs 5 -filter rate_hz=48000,block_frames=128'
make engine-metrics ENGINE_METRICS_ARGS='-filter kind=graph,tracks=16 -offline=false'
```

`-filter` requires every comma-separated substring to occur in the scenario
key. Use `tracks=1 ` with a trailing space to select one track without also
matching 16. Use `-bars` for offline duration and `-warmup` for native warmup.

## Native parallel and idle DSP checks

`BenchmarkNativeFullRender` compares one and four render lanes at 16, 32 and
64 tracks, with 64-, 128- and 256-frame buffers at 48 kHz. Every track has a
sustained acid synth and the same oversampled drive. Mixer, limiter and
per-sample smoothers remain enabled. Construction is outside the timed loop;
render faults fail the benchmark. Run it without competing builds:

```sh
go test ./kernel/engine -run '^$' -bench '^BenchmarkNativeFullRender$' -benchmem
CICADA_PARALLEL_TIMING=1 go test ./kernel/engine -run '^TestParallelCallbackTimingSample$' -count=1 -v
```

The benchmark reports averages and allocations. The optional timing check
reports 600 observed callback samples per case, their p50/p99, maximum,
allocation count and count above the buffer duration. It does not establish
callback tail latency or physical recording latency. Automatic native
parallelism starts at 32 tracks; sample-accurate event boundaries can select
a short serial segment. Browser kernels retain serial rendering and their
existing resource budget. Windows workers request MMCSS Pro Audio; other
hosts remain subject to ordinary scheduler delays.

Idle fast paths skip work only when silence is proven. Drive waits for its FIR
history to clear; active delay/reverb tails still render fully. Empty piano
soundboards skip their expensive resonators while controls advance. Organ
oscillator phases advance during idle intervals. PCM/state regressions cover
automation, silence and retriggering at 44.1, 48 and 96 kHz. No threshold
cuts a quiet musical tail or changes oversampling quality.
