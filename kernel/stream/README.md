# Streamed PCM pages

`stream.Cache` owns a fixed arena of 4,096-frame stereo float32 pages, with
384-frame guards on each side for the sample SRC kernel. Each page uses 38,912
PCM bytes. A cache can serve multiple immutable assets and audio readers.

Create the cache and register every reader before starting the asset worker or
audio callback. Asset IDs identify immutable contents within that cache. Use a
new ID for a changed asset. Short sampler regions can remain pinned in memory.

```go
cache, err := stream.New(stream.Config{
    Pages: 6, Readers: 1, AheadPages: 3,
}, []stream.Asset{{ID: 1, Frames: 172800000, RateHz: 48000, Channels: 2}})
// Handle err outside rendering, then register the audio owner's reader.
reader, err := cache.NewReader(1)
```

For the default unity-rate 48 kHz clip, three ahead pages cover approximately
250 ms. Configure the horizon in source frames, including the playback pitch ratio. Each reader requests its current page, ahead pages in its playback
direction, and one page behind. Admission reserves that window plus one old
pinned page per reader. The configuration rejects undersized arenas.

An audio owner calls `SeekFrame`, `Render` and `Stop`. Rendering copies unity-rate
PCM in either direction; it never opens storage, acquires a mutex, waits, spins
on ownership, or allocates. `ReadFrame` exposes guarded samples by value to sample-rate converters. It does not change the seek/prefetch intent. The host
must route transport changes to the audio owner rather than call `SeekFrame` from
another goroutine.

The worker calls `Next` and completes each returned `Work` exactly once through
`Publish`, `Cancel`, or `Fail`. Only one worker may own these calls. It fills both
channels, including mono duplication, before publication. The audio owner pins
a ready page with one atomic increment per ready candidate, followed by a
publication check. Competing readers never cause a retry or a false miss. A
worker that claims the slot first keeps any transient reader probes through
publication, cancellation, and failure, so a late release cannot erase a pin
or corrupt the page state. Eviction claims an unpinned page
outside active windows; old pages are recycled in publication order. The worker
cannot write PCM while a reader holds its pin. Call `Stop` on retired readers to
release pins and demand before retiring a plan.

Each seek overwrites a mailbox; seek history never becomes a request queue.
Obsolete fills are cancelled or rejected at publication. Cache telemetry counts
loaded, evicted, cancelled and failed pages. Reader telemetry counts underrun
episodes, missing output frames and recoveries. On a miss, the last output fades
to silence over 2 ms while source position advances. A ready page fades back in
at the current position. End-of-asset fading does not count as an underrun.
Missing pages get at most one lookup per source page in a render block; the next
block retries, so a storage stall cannot multiply arena scans by frame count.
Reader telemetry also counts page lookups for this bound. Recovery can wait one
additional render block after publication.

`host/sampleasset.OpenStream` verifies a WAV hash and dimensions before returning
a file-backed page source. `StartWorker` performs bounded-chunk decoding and
checks cancellation between reads; a second fixed goroutine cancels obsolete
reads. OS storage calls may outlive cancellation. `Cancel` returns immediately;
`Wait` takes a host deadline. Keep sources and the cache alive until `Done`
closes, then close files. No goroutine is created per seek. A stalled source can
delay other reads on that worker, while rendering continues with counted misses.

Offline hosts call `WaitReady` outside rendering, with a deadline, before each
new window. It reports errors only for missing pages in that reader's window.
The worker retains bounded retry/error records by asset and page, with a 25 ms
initial retry delay that doubles to a 1 s cap. Deferred failures cannot monopolize
current-page priority or starve other readers' prefetch. Successful publication
clears only that page's error; retired demand is reclaimed before records are
reused. Retry/error records never exceed the admitted arena's page count.
Native playback and offline WAV/stems now connect this cache through
`host/schedule.Compile` and `host/sampleasset.StreamPlan`. Clip-only assets above
1 MiB are paged; short clips and all sampler assets keep their resident path.
The sampler PCM budget remains 64 MiB. A streaming configuration holds verified
metadata without files or workers; `engine.New` opens its private runtime only
after engine validation succeeds. Each engine admits at most 32 clip readers
and 32 upcoming-start readers. At 48 kHz the stereo page arena is 14.25 MiB,
independent of song length or the combined size of full-length vocal takes.

The sample voice uses the resident path's prepared sinc banks, absolute source
coordinates, clip gains and fades for streamed SRC. Reader misses count output
frames rather than FIR taps, with a fixed per-callback missing-page table.
Missing output fades and recovery last 2 ms at the output rate. Arrangement
placements can overlap; explicit scene clip assignments retrigger their track,
while `keep` carries its current clip into the next scene.

Offline callers use `engine.PrepareRender(ctx, frames)` before every block and
render exactly its returned length. It primes current clips and upcoming
starts, shortening the block if its bounded prefetch table cannot cover all
starts. WAV, stems and placement resampling fail on page errors or any missed
output frames, so an unavailable page cannot become a successful silent export.
Native start and score offers prime outside the audio callback; live playback
continues with measured declicked misses if storage later falls behind.

`Engine.Retire` atomically requests teardown from the audio owner without I/O,
locks or allocation. The existing monitor goroutine cancels reads and closes
that runtime's private, concurrency-safe WAV handles; both worker goroutines
finish before `Engine.Close` returns from host teardown. Live score replacement
retires the old runtime after its crossfade, and discarded/error plans close
without being published. `StreamStats` reports arena size, page operations,
underrun episodes, missing output frames, recoveries and bounded page lookups.

Portable browser/kernel images continue using `schedule.CompileResident` and
its 64 MiB limit. Native streaming factories are rejected by image encoding;
TinyGo compiles only the resident clip/SRC branch. Browser worker-to-worklet page
transfer remains separate work.
