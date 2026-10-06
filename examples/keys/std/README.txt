Standard keyboard presets

Each score independently plays one original native keyboard patch through
the std/keys library. It uses the same comping and melodic phrases as the direct
keyboard examples one directory above. The shared manifest intentionally has no
entry or source list, so each score opens independently.

Native hosts prepare modeled voices before rendering. Browser playback loads the
optional keyboard module with capability bit 9 (512). These scores include a pin
for the embedded std/keys library. Presets resolve to the same patch names as
direct tracks; a local declaration with that name overrides the native source.

Validate and render a file, for example:

  cicada validate fm_ep.cicada
  cicada render fm_ep.cicada -o fm_ep.wav

All 21 patches have a corresponding file: tine_ep, tine_bell, tine_bark,
tine_tremolo, reed_ep, reed_tremolo, clav, clav_muted, clav_hollow,
tonewheel_organ, organ_jazz, organ_full, organ_soft, fm_ep, bell_keys, fm_bass,
brass_stab, soft_pad, poly_keys, sync_lead, and string_machine.
