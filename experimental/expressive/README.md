# Experimental expressive instruments

Experimental monophonic bowed-string, brass and electric-guitar voices, with
continuous expression, deterministic dry auditions and a small trio arrangement.
These are playable physical-model prototypes, not qualified realistic instruments.
Acoustic realism is unverified. Passing DSP tests, tuning
measurements and headless browser execution does not establish convincing timbre.

The guitar is also available from edition-2 scores as the explicitly experimental
built-in `guitar` voice with `experimental = on`. The
[score reference](../../docs/spec/edition-2.md#experimental-guitar-voice) covers
its registry controls and production native, TinyGo AudioWorklet and offline
paths.

## Models and controls

| Model | Excitation and resonator | Continuous controls | Limits |
|---|---|---|---|
| Bow | Nonlinear velocity-dependent bow junction joining two lossy waveguide rails; synthetic body modes; 2x junction rate/FIR decimation | Pitch, bow pressure/position, brightness, damping, 5 Hz vibrato depth | Analytic generic body; friction approximation; Drive ignored; no explicit bow-direction or separate speed control yet |
| Brass | Damped lip mass/spring, implicit Bernoulli flow scattering, reflected bore; synthetic bell tilt; 2x/FIR decimation | Pitch, breath pressure, bell position/brightness, damping/release, vibrato, optional saturation | Generic bore, not a measured trumpet; empirical coupled tuning correction; qualified tuning cases only 110–660 Hz; position changes radiation mix, not physical mouth position |
| Guitar | Fractional traveling-wave loop, triangular/noisy pluck, three-tap frequency-dependent loss, pickup comb | Bend, vibrato, brightness, damping/palm mute, pickup position, drive | One string; NoteOn replucks; use PitchHz changes for legato; Pressure unused; no fret buzz, sympathetic strings, feedback or measured pickup model |
| Amp | Cascaded 4x halfband up/downsampling; two first-order ADAA tanh stages; DC rejection and synthetic speaker lowpass | Smoothed guitar drive | Generic coloration; even zero drive includes low-level saturation/filtering; not a circuit or cabinet emulation |

Construct `NewBow`, `NewBrass`, or `NewGuitar` with the output sample rate, call
`NoteOn(hz, velocity)`, then `Next()` per sample. `SetExpression(Expression{...})`
is a full control snapshot, not a patch. PitchHz <= 0/nonfinite retains pitch;
other invalid values are sanitized. Vibrato is depth in cents; remaining controls
are normalized 0..1. Call `NoteOff()` to release. Each voice has one owner; do not
race rendering and controls. Constructors allocate delay storage; Next and
SetExpression do not allocate. Outputs are floating-point and can exceed unity,
especially Bow: reserve gain/headroom before PCM conversion. The demo uses fixed
authored gains, never clipping, limiting or per-file normalization.

## Reproduce

Go 1.25+ and Node are needed for all checks. From repository root:

```sh
scripts/expressive-check.sh
go run ./cmd/cicada-expressive-demo -out build/expressive-demos
```

The renderer produces four 8-second dry stereo PCM16/48k auditions and one
12-second stereo trio mix. Bow/brass demonstrate sustained and short articulations;
guitar demonstrates clean/drive, bends and palm damping. No reverb is applied.
The renderer rejects nonfinite or clipped PCM output and writes peak/RMS/DC and
render duration metrics. Fixed gains are .35 for bow/guitar and 2 for brass;
clean/drive use the same gain, so their loudness differs intentionally.

The keyboard-driven live WASM audition and build/run instructions are in
[web/README.md](web/README.md). It is a local audition using main-thread
ScriptProcessor, not a production AudioWorklet. No service is deployed.
