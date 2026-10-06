# Clav

The `clav`, `clav_muted` and `clav_hollow` patches use an original struck-string
model: tangent excitation, 24 stiff-string modes, frequency-dependent damping
yarn, two spatial electromagnetic pickups and key-off contact noise. The
pickup voltage follows modal velocity. The selector offers bridge, neck, both
and difference wiring. Controls and ranges are in the package documentation.

## Recorded reference comparison

A [free-use instrument recording](https://commons.wikimedia.org/wiki/File:Hohner_Clavinet_D6.ogg)
was downloaded for analysis only. The source page permits any use, including
commercial use and modification. No reference audio is shipped or sampled.
The Ogg file's SHA-1 is `575883c568b7ae473449bc93f019660253c6d052`.

The recording contains a played phrase with multiple pickup selections, not
isolated controlled strikes. The table compares power-weighted spectra after
mono 48 kHz conversion, DC removal and Hann windows. Model measurements use a
500 ms C4 strike. Reference measurements use separate 1 s phrase windows.

| Sound / window | Power centroid, Hz | Power above 2 kHz |
| --- | ---: | ---: |
| Reference, 1–2 s | 925.8 | 17.372% |
| Reference, 10–11 s | 915.5 | 18.632% |
| Reference, 30–31 s | 846.3 | 15.796% |
| Reference, 60–61 s | 1172.4 | 7.631% |
| Model, velocity 25 | 946.8 | 7.688% |
| Model, velocity 76 | 1004.6 | 10.019% |
| Model, velocity 127 | 1110.6 | 14.147% |

Model centroid and upper-band power occupy the recorded phrase's broad range.
This does not establish matched-note accuracy: voicing, key velocities,
registration, pickup wiring and recording EQ are unknown. The comparison
exposed an overly dark displacement pickup; differentiating its flux produces
the brighter attack reported here. Modeled T20 is 0.635–0.700 s in 5 ms RMS
windows. The dense reference phrase does not establish an isolated T20 or
release transient; those comparisons remain unvalidated.

## Cost and checks

At 48 kHz, a 128-frame block measured 14.3 µs with one voice and 74.7 µs with
eight voices: 111.9 and 72.9 ns/voice/frame respectively. Loaded-machine native
results vary; browser cost needs separate measurement. All playback events,
pedal changes, reset and rendering allocate zero times.

`go test -tags clav_wasm ./kernel/voice/clav` compares 393,216 native/TinyGo
float32 samples bit-for-bit at 44.1, 48, 96 and 192 kHz with 64, 128 and
256-frame blocks and checks WASM allocation bytes. Native golden tests pin
attack, retrigger and release. The isolated engine is unlinked from the core
WASM module and adds zero bytes to its raw and Brotli sizes.
