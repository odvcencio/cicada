# Licensed convolution responses

This fetchable pack contains real room, hall, plate and outdoor responses from
[IR-Library](https://github.com/itsmusician/IR-Library), pinned to revision
`07cbb6f8779a4a448d02355c355f0cef8916b78d`. The upstream library explicitly
[licenses its audio under MIT](https://github.com/itsmusician/IR-Library/blob/07cbb6f8779a4a448d02355c355f0cef8916b78d/README.md).
The repository tracks metadata and fetch code; it does not embed audio in the
WASM kernel or distribute the source audio itself.

Run `python3 assets/ir/fetch.py <destination>` to fetch exact, checksummed WAVs,
the manifest, and the upstream license as `COPYING`. Keep `COPYING` and
`manifest.json` with any redistributed copy of the audio or rendered examples.
The source license notice contains the copyright holder required by MIT.

| ID | Capture | Original duration | Original rate | Download bytes |
| --- | --- | ---: | ---: | ---: |
| room | Bedroom, medium treatment | 0.500 s | 48 kHz | 144,038 |
| hall | Ballroom | 4.527 s | 48 kHz | 1,303,724 |
| plate | Physical plate, 1-second sweep excitation | 2.834 s | 192 kHz | 3,270,728 |
| outdoor | Outdoor gazebo, impact excitation | 1.725 s | 48 kHz | 497,818 |

These are creative captures, not calibrated acoustic reference measurements.
The outdoor response represents a gazebo; it does not claim to represent a
Caribbean verandah. The upstream plate directory identifies sweep excitation.
Source files are unchanged. The upstream filenames identify the plate sweep
and gazebo impact, but do not document a calibration procedure for the other
selected captures. Preparing the plate at 48 kHz applies a bandlimited
sinc resampler and compensates its coefficient gain for the sample-rate ratio.

Use `host/irasset.ReadManifest`, `Fetch` and `Decode` before rendering. The
browser module `host/irasset/loader.mjs` fetches one selected response lazily,
verifies SHA256 and WAV dimensions, then returns owned PCM for transfer to an
audio worker. No fetch or decoding belongs in an AudioWorklet callback.

The original plate has a much higher coefficient energy and a DC bias than
the room responses. `Impulse.Condition(20, 1)` prepares a 20 Hz highpass and
attenuates either channel whose L2 norm exceeds one, with one shared stereo
gain. Use an explicit wet mix after conditioning and measure the rendered
peak. The energy ceiling controls response level; it does not guarantee that
arbitrary input cannot clip. The source checksum still describes the exact,
unchanged WAV; conditioning is an optional runtime transformation.
The browser loader's `conditionImpulse` provides the same optional preparation.

`kernel/fx/convolution.New` accepts a prepared mono or stereo response and a
power-of-two partition size from 64 to 2048 frames. It returns wet stereo audio
and reports a latency equal to the partition size. Align a parallel dry path by
that latency. Each stereo response channel filters the matching input channel;
this is diagonal stereo convolution, not a four-response true-stereo matrix.
Reset clears the tail. Source memory and transformed partitions are bounded,
but long responses still need CPU and memory qualification on the target
device. At head partitions up to 512 frames, long responses use a 2048-frame
tail. Its bit reversal, FFT butterflies, spectral sums and inverse FFT run
in fixed-budget jobs every 128 samples; the tail's internal delay aligns with
the shorter head latency. A 2048-frame head uses uniform convolution.
Average CPU alone does not establish that every browser callback meets its
deadline. `tools/audio/convolution-cpu.mjs` reports mean, p99 and maximum time
from the companion WASM module with the actual fetched responses.
