# Sample instrument acceptance targets

The following measurements check sampler behavior and runtime limits. Listening comparisons assess the supplied sounds.

The behaviour reference is Kontakt's documented mapping zones, velocity crossfades, cycle round robin, release triggers, sustain loops and voice stealing: <https://docs.native-instruments.com/ni-tech-manuals/kontakt-manual/en/classic-view>. These are behaviour references, not a claim of sound equivalence. No proprietary DAW recordings or samples are distributed.

| Behaviour | Gate | Measurement |
| --- | --- | --- |
| Velocity | Independent recorded layers; continuous interpolation through layer centres; silence at velocity zero | Constant fixtures check every velocity and boundary; listening sweeps use licensed recordings |
| Variation | Each declared round robin plays once per cycle; independent key groups; reset reproducible | Exact sequence and PCM comparisons |
| Release and pedal | Per-note releases; stale handles cannot release replacements; pedal holds until pedal-up; recorded release triggers once | Repeated-pitch, steal, pedal and release fixtures |
| Loops and stealing | Crossfade loop endpoints; new notes ramp from zero; one bounded old-output tail per slot | Discontinuous constant fixtures bound first differences to 0.03 at 48 kHz |
| Envelopes and filter | Per-note attack/decay/sustain/release; finite filter output; exact silence after completion | Timed envelope fixtures, extreme parameter tests |
| Legato and tuning | Retune an owned voice without replaying its attack; cents tuning and seeded timing/velocity/pitch variation | Phase continuity and block-size independent PCM |
| Recording roots | Selected tonal recordings match sounding MIDI keys within 35 cents, periodicity confidence >=0.8 | Licensed-root pitch analysis; an octave mismatch must fail even when PCM fidelity passes |
| Resampling | Alias rejection >=80 dB; residual <=-90 dB; passband ripple <=0.05 dB for qualified ratios | Existing least-squares tone and stopband sweep in kernel/voice/sample/quality_test.go |
| Noise | Inactive engine produces bit-exact zero; source noise remains separately reported | Silence fixture and sample-file tail/DC/peak measurements |
| Runtime | No allocation or asset I/O on callback; bounded layers/voices; native/TinyGo parity; 128-frame timing reported per voice | Allocation tests, standalone WASM fixture, benchmarks |
| Kernel | Production kernel stays below 300 KiB raw; pack audio is external | make budget-size; before/after bytes |
| Assets | Every input and output has URL, SHA-256, licence; PCM admitted before playback; lazy fetch outside callback | Corruption, traversal, licence, size and cache tests |

Room sound is retained where it exists in the licensed stereo recording. Dry and existing Cicada reverb versions are supplied for listening; no unverified impulse response is added. Acoustic instruments do not gain recorded legato transitions or sympathetic resonance merely from pitch continuity. Missing articulations, sparse key coverage and unqualified CPU scenarios must be reported.

The A/B protocol uses the same note events, velocities, seed, gain and room settings. Before uses a single fixed layer and the existing 2 ms sample release; after uses mapped layers, round robins and authored releases. WAVs and scores live outside the source repository. Raw licensed recordings provide the source-envelope/spectral reference; they are not recordings of a commercial DAW. Do not manufacture extra velocity layers or round robins by processing a single recording.

## Reproduce validation

Run `GOWORK=off make test-sampler-wasm` for native/WASM parity, ABI admission
and the optional module size gate. CI uploads this gate's report as an artifact.

After building the pinned external packs, generate source and runtime evidence:

```sh
GOWORK=off go run ./tools/sampler/source_render.go -packs build/pro-packs -out build/source-reference > build/source-reference-selections.txt
python3 tools/sampler/source_metrics.py --packs build/pro-packs --sources sample-source-cache --selections build/source-reference-selections.txt
node tools/sampler/wasm_metrics.mjs
node tools/sampler/browser_gate.mjs
```

Source comparisons need NumPy and FFmpeg. The browser gate needs Playwright and
Chrome selected with `CHROME_BIN`; `PLAYWRIGHT_MODULE` can select its installed
entrypoint. Keep reports under `build/` and publish them as CI artifacts or PR
attachments. Commit reusable tools, acceptance criteria and pack metadata.
