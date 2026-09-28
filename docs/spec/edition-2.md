# Cicada source edition 2

Edition 2 keeps the validated language and mixer behavior of edition 1 while requiring the named mixer forms. The complete grammar is in the [EBNF appendix](appendix.ebnf); validation applies the edition-specific rules below. Source edition and semantic JSON format are independent: edition 2 mixer projects are represented in `cicada.project/2`.

## Selecting the edition

**Status:** Implemented.

**Syntax:** A loose score can begin with `cicada 2`. A project selects the edition in `cicada.mod`:

```text
project sketch
cicada 2
```

**Meaning:** A loose score with no header defaults to edition 1. In a project, the manifest selects the edition; if a score also has a source header, it must match the manifest.

**Errors:** Unsupported editions and a source-header/manifest mismatch report `CICADA-VERSION`.

## Named mixer forms

**Status:** Implemented for the routes listed in [edition 1](edition-1.md#named-effects), [buses and master](edition-1.md#buses-and-master), and [track mixer settings](edition-1.md#track-mixer-settings).

**Syntax:** Edition 2 uses named effect declarations, named sends, per-send taps, named inserts, built-in bus declarations, and a separate mute switch:

```cicada
cicada 2
fx room delay {}
fx hall reverb {}
fx glue comp {}
bus music { insert = glue }
master { level = -1dB mute = off }
track bass acid {
  level = -6dB
  mute = off
  send room = 0.25 pre
  send hall = -12dB
  insert = none
  out = music
}
pattern pulse acid { 1 . 1 . }
scene main { bass = pulse }
song { main }
```

The music and SFX buses exist without declarations. `bus music` may select the music compressor; `bus sfx` configures the built-in SFX bus. Track sends name a declared delay or reverb effect. A send's optional `pre` applies only to that send. Insert chains use `a -> b`, but this edition supports at most one insert per route. Track and master `mute` is independent of `level`.

**Removed spellings:** Edition 2 reports `CICADA-VERSION` for the edition-1 shorthand `fx delay {}`, track `send_a`, `send_b`, `send_pre`, `bus = music` or `bus = sfx`, and track `level = off`. Use `fx <name> <kind>`, `send <effect> = <level> [pre]`, `out`, and `mute = on` instead. `level = off` is not emitted by `cicada fix`; it becomes `mute = on`, preserving the track's stored level.

**Unsupported routes:** A syntactically valid topology without engine support reports `CICADA-UNSUPPORTED`:

```cicada-invalid CICADA-UNSUPPORTED
cicada 2
track bass acid { send music = 0.2 }
pattern pulse acid { 1 }
scene main { bass = pulse }
song { main }
```

Unknown effect and bus names report `CICADA-REFERENCE`. Supported effect kinds, send levels, insert placement, and bus controls are specified in edition 1's mixer sections.

## Migrating with `cicada fix`

**Status:** Implemented.

**Syntax:** Run `cicada fix score.cicada`; use `cicada fix score.cicada --check` to check whether a rewrite or manifest update is needed.

**Meaning:** The command rewrites edition-1 mixer aliases while preserving unrelated text and layout, then creates a `cicada.mod` at edition 2 or upgrades an existing edition-1 manifest. `send_a` maps to the declared delay effect and `send_b` to the declared reverb effect. `send_pre = true` moves the pre-fader tap onto each migrated send; `send_pre = false` is removed. `bus = sfx` becomes `out = sfx`; the default `bus = music` is removed. Track `level = off` becomes `mute = on`. Legacy `fx comp {}` becomes a named compressor and is inserted on the music bus unless that insert is already present.

The migration requires valid source before editing and checks typed semantic equality after rewriting. `TestFixNamedMixerMigrationIsTypedAndPCMExact` also renders the covered examples and migration fixture before and after, then compares their PCM24 hashes.

**Errors:** Invalid input, unsupported source editions, ambiguous rewrites, and any semantic change stop the migration. A source header must be standalone for `cicada fix` to remove it safely.
