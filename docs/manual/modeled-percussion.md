# Modeled percussion

A `model_` track plays struck bars, drums and plates without samples. The
profiles are `model_steelpan`, `model_marimba`, `model_vibraphone`,
`model_conga`, `model_shaker`, `model_wood`, `model_zinc`, `model_felt` and
`model_marble`. Notes set the pitch and, through velocity, how hard the bar is
struck. Each track reserves four voices, so four struck bars ring at once; a
fifth strike takes over the quietest. A track accepts `octave`, `sustain` and
the usual mixer settings (`level`, `pan`, sends, inserts). The profile names
are reserved for these tracks: an instrument, a kit or a sampler cannot take
one, and the check reports `CICADA-DUPLICATE`. The
[modeled instruments reference](../audio/modeled-instruments.md) describes the
physical models.

```cicada
cicada 2
tempo 108
track bars model_marimba { level = -6dB octave = 4 }
pattern run notes { c4 e4 g4 c5 g4 e4 c4 . }
scene play { bars = run }
song { play*2 }
```

## The vibraphone's damper pedal

A vibraphone has a damper bar under its keys. When a note ends, the damper
falls on the bar and the sound stops in about 70 ms. The other profiles have no
dampers, so their bars always ring out.

Players hold the damper pedal to lift every damper at once. Bars then ring
until the pedal comes up, which is the vibraphone's lush, blended sound. Cicada
works the pedal with `sustain`, the control the piano and the keyboards use:

| Value | Pedal |
| --- | --- |
| `off`, or a number below 0.5 | Up. A note's end damps its bar. |
| `on`, or a number from 0.5 to 1 | Down. A note's end leaves its bar ringing. |

A bar's damper falls when its key is up and the pedal is up. The order of the
two does not matter, not even within one sample. So lifting the pedal damps the
bars whose notes have ended, and leaves a bar whose note is still held ringing
until that note ends. A pedal change that falls on the instant a note ends gives
the same sound whichever of the two a renderer applies first.

Write `sustain` on the track to hold the pedal down for the whole score:

```cicada
cicada 2
tempo 92
track vibes model_vibraphone { level = -9dB sustain = on }
pattern arpeggio notes { gate = 50% step = 1/8 c4 e4 g4 b4 c5 b4 g4 e4 }
arrange { place a vibes arpeggio { at = @1.1.1 length = 2bars } }
```

Here the gate is only half of each step, and every note still rings on after
it. Without `sustain = on` each bar would stop 70 ms after its gate. At most
four bars ring at once, so a held pedal blends the last few notes and not the
whole line.

The setting must be `on`, `off` or a unitless number from 0 to 1. Anything else
is `CICADA-PARAM`:

```cicada-invalid CICADA-PARAM
cicada 2
track vibes model_vibraphone { sustain = 2 }
pattern line notes { c4 }
scene main { vibes = line }
song { main }
```

## Changing the pedal during a piece

The pedal is a control like any other, so a scene or an `automate` lane can
move it. A scene sets it with `track.sustain` and the number 0 or 1. A later
scene that omits the setting keeps the earlier value, so a scene that should
let the sound go must set `vibes.sustain = 0`. A Game Director state is a
scene, and its settings work the same way.

```cicada
cicada 2
tempo 92
track vibes model_vibraphone { level = -9dB }
pattern line notes { gate = 100% step = 1/8 c4 - - - g4 - - - }
scene dry { vibes = line vibes.sustain = 0 }
scene lush { vibes = line vibes.sustain = 1 }
song { dry lush dry }
```

An `automate` lane changes the pedal at exact places, which is how a player
re-pedals on a change of harmony. Positions are `@bar.beat.sixteenth`. Write
`step` on every point after the first, so the pedal jumps at that point. A
point without it ramps from the previous value, and the pedal changes where the
ramp crosses 0.5. Before its first point a lane sets nothing, and after its last
point the last value holds. A lane follows the song's clock, so it repeats when
the song loops but not while a Game Director state loops one scene. Give such a
state its pedal with a scene setting.

Lift the pedal on the beat where the new note starts, and press it again a
sixteenth later. The lift damps the old bar, the new bar's key is still down, so
it rings on, and the pedal returns while it does:

```cicada
cicada 2
tempo 92
track vibes model_vibraphone { level = -9dB }
pattern line notes { gate = 100% step = 1/8 c4 - - - g4 - - - | e4 - - - c4 - - - }
automate vibes.sustain {
  @1.1.1 1
  @1.3.1 0 step
  @1.3.2 1 step
  @2.1.1 0 step
  @2.1.2 1 step
  @2.3.1 0 step
  @2.3.2 1 step
}
arrange { place a vibes line { at = @1.1.1 length = 2bars } }
```

Two limits matter when you re-pedal:

- The new note's key must still be down when the pedal returns. A note that
  ends while the pedal is up is damped with the others, so give the notes that
  you re-pedal over `gate = 100%`, or tie them.
- Points sit on sixteenths, so the pedal is up for at least one. At 92 BPM that
  is 163 ms.

## Live control

`sustain` is a live control, with the registry name `piano.sustain`. A host
sets a modal track's pedal with the existing `OpSetParam` command and
`ParamPianoSustain`, as it does for the piano; there is no new opcode.

To work the pedal from a MIDI controller in Studio, learn the controller for the
track's `sustain` control: select the control, choose **Learn next input**, then
press the pedal (see [Live performance](studio.md#live-performance)). Studio
learns the sustain controller (CC 64) as it learns any other. A controller value
of 64 or more puts the pedal down. The pedal then works the patterns that are
playing. Studio does not play live notes on a `model_` track: **Live** offers
acid tracks, the modeled keyboards and authored instrument tracks only, so you
cannot play a vibraphone from a MIDI keyboard there.

A seek rebuilds the pedal as it rebuilds every scene setting: the authored
`sustain`, then each scene setting and automation point up to the new position.

## Browser and plugin kernels

Native playback and WAV and stem export need nothing more. A kernel image
that uses the pedal marks the modal track's profile byte with its high bit and
adds the track's pedal value after it, four bytes a track. A score without a
pedal writes the same image bytes as before, and every earlier image loads
unchanged.

The browser and plugin kernels (`cicada-kernel.wasm`, `cicada-keys.wasm`,
`cicada-vocals.wasm`) are built with TinyGo and must be rebuilt to play the
pedal. A kernel built before this change refuses an image that uses it, because
it does not accept the marked profile byte. It never plays the score with the
pedal ignored: `gosx_audio_init` returns -1, the WAM2 processor throws "Cannot
initialize the score", and the `cicada` AudioWorklet throws on the same return
value. Rebuild the kernel your host loads with `make build-kernel-wasm`,
`make build-keys-wasm` or `make build-vocals-wasm`.
