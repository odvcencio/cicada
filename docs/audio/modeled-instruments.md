# Modeled percussion candidates

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

Run `cicada render examples/modeled/steelpan.cicada -o steelpan.wav --rate 48000 --bits 24 --tail 3s`. Every profile has a score under `examples/modeled/`. The engineering targets and listening gate are in [pro-grade.md](pro-grade.md).

The model is still a candidate for owner listening acceptance. Pole tuning and damping tests validate synthesis behaviour; they do not establish acoustic equivalence. Retained-mode high-rate comparisons exclude the broadband attack, which remains unqualified for full alias rejection. There are no release samples, vibraphone motor tremolo, alternate conga hand articulations, or cross-note sympathetic coupling.

The optional CC0 marimba reference is pinned in `assets/reference/marimba.json`. The audio remains outside the repository. `tools/audio/modal-reference.py` compares its spectrum and onset-aligned, separately normalized RMS envelope against a rendered model WAV. It requires NumPy. The upstream filename uses a different octave convention: the supplied note corresponds to MIDI 72, scientific C5. Reference and model differ in decay and upper-mode spacing; the measurements are evidence of those differences, not a perceptual pass.
