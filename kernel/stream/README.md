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
250 ms. Configure the horizon in source frames, including pitch ratio for future
SRC callers. Each reader requests its current page, ahead pages in its playback
direction, and one page behind. Admission reserves that window plus one old
pinned page per reader. The configuration rejects undersized arenas.

An audio owner calls `SeekFrame`, `Render` and `Stop`. Rendering copies unity-rate
PCM in either direction; it never opens storage, acquires a mutex, waits, spins
on ownership, or allocates. `ReadFrame` exposes guarded samples by value for
future SRC integration. It does not change the seek/prefetch intent. The host
must route transport changes to the audio owner rather than call `SeekFrame` from
another goroutine.

The worker calls `Next` and completes each returned `Work` exactly once through
`Publish`, `Cancel`, or `Fail`. Only one worker may own these calls. It fills both
channels, including mono duplication, before publication. The audio owner pins
a ready page with one atomic attempt per slot. Eviction claims an unpinned page
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
This package provides lane A's
streamed reader; arrangement scheduling and browser worker page transfer are
separate integration work. Changing the core worklet or legacy engine is not
required to use the native page source.

Checks: `go test -race ./kernel/stream ./host/sampleasset` covers one-hour
playback, page pinning, reverse guards, seek storms, slow storage and cancellation.
`make test-kernel-wasm` includes exact native/TinyGo streaming PCM parity at
64-, 128- and 256-frame block sizes. Allocation tests cover seek, ready playback,
misses, recovery and rendering while storage remains stalled.
