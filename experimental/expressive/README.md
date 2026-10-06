# Expressive instrument research tranche

Experimental monophonic bowed-string, brass and electric-guitar voices, with
continuous expression, deterministic dry auditions and a small trio arrangement.
Based on main `01569e19e5e6b07d0b52277626b96819fbdd7485`. This additive package does
not change the existing instrument ABI, graph opcodes, score syntax, modal pack,
or frozen-v15 DAW integration. No samples or measured impulse responses are used.

These are playable physical-model prototypes, not qualified realistic instruments.
Acoustic realism is unverified. Passing DSP tests, tuning
measurements and headless browser execution does not establish convincing timbre.

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
go run ./cmd/cicada-expressive-demo -out /tmp/cicada-expressive-demos
```

The renderer produces four 8-second dry stereo PCM16/48k auditions and one
12-second stereo trio mix. Bow/brass demonstrate sustained and short articulations;
guitar demonstrates clean/drive, bends and palm damping. No reverb is applied.
The renderer rejects nonfinite or clipped PCM output and writes peak/RMS/DC and
render duration metrics. Fixed gains are .35 for bow/guitar and 2 for brass;
clean/drive use the same gain, so their loudness differs intentionally.

The keyboard-driven live WASM audition and build/run instructions are in
[web/README.md](web/README.md). It is a local research harness using main-thread
ScriptProcessor, not a production AudioWorklet. No service is deployed.

## Evidence and remaining acceptance

See [evidence](evidence/) for captured native/Node-WASM test output and demo metrics.
Tests cover extreme controls, finite bounded output, release, no callback
allocations, block-size-independent scheduling, measured tuning cases, pressure
re-excitation, brass tonguing/slur, guitar damping, and one amp alias regression.
The amp regression compares a specific folded fifth harmonic against a naive
clipper at a coherent high test frequency; it is not a full-band alias measurement
or proof of transparency. Bow/brass antialias rejection has not been quantified.

Tuning tests are limited stationary cases. Autocorrelation reports can miss
register errors; the brass-specific tests search a broader lag range. Arbitrary
pressure transitions, extreme notes, rapid bends and high polyphony remain R&D.
CPU timings depend on this host and do not certify browser callback deadlines.
Human listening, real audio-device/browser soak, additional sample rates and
articulation calibration are review gates before production use or realism claims.

## Primary research references

Algorithms were independently written in Go using these conceptual references;
no source implementation, sample bank or cabinet IR was imported.

- Julius O. Smith, [Bowed Strings](https://www.dsprelated.com/freebooks/pasp/Bowed_Strings.html), [Brasses](https://www.dsprelated.com/freebooks/pasp/Brasses.html), and [Electric Guitars](https://www.dsprelated.com/freebooks/pasp/Electric_Guitars.html), Physical Audio Signal Processing.
- Cook/Scavone, [STK Brass model description](https://raw.githubusercontent.com/thestk/stk/master/include/Brass.h), numerical/model precedent inspected during research.
- Parker, Zavalishin and Le Bivic, [Reducing the Aliasing of Nonlinear Waveshaping Using Continuous-Time Convolution](https://www.dafx.de/paper-archive/details.php?id=vem_XXF5qBbfiWOH2RVVAA), DAFx 2016; first-order antiderivative formulation.
- Kahles et al., [Oversampling for Nonlinear Waveshaping](https://acris.aalto.fi/ws/portalfiles/portal/34665796/ELEC_Kahles2019_Oversampling_JAES.pdf), JAES 2019; reconstruction/decimation tradeoffs.
