# Durable takes

`Store.Begin` journals a take before `capture.Recorder` accepts PCM.
Pass `Store.Writer(id)` to the recorder. Close/drain the recorder, then call
`Finalize`, `Publish`, and the host's revision-checked source transaction.

The writer batches up to 100 ms of input, then flushes interleaved float32 PCM
before appending and flushing its timing records. Finalization also flushes a
short last batch. At most the unacknowledged batch is omitted after a crash. Gaps remain silence in the raw frame domain; discontinuities
mark the take incomplete. Recovery streams the journal, ignores only a torn final
line, and finalizes acknowledged PCM. Timing records stay on disk, so memory does
not grow with capture duration. RIFF capacity is checked before writing; RF64 is not supported. The audio callback never performs journal I/O.

Finalization verifies WAV dimensions and hashes exact container bytes. Publication
uses immutable `audio/blobs/<sha256>.wav` and friendly
`audio/takes/<track>-<UTC>-<take-id>.wav` paths. Links publish only complete files
and refuse collisions. Filesystems without hard links fail explicitly. PCM and
staged WAV files remain recovery roots; automatic garbage collection is absent.

Legacy scene recording retains every asset and clip declaration in the score.
The newest pass becomes the selected scene binding; selecting an older pass
changes only that binding. Clip
preroll trim preserves original PCM. Placement and clock confidence are retained
in the journal. `BeginTimeline` also pins an arrangement take's punch range,
backing origin and tempo. Session comp selections live in revisioned score
metadata and refer to these retained passes; the host prints only the chosen
mono vocal to a new immutable asset, keeping older prints for Undo.

Studio exposes `GET /api/takes` and revision-checked `POST /api/takes` with actions
`arm`, `record`, `start`, `stop`, `recover`, `select`, `comp`, and `listen`.
Session arm/record requests add `recording: {start, end}` musical positions;
comp/listen requests add the same range and a `takeId`. `listen` prepares a
private song audition without publishing source. Legacy arm takes `track` and `scene`;
recover/select take `takeId`. Requests include `revision` and use the same JSON
origin checks as existing source edits. Source conflicts return HTTP 409 and keep
published audio. Recovery/reapplication requires the current revision. Prepared
source writes resume on open; completed takes never replay after undo.

`GET /api/takes/{id}/waveform?bins=4096` returns bounded mono min/max peaks
for a published arrangement take's covered vocal range. Counts are limited
to 64–8192 bins. The host streams verified raw PCM, excludes count-in/pre-roll
using the retained origin, and caches at most 16 immutable summaries outside
recorder/source locks. Analysis never changes a take or a source transaction.

An exclusive journal lock prevents concurrent recovery/recorders for the same
score. `host/projectcopy.SaveAs` reads complete record prefixes while recording,
copies assets and acknowledged PCM, copies source history and recovery receipts,
then publishes the unchanged score. The CLI exposes this as
`cicada save-as <score.cicada> <target.cicada>`; desktop Save As calls the CLI.

On Unix, files and parent directories are synced at publication boundaries.
Windows flushes file contents but does not support directory flushing through
Go's directory handles; power-loss directory durability needs Windows filesystem
qualification. Process-crash recovery does not establish durability under physical power loss.

Hosts prepare audio assets before engine construction. Native playback and
offline rendering support prepared audio; browser capture and recording controls
use the same journal and publication APIs.
