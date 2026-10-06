# Durable takes

`Store.Begin` journals a take before lane D's `capture.Recorder` accepts PCM.
Pass `Store.Writer(id)` to the recorder. Close/drain the recorder, then call
`Finalize`, `Publish`, and the host's revision-checked source transaction.

The writer batches up to 100 ms of input, then flushes interleaved float32 PCM
before appending and flushing its timing records. Finalization also flushes a
short last batch. At most the unacknowledged batch is omitted after a crash. Gaps remain silence in the raw frame domain; discontinuities
mark the take incomplete. Recovery streams the journal, ignores only a torn final
line, and finalizes acknowledged PCM. Timing records stay on disk, so memory does
not grow with capture duration. RIFF capacity is checked before writing; RF64 is
future work. The audio callback never performs journal I/O.

Finalization verifies WAV dimensions and hashes exact container bytes. Publication
uses immutable `audio/blobs/<sha256>.wav` and friendly
`audio/takes/<track>-<UTC>-<take-id>.wav` paths. Links publish only complete files
and refuse collisions. Filesystems without hard links fail explicitly. PCM and
staged WAV files remain recovery roots; automatic garbage collection is absent.

The score retains every asset and clip declaration. The newest pass becomes the
selected scene binding; selecting an older pass changes only that binding. Clip
preroll trim preserves original PCM. Placement and clock confidence are retained
in the journal; independent occurrence offsets and comp maps belong to later
arrangement work.

Studio exposes `GET /api/takes` and revision-checked `POST /api/takes` with actions
`arm`, `start`, `stop`, `recover`, and `select`. Arm takes `track` and `scene`;
recover/select take `takeId`. Requests include `revision` and use the same JSON
origin checks as existing source edits. Source conflicts return HTTP 409 and keep
published audio. Recovery/reapplication requires the current revision. Prepared
source writes resume on open; completed takes never replay after undo.

An exclusive journal lock prevents concurrent recovery/recorders for the same
score. `host/projectcopy.SaveAs` reads complete record prefixes while recording,
copies assets and acknowledged PCM, copies source history and recovery receipts,
then publishes the unchanged score. The CLI exposes this as
`cicada save-as <score.cicada> <target.cicada>`; desktop Save As calls the CLI.

On Unix, files and parent directories are synced at publication boundaries.
Windows flushes file contents but does not support directory flushing through
Go's directory handles; power-loss directory durability needs Windows filesystem
qualification. Subprocess crash tests exit without cleanup at every durable
journal/publication/source boundary and verify exact recovered PCM and idempotent
reopening. These tests cover process crashes, not physical power cuts.

The Phase 1 base branch can validate and retain audio scores; its engine still
rejects audio playback until the sample-engine lane is integrated. Browser
capture and recording controls use the same publication contract but are separate
host work.
