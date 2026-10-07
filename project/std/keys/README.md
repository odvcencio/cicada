`std/keys` exposes the original modeled keyboard patches as native voice presets.
Each export selects its matching `builtin` patch and retains the engine's original
defaults. Track parameters override those defaults through the same parameter
validation as direct keyboard tracks.

```cicada
cicada 2
import "std/keys"

track part keys.tine_ep {
  voices = 4
  pickup_distance = 0.7
  release = 100ms
  level = -12dB
}
pattern comp notes { [c4 e4 g4 b4] . [f4 a4 c5 e5] . }
scene main { part = comp }
song { main*2 }
```

Exports:

- Tine EP: `tine_ep`, `tine_bell`, `tine_bark`, `tine_tremolo`.
- Reed EP: `reed_ep`, `reed_tremolo`.
- Clav: `clav`, `clav_muted`, `clav_hollow`.
- Tonewheel organ: `tonewheel_organ`, `organ_jazz`, `organ_full`, `organ_soft`.
- FM keys: `fm_ep`, `bell_keys`, `fm_bass`.
- Analog poly keys: `brass_stab`, `soft_pad`, `poly_keys`, `sync_lead`.
- Ensemble strings: `string_machine`.

Every patch accepts `voices` (integer 1–8) and `sustain` (0–1 or `off`/`on`).
Instrument-specific controls are documented in the keyboard manual. The
examples under `examples/keys/std/` cover every export with comping and melodic
phrases.
The native host prepares these modeled voices before rendering. Browser scores
require the separately loaded keyboard WASM module, capability bit 9 (512).

Run `cicada lib update` to pin the exact embedded manifest and source bytes.
Existing pins reject changed library bytes. The supplied examples include the
matching pin.

Presets resolve to the same patch names as direct tracks. A local instrument,
kit, or sampler declaration with that name takes precedence consistently.
Keep those names available when you want the original native keyboard voice.
