# Compare the modeled and sampled grands

`cicada-piano-abx` renders seven performances through the modeled piano and the
CC0 grand sample pack. It delivers blind A/B/X trials in stereo 48 kHz PCM24.

```sh
GOWORK=off go run ./cmd/cicada-piano-abx \
  -pack ./packs/grand \
  -out ./piano-modeled
```

The pack argument accepts either the grand pack directory or its `manifest.json`.
The command reads local assets; it does not download or embed samples. The output
directory must be new. `-trials 4` is the default and produces 28 trials, each with
three WAV files. `-seed 42` reproduces the mappings and presentation order. Omit
the seed for cryptographic randomization. `-model-source` defaults to
`kernel/voice/piano`; set it to that source directory when running a built binary
from elsewhere. The command hashes the model's Go sources for the organizer record.

Both instruments receive identical, frame-indexed MIDI note, velocity, key-up,
and sustain-pedal events. The performances cover low, middle, and high registers;
four dynamics; repeated notes; articulated chords; and the same chords with the
pedal released or held. Both use eight note slots. The sample reference blends
adjacent recorded dynamics, preserves natural sample decay, defers damping while
the pedal is down, and plays recorded release samples at damping. It applies the
pack's attack and release envelope. Pedal events in this set are fully up or down.

The command matches each performance using the kernel's ITU-R BS.1770-4 integrated
loudness meter. The requested target is −23 LUFS. It lowers the common target when
either reference needs more headroom. It applies constant gain, quantizes to
PCM24, and measures again; generation fails if the references differ by more than
0.1 LU or exceed −1 dBTP. There is no added limiter, EQ, room effect, or dither.
Different performances may require different gains; dynamics within a performance
remain intact.

The output separates listener material from organizer records:

| File | Purpose |
| --- | --- |
| `*-A.wav`, `*-B.wav`, `*-X.wav` | Blind listening references and unknown |
| `trials.json` | Randomized trial order, filenames, and frame counts |
| `LISTEN.txt` | Playback and response instructions |
| `responses.csv` | Blank response sheet to copy for each listener |
| `organizer/answer-key.json` | Reference identities, X answers, randomization seed, and WAV hashes |
| `organizer/manifest.json` | Exact performances, gains, encoded loudness and peaks, render timings, and verified sample provenance |

The manifest pins the modeled piano's Go source hashes, its MIT license, the
available build revision, the sample-pack manifest hash, and every used compressed and
decoded WAV hash. It records CC0 license links and original-source hashes declared
by the pack. The original downloads are not re-fetched: the command verifies the
local compressed assets and decoded WAVs. Asset paths in the records are relative
to the pack.

To run a blind session, distribute the output without `organizer/`. Keep player
normalization and processing off, maintain a fixed listening level, and collect
each listener's A-or-B choice before revealing the key. X is a byte-identical copy
of one reference, so byte comparison or checksum inspection invalidates a trial.

This command prepares listening evidence. It does not collect listener judgments
or establish acoustic equivalence. The roadmap's ten-listener acceptance requires
an actual listening session and analysis of the collected responses.
