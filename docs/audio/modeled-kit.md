# Modeled drum kit

The kit synthesizes 15 drum articulations inside Cicada from damped membrane, shell, rim and metallic modes with filtered contact and snare-wire excitation. It contains no samples or impulse responses.

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

Use the checked examples under `examples/modeled-kit/` for full scores. Any drum source lane may select any `model.` articulation; omitted lanes remain silent.

| Piece | Model suffixes |
| --- | --- |
| Kick | `kick` |
| Snare | `snare`, `rimshot`, `cross_stick`; low snare velocity gives ghost notes |
| Toms | `tom_low`, `tom_mid`, `tom_high` with a pitch fall after the strike |
| Hi-hat | `hat_closed`, `hat_pedal`, `hat_half_open`, `hat_open` |
| Ride | `ride_bow`, `ride_bell` |
| Other cymbals | `crash`, `splash` |

Track controls use the source lane prefix, such as `bd_tune` or `sd_position`. Tune multiplies modal frequency (0.5–2, default 1). Decay multiplies tail time (0.25–2, default 1). Position runs from centre to edge (0–1, default 0.35). Humanize adds bounded strength, timbre and pitch variation (0–0.1, default 0.015); it does not move event times. These synthesis controls are fixed before rendering and affect subsequent strikes. Decay is a unitless multiplier, so `250ms` is invalid. Lane level and pan use the existing mixer and may change live; unsupported live synthesis updates are rejected.

Every bound lane reserves one voice, retains one ringing strike and fades its predecessor for 1 ms on retrigger. The two-track full kit binds 11 main pieces and four alternate articulations, using 15 of the 32 available voices. Score gate ends leave natural tails. A live `OpNoteOff` on a drum lane explicitly chokes it with a 5 ms model fade. Every modeled hat strike chokes the other modeled hats in the same kit track, based on their selected articulation rather than lane name. Hats on separate tracks do not choke each other.


The models use analytic resonances and contain no recorded microphone channels, articulations beyond the listed pieces, or acoustic-room response. Tune, decay, position, and humanize are fixed before rendering.
