# Native capture contract

`audiobackend.Config.Capture` receives a `Block` and borrowed planar input before
rendering or monitoring. Tymbal timing is preserved, including device clock
positions, actual rate/period, latency, epoch and discontinuities. A device clock
observation is marked separately from a first-frame timestamp.

Prepare a mono/stereo `Recorder` and its bounded ring before arming. The studio
transport's `armCapture` starts duplex while the score is stopped. Pause and Stop
keep an armed device running; Disarm releases a stopped device. Playback resumes
on the same active device. Stop gives the next score a new engine epoch.

`startCapture` prepares one bar of count-in from the active engine tempo using
`seq.Clock`. The callback splits the final count-in period at the exact frame and
retains all raw preroll. `NewCountIn` admits zero to eight bars. A recorder owns
one take; create another recorder for another retained pass.

`Writer` runs on a goroutine. Save raw PCM and every `RecordedBlock` before
returning; its slices expire on return. Save `Placement` into the published take
source: `rawEngineFrame`, `mappedEngineFrame`, `correctionFrames`, `engineFrame`,
`timingConfidence` and `calibrated`. This keeps placement edits visible without
rewriting or discarding the original audio. Durable journaling, WAV writing and
asset/source publication are handled by the take journal.

`Place` maps first-frame timestamps in a shared clock domain. It uses reported
latency only for an estimated mapping when those timestamps are unavailable, or
to estimate output lead from a clock observation. It then applies only the
remaining calibration offset. `Calibrate` measures that residual from a known
loopback event using the same mapping. Calibration belongs to a device epoch;
reopening invalidates it. Missing timing stays uncalibrated.

Ring overflow never blocks the callback. The next accepted block carries the
known gap; `Snapshot.Ring` preserves trailing losses even without a next block.
Writer errors, device discontinuities and queue losses mark the take incomplete.
Close rejects new capture and drains accepted blocks. Only the writer/control
side may allocate or wait.

Qualification here uses simulated mono/stereo duplex. Separate device drift,
browser capture, crash recovery and hardware loopback/soak qualification require
their own work.
