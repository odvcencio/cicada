# Diagnostic codes

<!-- Generated from internal/diagnostics/catalog.go. Do not edit; run
CICADA_UPDATE_DIAGNOSTICS_DOC=1 go test ./internal/diagnostics -run TestDiagnosticsReferenceIsCurrent -->

Every code cicada can report, with its severity, where it is reported, what
it means and how to fix it. `cicada diagnostics` prints the same list, and
`cicada diagnostics --json` writes it as JSON ([agent formats](agents.md#diagnostics---json)).
A test fails when the code emits a code this list lacks.

Where a code is reported:

- **score**: validating a score. `cicada check` prints it with its source line,
  `cicada check --json` gives its position as data, and `validate`, `render` and
  the language server report it too.
- **command**: the error of a command, such as `lib`, `bundle`, `convert`,
  `director` or the export settings of `render`.
- **studio**: responses of Studio, the browser host or the public demo.
- **edit**: refusals of edit intents, from `cicada apply` and Studio.

| Code | Severity | Where | Meaning | Fix |
| --- | --- | --- | --- | --- |
| `CICADA-ARRANGEMENT` | error | score | an arrange block is invalid: it is used together with a song block or in an edition-1 score; it has no placements; a placement or marker has an unknown field, lacks at (or a placement its length), has a malformed or out-of-range time, or a time whose source and typed values differ; a placement's ID is invalid, its content has no track slot, its pattern does not suit its track, or a step pattern placement starts between steps | use arrange in an edition-2 score without a song block, give it at least one placement, give each placement at and length and each marker at as musical times inside the song (such as @5.1.1 and 2bars), use only those fields, place patterns on tracks of their kind, and start step patterns on a step (check --json offers a moveclip fix for that one) |
| `CICADA-ASSET-FORMAT` | error | score | an audio asset's declared format, frames, rate or channels are invalid or differ from the WAV file | declare format = wav and the file's real frames, rate (8000 to 384000 Hz) and channels (1 or 2); the message gives the expected and actual values |
| `CICADA-ASSET-HASH` | error | score | an audio asset's sha256 is malformed or does not match the file's bytes | write the file's SHA-256 as 64 lowercase hex digits in quotes, or restore the file the score pinned |
| `CICADA-ASSET-MISSING` | error | score | an audio asset's file, or the project or library folder it lives in, cannot be read | put the file at the declared path, relative to the folder of cicada.mod (or of the score when there is none) |
| `CICADA-ASSET-PATH` | error | score | an audio asset's path leaves the project folder, or a library asset is not under audio/ | use a project-relative path without .. that stays inside the project; library assets live under audio/ |
| `CICADA-AUDIO` | error | studio | browser audio is not ready for the request: transport stopped, worklet send failed, or a reset is pending | start browser audio and the transport, or wait for the fresh worklet kernel image after Stop, then retry |
| `CICADA-CAPABILITY` | error | studio | the public demo session does not offer this command | use the command in a local cicada studio session |
| `CICADA-CHAIN` | error | score | a track's chain names no patterns or more than 32 | give the chain 1 to 32 pattern names |
| `CICADA-CLIP-RANGE` | error | score | an audio clip's region, gain or fades are invalid, or a clip is bound to a track that is not an audio track | use start < end within the asset in frames, s or ms that resolve to whole frames, gain -60 to +24 dB, fades no longer than the region, and bind clips to audio tracks |
| `CICADA-DEMO` | error | studio | the public demo host refused its build or request (missing source commit, unsupported host, bad asset or entry document) | build the demo from a full source commit and serve it from a supported host |
| `CICADA-DIRECTOR` | error | score, command | the live block cannot become a Game Director manifest: an invalid state or stinger name, or a stinger pattern without a track slot | use valid identifiers for states and stingers and bind each stinger pattern to a track slot |
| `CICADA-DUPLICATE` | error | score | a name is declared twice in one namespace, or uses a reserved name; the message names the first declaration | rename or remove one of the declarations |
| `CICADA-EXPANSION` | error | score | a pattern expands beyond 64 steps after its phrase uses | use fewer or shorter phrase uses, or split the pattern |
| `CICADA-EXPERIMENTAL` | error | score | an experimental voice (guitar) is used without opting in | add experimental = on to the track |
| `CICADA-GRID` | error | score | a step grid attribute or tuplet group is invalid: tuplets need 2 to 8 pitches that divide a cell into whole ticks of at least 30 | use a supported step division and tuplet sizes |
| `CICADA-HISTORY` | error | studio | the public demo session has no change to undo or redo | make a change first |
| `CICADA-IO` | error | score, command | a file cannot be read, or has an extension the command does not accept | check the path and pass a .cicada score (or .json where the command says so) |
| `CICADA-KEY` | error | score | the key root or scale is unknown | use a root such as a, c# or eb and a scale: minor, major, dorian, phrygian, harmonic, mixo, pent or blues |
| `CICADA-LIB-ASSET` | error | command | convert cannot write library-owned audio into semantic JSON | keep the score's imports, or vendor the library, until asset vendoring is supported |
| `CICADA-LIB-CAPABILITY` | error | score | an imported library requires an engine edition or capability this cicada does not support | update cicada or use a library version that fits it |
| `CICADA-LIB-CYCLE` | error | score | libraries import each other in a cycle | remove one import of the cycle the message names |
| `CICADA-LIB-DECL` | error | score | a library declares something only a score may declare, or a qualified name | keep tracks, scenes, songs and other score-only declarations in the score; library names are unqualified |
| `CICADA-LIB-HASH` | error | score, command | an imported library is not pinned in cicada.sum, or its content differs from the pin | run cicada lib update <path> after an intended change; vendoring refuses a library that changed while it copied |
| `CICADA-LIB-IMPORT` | error | score | a score with imports was parsed without the project source loader | check the score as a file (cicada check score.cicada) so imports resolve |
| `CICADA-LIB-MISSING` | error | score | an imported library is not found in std, the project lib/ folder or the user library | run cicada lib list to see what resolves; create, vendor or fix the import path |
| `CICADA-LIB-PATH` | error | score, command | a library path is invalid, or names std where a user library is required | use a lowercase path such as demo/tone; std is reserved |
| `CICADA-LIB-PRIVATE` | error | score | a score references a library declaration that is private | reference a public declaration of the library |
| `CICADA-LIB-REFERENCE` | error | score | a library reference does not name a declaration, uses a namespace no import binds, or a library parameter path lacks a direct import | import the library and reference namespace.name of a public declaration |
| `CICADA-LIB-ROOT` | error | command | scores in different folders keep separate library pins | run cicada lib update from each score's folder, or add a shared cicada.mod |
| `CICADA-LIB-SHADOW` | error | score | an import namespace collides with a declaration, another import or the reserved builtin namespace, or a library exists in more than one search location | rename the import or the declaration, or remove the duplicate library |
| `CICADA-LIB-SOURCE` | error | score | a library's source file cannot be read | restore the library's files or run cicada lib update |
| `CICADA-LIB-SUM` | error | score | cicada.sum is malformed | let cicada lib update rewrite it; each line is path std\|project\|user sha256:<64 hex digits> |
| `CICADA-LIMIT` | error | score, studio | a size or count limit is exceeded: name length, steps, tracks, voices, graph nodes, chain stages, image size, held inputs or source size | reduce what the message counts; cicada check --profile browser lists the portable limits |
| `CICADA-LIVE-ATTACK` | error | score | a live layers attack other than 1bar is given | use attack = 1bar |
| `CICADA-LIVE-BLOCK` | error | score | the live block is repeated, comes before a track, repeats a setting, or leaves an array implicit | write one live block after all tracks with each setting once |
| `CICADA-LIVE-COUNT-IN` | error | score | the live count_in is not 0 to 8 whole bars | use count_in = 0bars to 8bars |
| `CICADA-LIVE-LAND` | error | score | the live land value is unknown | use land = now, beat, bar, 2bars, 4bars or phrase |
| `CICADA-LIVE-LIMIT` | error | score | the live block has more than 16 macros, or layers use more than 3 distinct thresholds | remove macros or merge layer thresholds |
| `CICADA-LIVE-MACRO` | error | score | a live macro is duplicated, badly named, out of range, or maps to an invalid target | give each macro a unique 1 to 64 byte name, a unitless value from 0 to 1 and valid targets |
| `CICADA-LIVE-PHRASE` | error | score | the live phrase is not 1 to 64 bars | use phrase = 1bars to 64bars |
| `CICADA-LIVE-RECORD` | error | score | the live record division does not resolve to 30 to 3840 ticks | use a musical division such as 1/16 or 1bar |
| `CICADA-LIVE-RELEASE` | error | score | the live layers release is not 1 to 16 bars | use release = 1bars to 16bars |
| `CICADA-LIVE-TRACK` | error | score | a live layer rule names an unknown track or repeats one | name each declared track at most once |
| `CICADA-LOCKED` | error | edit | a phrase repair would need to change a locked step | unlock the step the message names, or edit the phrase directly |
| `CICADA-MANIFEST` | error | score | cicada.mod has an unknown, repeated or malformed directive | keep one project name and one supported cicada edition, with each directive once |
| `CICADA-MIDI` | error | score | the midi block is invalid: a port is unknown or repeated, a channel is outside 1 to 16, a note or controller outside 0 to 127, or a mapping target is invalid | declare each port once and map inputs to existing tracks, scenes and parameters |
| `CICADA-NOTE` | error | studio | a live note was refused: invalid input identity, note or velocity, or a voice without browser live notes | send a MIDI note 0 to 127 with velocity 1 to 127 to a track whose voice plays live |
| `CICADA-NOTES` | error | edit | an editnotes batch was refused because of one note; the reason and the note (at, step, pitch or lane) come as data | read reason and note in the error, then change the batch; docs/manual/editors.md describes each reason |
| `CICADA-PARAM` | error | score, command, studio | a parameter, attribute or setting is unknown, out of range, or not allowed here | check the name, range and unit with cicada params and cicada explain; the message names the value |
| `CICADA-PERFORMANCE` | error | score | a performance pattern is invalid: bad note fields, overlapping gates of one pitch, gesture times outside a note, or ticks outside a performance | give each note a MIDI pitch, velocity 1 to 127, start and length in ticks inside the loop, and gesture points inside its held length |
| `CICADA-POSITION` | error | score | a position is invalid or out of order: arrangement tempo steps, automation points or contour points | write positions as @bar.beat.step in increasing order inside the song; contours run from @0 to @1 |
| `CICADA-PRESET-PARAM` | error | score | a preset sets a parameter its target instrument does not have, or a structural one | set only the target's parameters (cicada explain lists them) |
| `CICADA-PRESET-RANGE` | error | score | a preset value is outside its parameter's range | use a value inside the range the message gives |
| `CICADA-PRESET-TARGET` | error | score | a preset's instrument target is missing or incompatible | set instrument = an existing instrument or built-in voice |
| `CICADA-PRESET-TYPE` | error | score | a preset value has the wrong type: not one of the allowed values, not on or off, not a finite number or not an integer | use a value of the type the message expects |
| `CICADA-PRESET-UNIT` | error | score | a preset value has the wrong unit | use the unit the message names |
| `CICADA-RATE` | error, warning | score | a modulator's rate is not what it plays: the warning when loop = continue fits whole cycles to the song at another rate, the error when a routed modulator exceeds 20 Hz at the song's fastest tempo | choose a rate that fits the song's length, or a slower one; the message gives the rate that plays |
| `CICADA-REFERENCE` | error | score | a name refers to nothing: an unknown track, pattern, scene, instrument, effect, bus, clip, asset or profile; close names get a spelling suggestion | declare it, or use one of the names the message suggests |
| `CICADA-REVISION` | error | studio | the public demo session changed since the client read it | refresh, then edit again |
| `CICADA-ROUTE` | error | score | a modulation route reaches a target it cannot modulate: a legacy effect setting, a non-track row, a non-continuous row, or an envelope's own input | route to a continuous live track mixer or voice parameter |
| `CICADA-ROW` | error | score | a velocity row is invalid: wrong length, a value outside 1 to 127, or a holding dot with no earlier value | give the row one entry per expanded step, values 1 to 127, and a value before the first dot |
| `CICADA-SAMPLER-MEMORY` | error | score, command | the sampler packs decode to more PCM than the resident cap allows | use smaller packs, or give a larger cap with --max-resident-pcm or CICADA_MAX_RESIDENT_PCM, such as 2GiB |
| `CICADA-SAMPLER-PACK` | error | score | a sampler pack is declared wrongly or does not load at 44.1, 48 or 96 kHz | declare a relative JSON pack with its SHA-256 pin and no asset or loop mode, and fix the pack the message names |
| `CICADA-SAMPLER-PARAM` | error | score | a sampler's fields are invalid: root, mode, voices, envelope or the field set | use an absolute root c0 to b6, mode oneshot or loop, voices 1 to 32 and valid envelope times |
| `CICADA-SAMPLER-ZONE` | error, warning | score | a note falls outside every zone of its sampler pack, or a hit velocity no zone covers (errors); a warning when a hit's velocity is outside the recorded range and plays at the nearest recorded one | move the note into the pack's key range, write it in another octave, change the pattern's transpose, or use a pack that covers it; for the warning, write a velocity the pack records |
| `CICADA-SCALE-DEGREE` | error | score | a scale degree does not exist in the score's scale (2 and 6 in pent and blues) | choose a degree in the scale or use an absolute letter pitch |
| `CICADA-SEED` | error | score | a seed is outside 0 to 4294967295 | use a seed from 0 to 4294967295 |
| `CICADA-SLIDE-REST` | warning | score | a slide leads into a rest, so it has no effect | slide into a note, or remove the slide |
| `CICADA-SOLO` | warning | score | a saved solo switch is on, so render and check play only the soloed parts | turn solo off before delivering, unless you mean it |
| `CICADA-SOURCE-MISSING` | error | score | a score that cicada.mod lists cannot be read | restore the file or remove it from cicada.mod |
| `CICADA-SOURCE-PATH` | error | score | a source path is absolute, uses traversal or patterns, is not listed in cicada.mod, or leaves the project | list project-relative .cicada paths in cicada.mod |
| `CICADA-SYNTAX` | error | score | the text does not parse: a token or declaration is malformed | check braces, spelling and separators near the position the message gives |
| `CICADA-UNIT` | error | score | a value has the wrong unit or type for its field | use the unit cicada explain gives for the field, such as Hz, ms, dB or bars |
| `CICADA-UNSUPPORTED` | error | score, command, studio | valid notation that the engine does not implement: a route, effect kind or placement, a setting without an engine representation, or an export option combination | use a supported alternative; the message names what is not implemented |
| `CICADA-USE` | error | score | a phrase use is invalid: repeat outside 1 to 64 or transpose outside -24 to 24 | use repeat 1 to 64 and transpose -24 to +24 |
| `CICADA-VERSION` | error | score, command, studio | the edition is unsupported, the score header and cicada.mod disagree, or a removed edition-1 spelling is used | run cicada fix to migrate legacy spellings (check --json offers it as a fix); keep the header and cicada.mod on one edition |

## CICADA-NOTES reasons

A `CICADA-NOTES` refusal carries one of these values in `reason`.

| `reason` | Meaning |
| --- | --- |
| `invalid-note` | the note has a start, length, pitch or velocity outside its range |
| `note-changed` | the note is missing, or its length or velocity differs; read the pattern again |
| `no-lanes` | the batch names a lane in a pattern without drum lanes |
| `not-whole-steps` | the note does not start or end on the pattern's step grid |
| `outside-pattern` | the note would leave the pattern's steps |
| `outside-lanes` | the note would leave the pattern's lanes |
| `outside-loop` | the note would leave the performance loop |
| `pitch-range` | the note would leave MIDI 0 to 127, counting the pattern's transpose |
| `too-short` | the note would become shorter than one step or one tick |
| `gesture-cut` | the note would end before its last gesture point |
| `overlap` | the note would overlap another note: the same pitch in a performance, or any note in a step pattern |
| `same-pitch` | the note would land on the pitch of another selected note |
| `same-cell` | the note would land on the drum cell of another selected note |
| `chord-pitches-equal` | the note would equal another pitch of its own chord |
| `chord-size` | the note would make a chord of more than four pitches |
| `chord-articulation` | the note would make a chord with a note that slides or ratchets |
| `chord-length` | the note would join a chord of another length |
| `chord-modifiers` | the note would join a chord with another accent, chance or velocity |
| `acid-one-pitch` | the note would put a second pitch on an acid step |
| `fixed-length` | the note is a drum hit, which lasts one step |
| `unknown-lane` | the note names a drum lane the pattern does not have |
