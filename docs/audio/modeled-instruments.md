# Modeled percussion

Select a struck voice with `track pan model_steelpan {}`. Other profiles are `model_marimba`, `model_vibraphone`, `model_conga`, `model_shaker`, `model_wood`, `model_zinc`, `model_felt`, and `model_marble`. Default octave is 4; `octave = 3` moves unnumbered notes down. Explicit pitches such as `c3` keep their register. Track gain, pan, mute, solo, inserts and room sends use the existing mixer.

```text
cicada 2
track pan model_steelpan { level = -9dB }
pattern riff { c4 . e4 g4 c5^ . g4 . }
scene play { pan = riff }
song { play*4 }
```

Velocity controls contact duration and upper-mode balance. Four deterministic strike positions vary timbre without changing pitch. Four ringing slots keep the preceding notes alive; the quietest slot fades over 64 frames when stolen. Every track reserves four voices under the existing limit of 32. Key release allows natural decay, except vibraphone damping; slides retune the most recent strike while other strikes continue ringing. Steelpan includes a quiet close mode for bowl beating; this is not coupling between independently tuned notes.

Use the ordinary live `OpNoteOn` command for game events. Choose a profile in a prepared track, then use note to set resonant pitch and velocity for impact strength. `examples/modeled/domino-slam.cicada` auditions wood, zinc, felt and marble with the same hit sequence. The profiles describe material character, not measured recordings of a particular game surface.

Run `cicada render examples/modeled/steelpan.cicada -o steelpan.wav --rate 48000 --bits 24 --tail 3s`. Every profile has a score under `examples/modeled/`.

These are analytic physical models. There are no release samples, vibraphone motor tremolo, alternate conga hand articulations, or cross-note sympathetic coupling. Acoustic realism and broadband-attack alias rejection are unverified.
