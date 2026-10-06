# Modeled grand piano

Use `piano` as a track source to play the modeled grand without loading samples:

```cicada
cicada 2
track grand piano {
  level = -6db
  sustain = 0
}
pattern motif notes steps=4 { c4 e4 g4 c5 }
scene main { grand = motif }
song { main }
```

Explicit instruments, kits, or samplers named `piano` override the built-in.
Piano note patterns also accept main’s bounded chord syntax, such as `[c4 e4 g4]`.

`sustain` ranges from 0 (dampers lowered) to 1 (pedal down), with continuous half
pedaling between them. Scenes can set `grand.sustain`. Live hosts use the existing
24-byte `OpSetParam` command with `ParamPianoSustain`; there is no new opcode.
Live `OpNoteOff.Index` carries a MIDI key for piano tracks, or `0xffff` to release
all keys. `OpStop` clears ringing state. Existing voice kinds keep their command
semantics.

The piano covers MIDI 21–108 at 44.1, 48 and 96 kHz. Eight simultaneous struck
keys share a soundboard and 88 sympathetic string modes. Repeating a key keeps
its existing string motion and strikes it again. When all slots are occupied,
the oldest released key is stolen first; a short output tail limits the click.
The engine reserves eight voices per piano track against `MaxVoices`.

Each struck key has up to 48 modal oscillators, divided among one bass string,
two lower-middle strings, or three upper strings. Small unison detuning produces
beating. The bending-stiffness dispersion law is
`f(n) = n f(1) sqrt((1+B n²)/(1+B))`; it preserves the fundamental and stretches
upper partials. Frequency-dependent losses make the upper partials decay sooner.
Modes above 0.42 of the output sample rate are omitted.

A one-sided cubic felt spring acts between the hammer mass and the summed
string displacement. Its reaction slows and rebounds the hammer. The collision
runs with an implicit cubic contact solve and four substeps while contact is
active; free strings use an exact
damped rotation at the output rate. Velocity changes hammer speed and therefore
contact force and spectral excitation. Hammer position couples to the string
mode shapes, rather than injecting a prerecorded attack.

The summed bridge motion excites unstruck strings when their dampers are lifted.
Held keys and the undamped top register can resonate with the pedal up. Twelve
broad plate modes and broadband radiation represent the soundboard. Its shared
state keeps ringing through note releases. Coefficients are prepared for all
keys at construction; note events and render calls allocate nothing. Float32
products have explicit rounding boundaries for native/TinyGo parity.

This is a reduced physical model with analytic design curves. It does not fit
the reference grand's measured string geometry or soundboard, include every
sympathetic overtone, model longitudinal string motion, or return soundboard
forces into the struck strings. Those approximations affect acoustic realism.
The [DAFx piano-model paper](https://www.dafx.de/paper-archive/2004/P_089.PDF)
describes how longitudinal motion contributes to low-register piano timbre.
No source code, sample, or measured impulse response from that paper is included.
