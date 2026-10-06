# Transcribe a melody

Convert a single sung, hummed or solo-instrument WAV recording into an editable
Cicada score:

```sh
cicada transcribe melody.wav --tempo 120 --key "c major" -o melody.cicada
cicada check melody.cicada
cicada render melody.cicada -o melody-preview.wav --tail 0.1
```

Omit `--tempo` and `--key` to estimate them. Estimates include confidence and
warnings; a short melody often fits several keys or half/double tempo. Use
`--report` to write the measured notes, pitch movement, musical estimates and
mean quantization movement as JSON to stderr. Without `-o`, the score goes to
stdout. Existing output files are preserved.

In Studio's **Takes** panel, choose **Recording to score**, select a completed
take or a WAV file, and choose **Transcribe melody**. Review the generated score
before choosing **Replace score**. Replacement checks the preview's source
revision and creates an undo entry; an intervening score edit prevents
replacement. Previewing leaves the score file unchanged. **Discard preview**
removes the preview. Native recordings use the existing local take journal.

The browser recording controls also offer **Recording to score**: choose
**Record melody**, sing, hum or play one melody, then stop. Microphone monitoring
is off. Audio capture starts only after your button press. Temporary microphone
audio is deleted after analysis or cancellation; imported WAVs stay on your
device. The preview can play the original recording and detected notes, display
confidence, download a score, or replace it through the same revision check.

Analysis runs locally outside the audio callback and requires no model or
network service. Studio accepts transcription requests only over a loopback
connection. Audio and pitch data are never sent to an external service.

The tracker covers A1–C7, distinguishes voiced sound from noise, splits repeated
notes at attacks and legato notes at stable pitch changes, and preserves measured
detuning in `bend:` rows. Scale degrees follow the chosen major/minor key;
chromatic accidentals retain notes outside the scale. The generated sine voice
provides a simple pitch audition. Every generated score is parsed, formatted and
compiled before it is returned.

Choose `--grid 1/4`, `1/8` or `1/16` to snap starts and ends. Quantization movement
measures the difference from the detected timing; it is separate from pitch
tracker accuracy. Notes that collapse on the selected grid produce an error,
so they cannot silently disappear. The current score format uses 4/4 rows even
when the accent estimate suggests 3/4. Pitch movement is sampled at the score's
sixteenth-note rate, so fast vibrato and fine glides may need editing. Sustained
notes crossing a four-bar pattern boundary are retriggered.

Use a clear recording with one melodic source. Chords, simultaneous singers,
strong background noise, clipping and ambiguous attacks can cause errors.
Confidence helps identify notes to review; it does not guarantee accuracy.
PCM16/24/32 and float32 mono/stereo WAVs at 8–96 kHz are supported. CLI input is
bounded to 64 MiB, five minutes and 8,388,608 source frames. Scores contain at
most 64 bars. Studio microphone and completed-take transcription is bounded to
60 seconds; the browser file limit is 32 MiB and the workstation upload limit
is 1.9 MiB. Completed takes do not require an upload.
