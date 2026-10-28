# Modeled drum kit

The kit synthesizes 17 drum articulations inside Cicada from damped membrane, shell, rim and metallic modes with filtered contact and snare-wire excitation. It contains no samples or impulse responses.

```text
cicada 2
kit acoustic {
  bd = model.kick
  sd = model.snare
  ch = model.hat_closed
  oh = model.hat_open
  cy = model.crash
}
track kit acoustic {
  level = -12dB
  bd_tune = 0.9
  sd_position = 0.4
  cy_decay = 1.2
}
pattern beat drums {
  bd: x8... ...x6 x8... ....
  sd: .... x7... .... x7...
  ch: x4.x5. x4.x5. x4.x5. x4.x5.
  oh: .... .... .... ..x5.
  cy: x6... .... .... ....
}
scene groove { kit = beat }
song { groove*4 }
```

Use the checked examples under `examples/modeled-kit/` for full scores. Any drum source lane may select any `model.` articulation; omitted lanes remain silent. A kit in a library binds `model.` pieces the same way, and a score with imports needs no import for them.

| Piece | Model suffixes |
| --- | --- |
| Kick | `kick` |
| Snare | `snare`, `rimshot`, `cross_stick`; low snare velocity gives ghost notes |
| Toms | `tom_low`, `tom_mid`, `tom_high` with a pitch fall after the strike |
| Hi-hat | `hat_closed`, `hat_pedal`, `hat_half_open`, `hat_open` |
| Ride | `ride_bow`, `ride_bell` |
| Other cymbals | `crash`, `splash` |
| Brushes | `brush_tap` on the snare, `brush_sweep` |

Track controls use the source lane prefix, such as `bd_tune` or `sd_position`. Tune multiplies modal frequency (0.5–2, default 1). Decay multiplies tail time (0.25–2, default 1). Position runs from centre to edge (0–1, default 0.35). Humanize adds bounded strength, timbre and pitch variation (0–0.1, default 0.015); it does not move event times. These synthesis controls are fixed before rendering and affect subsequent strikes. Decay is a unitless multiplier, so `250ms` is invalid. Lane level and pan use the existing mixer and may change live; unsupported live synthesis updates are rejected.

Every bound lane reserves one voice, retains one ringing strike and fades its predecessor for 1 ms on retrigger. The two-track full kit binds 11 main pieces and four alternate articulations, using 15 of the 32 available voices; each brush piece takes one more. Score gate ends leave natural tails. A live `OpNoteOff` on a drum lane explicitly chokes it with a 5 ms model fade. Every modeled hat strike chokes the other modeled hats in the same kit track, based on their selected articulation rather than lane name. Hats on separate tracks do not choke each other. Brushes join no choke group, so a tap, a sweep and a modeled hat can sound together. A built-in or instrument hat on the `ch` lane still chokes whatever sounds on the `oh` lane, a sweep included.

`brush_tap` is a soft pat of the bristles on the snare head: a short burst of noise over a muted head tone and a light wire response. `brush_sweep` is a sustained swish whose level swells and falls about twice a second, like a brush circling the head. It holds its level while its gate is held, for at most about 1.6 s, and a live `OpNoteOff` ends it with the 5 ms fade. A score row has no gate end, so a sweep in a score runs to that limit and then fades out within a quarter of a second, unless the song ends first: an offline render ends every drum lane when its last bar ends, so a sweep still sounding then stops with the 5 ms fade. `<lane>_decay` scales both times, so 0.5 gives a sweep that ends after about 0.9 s. The next hit on the lane replaces the sweep with the usual 1 ms fade. When a sweep replaces a sweep, the new one takes over at the level the old one has reached and swells from there, so a chain of sweeps has no gap between them. A sweep is noise, so `<lane>_tune` and `<lane>_position` move its bristle band (higher is brighter) instead of a head resonance. Brushes sit near the hats in level at equal velocity; raise them with `<lane>_level`.

```text
cicada 2
tempo 84
seed 4242
kit brushes {
  bd = model.kick
  sd = model.brush_tap
  cp = model.brush_sweep
  ch = model.hat_pedal
}
track kit brushes {
  level = -12dB
  cp_decay = 0.9
}
pattern ballad drums {
  bd: x4... .... .... x3...
  sd: .... x6... .... x6...
  cp: x6... .... x5... ....
  ch: .... x5... .... x5...
}
scene groove { kit = ballad }
song { groove*4 }
```


The models use analytic resonances and contain no recorded microphone channels, articulations beyond the listed pieces, or acoustic-room response. Tune, decay, position, and humanize are fixed before rendering.
