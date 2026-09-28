# Typed semantic project and agent mapping

This page defines the current typed JSON interchange form. It is distinct from both Cicada source syntax and the parser's concrete syntax tree.

## Semantic JSON

**Status:** Implemented.

**Syntax:** JSON objects follow the public [project schema /1](../../project/schema/project-1.json) or [project schema /2](../../project/schema/project-2.json). The generated [field catalog /1](../../project/schema/cicada.fields-1.json) and [field catalog /2](../../project/schema/cicada.fields-2.json) index their constructs and fields. Run <code>go run ./cmd/cicada fields</code> to print the catalog or <code>go run ./cmd/cicada explain pattern.steps --json</code> to inspect one field.

**Meaning:** <code>cicada.project/1</code> stores the typed project consumed by validation, rendering, interchange, and agent tools when it has no /2-only content. <code>cicada.project/2</code> adds scene settings, named effects and mixer records, built-in bus records, master settings, and export profiles. The field catalogs record each construct, field order, required flag, type, unit, range, default, meaning, profile, and format that introduced it.

**Types and units:** The project uses records for the key, instruments, expressions, kits, tracks, mixer values, patterns, steps, scenes, song entries, effects, buses, master, and exports. Tempo is an integer in milli-BPM. Step notes are resolved MIDI numbers; chance is an integer percentage; effect and track parameters are tagged number-or-text values. The schema rejects unknown fields.

**Defaults:** A loose source file without a header uses edition 1; a project manifest may select edition 1 or 2. JSON written by Cicada records its source edition, and older JSON that omits it reads as edition 1. Source defaults are resolved into the semantic project, including tempo, key, seed, mixer values, pattern metadata, and note defaults.

**Errors:** Invalid JSON reports a stable Cicada diagnostic and a JSON Pointer when a field can be located. JSON input and canonical output are limited to 2 MiB and nesting depth 64. Source validation reports <code>CICADA-LIMIT</code> if its canonical project would exceed 2 MiB.

**Example:** Convert a validated source file with <code>cicada convert score.cicada -o score.json</code>, then inspect the JSON with the project schema and field catalog.

**Edition history:** Semantic formats /1 and /2 and their field catalogs are implemented. Source editions 1 and 2 are implemented; the source edition and semantic format version are independent. Automation remains accepted and is not present in either format.

## Semantic JSON version 2 scene settings

**Status:** Implemented.

**Syntax:** In <code>cicada.project/2</code>, a scene may contain an ordered <code>settings</code> array. Each entry contains a resolved <code>path</code> and a typed <code>value</code>. The [project /2 schema](../../project/schema/project-2.json) defines the complete object.

**Meaning:** The path records the source parameter address, such as <code>bass.cutoff</code>. The value records the registry-validated setting in semantic form. Source order is preserved, and a path may appear at most once in a scene.

**Types and units:** Numeric values use a <code>number</code> in the registry's base unit with a matching <code>unit</code>. Enumerated values use <code>unit: "enum"</code> and <code>text</code>; for example, <code>level = off</code> becomes enum text <code>off</code>. Numeric values do not include both number and text.

**Defaults:** A scene without settings omits <code>settings</code>. Canonical JSON uses <code>cicada.project/1</code> whenever no /2-only content is present, including when the in-memory project carries a /2 marker. It writes <code>cicada.project/2</code> when scene settings, named mixer forms, buses, a master block, or exports require it.

**Errors:** A /1 JSON document containing scene settings reports <code>CICADA-VERSION</code>. Paths must resolve to live registry entries, values must match their type, unit, and range, and duplicate paths are rejected. Invalid paths and values report the stable Cicada diagnostics described under [Diagnostics](#diagnostics).

**Example:** This fragment shows a frequency setting in base units:

```json
{
  "format": "cicada.project/2",
  "version": 2,
  "scenes": [
    {
      "id": "drop",
      "bindings": { "bass": "pulse" },
      "settings": [
        { "path": "bass.cutoff", "value": { "number": 900, "unit": "hz" } }
      ]
    }
  ]
}
```

**Edition history:** Version /2 adds ordered scene parameter settings. Projects without those settings remain representable as /1.

## Semantic JSON version 2 named mixer records

**Status:** Implemented for the mixer routes supported by the current engine.

**Syntax:** Project /2 adds <code>effects[]</code> records with <code>id</code>, <code>kind</code>, and <code>params</code>; <code>buses[]</code> records with <code>id</code> and <code>mixer</code>; an optional <code>master</code> record containing <code>mixer</code>; and <code>exports[]</code> render profiles. The /2 mixer replaces <code>gain_db</code>, <code>send_a</code>, <code>send_b</code>, <code>send_pre</code>, <code>insert</code>, and <code>bus</code> with <code>level</code>, <code>pan</code>, <code>mute</code>, <code>solo</code>, <code>inserts[]</code>, <code>sends[]</code>, and <code>out</code>. Each send record contains <code>to</code>, <code>level</code>, and <code>tap</code>.

**Meaning:** Effect IDs preserve the source instance name; the kind selects one built-in processor. A send names its effect destination and has its own pre or post tap. The built-in <code>music</code> and <code>sfx</code> buses may be absent from <code>buses[]</code>; absent records use engine defaults. A master record changes only fields it contains. The master safety limiter is implicit and remains last. Export profiles supply named render settings.

**Types and units:** Mixer levels and pans are numbers in dB and unitless pan range. Mute and solo are booleans. Insert chains and sends are ordered arrays. Send levels use <code>unit</code> for linear gain or <code>db</code> for decibels; <code>tap</code> is <code>pre</code> or <code>post</code>. Export tail, loudness, and true peak keep the base units <code>ms</code>, <code>lufs</code>/<code>lu</code>, and <code>dbtp</code>. JSON stores the number in that unit without its source suffix.

**Defaults:** Missing track mixer fields use edition-1 track defaults. The music bus keeps its fixed −3 dB trim and SFX remains at unity. Missing bus and master records do not change engine defaults. Missing export fields retain the render CLI defaults.

**Errors:** A /2 object with unknown fields, invalid presence or value variants, an unknown effect reference, or an engine-unsupported mixer route is rejected. Valid notation whose route is not implemented reports <code>CICADA-UNSUPPORTED</code>.

**Example:** These declarations lower to /2 records:

```cicada
fx room delay {}
bus music { insert = none }
master { insert = none }
export streaming { rate = 48000Hz bits = 24 tail = 2s loudness = -14LUFS }
track bass acid { send room = 0.2 pre out = music }
pattern pulse acid { 1 . }
scene main { bass = pulse }
song { main }
```

This JSON fragment shows the named records and their base units:

```json
{
  "effects": [{"id": "room", "kind": "delay", "params": {}}],
  "tracks": [{
    "id": "bass",
    "mixer": {
      "sends": [{"to": "room", "level": {"unit": "ratio", "number": 0.2}, "tap": "pre"}],
      "out": "music"
    }
  }],
  "buses": [{"id": "music", "mixer": {"inserts": ["glue"]}}],
  "master": {"mixer": {"level": {"unit": "db", "number": -1}}},
  "exports": [{
    "id": "streaming",
    "rate": 48000,
    "bits": 24,
    "tail": {"unit": "ms", "number": 2000},
    "loudness": {"unit": "lufs", "number": -14},
    "true_peak": {"unit": "dbtp", "number": -1}
  }]
}
```

```cicada-invalid CICADA-UNSUPPORTED
bus ambience {}
track bass acid {}
pattern pulse acid { 1 }
scene main { bass = pulse }
song { main }
```

**Edition history:** Named mixer records are part of project /2. <code>Migrate1To2</code> carries legacy mixer values into the named record shape; the writer still selects /1 when no /2-only content is needed.

## Source-to-JSON mapping

| Cicada source | Semantic JSON | Mapping |
| --- | --- | --- |
| <code>title</code>, <code>tempo</code>, <code>key</code>, <code>seed</code> | Project fields | Tempo is scaled to integer milli-BPM; key root becomes pitch class 0–11. |
| <code>instrument</code>, <code>param</code>, <code>let</code>, <code>out</code> | <code>instruments[]</code> | The typed graph is stored as expression nodes and ordered bindings. |
| <code>kit</code> | <code>kits[]</code> | Lane names map to an instrument ID or a <code>builtin.&lt;lane&gt;</code> recipe. |
| <code>track</code> | <code>tracks[]</code> | Parameters are typed values; the mixer is a separate record; each track has 16 pattern slots. Project /1 uses fixed send and bus fields; /2 uses named sends, taps, inserts, and output. |
| Note pattern | <code>patterns[].data[]</code> | Source degrees and spelling resolve to MIDI note numbers. |
| Drum pattern | <code>patterns[].lanes</code> | Each lane stores one nullable step per pattern position. |
| Scene | <code>scenes[].bindings</code>; <code>scenes[].settings[]</code> in /2 | Track names map to pattern IDs or the <code>off</code> action. <code>keep</code> is omitted from the resolved map. Version /2 settings preserve the path and typed value in source order. |
| Song | <code>song[]</code> | Each entry contains a scene ID and bar count. |
| <code>fx</code>, <code>bus</code>, <code>master</code>, <code>export</code> | <code>effects[]</code>, <code>buses[]</code>, <code>master</code>, <code>exports[]</code> in /2 | Effect IDs and kinds, mixer settings, ordered sends and inserts, and render targets are preserved. |

The JSON step record contains <code>note</code>, <code>accent</code>, <code>slide</code>, <code>tie</code>, <code>ratchet</code>, <code>probability</code>, and <code>velocity</code>. Nullable step entries represent rests. All source declarations that do not fit the current schema fail conversion; conversion does not silently discard them.

## Canonicalization and source round trips

**Status:** Implemented.

**Syntax:** <code>cicada convert source.cicada -o project.json</code> writes canonical semantic JSON. <code>cicada convert project.json -o source.cicada</code> writes normalized source. <code>cicada compare --semantic a b</code> compares compiled project meaning.

**Meaning:** Semantic comparison ignores comments, layout, and equivalent source spellings. JSON-to-source conversion writes normalized source in the project's source edition and checks that recompiling it produces the same canonical project. Edition-2 source output includes a `cicada 2` header. Canonical JSON writes /1 when no /2-only content is needed, and /2 when mixer records, buses, master settings, exports, or scene settings require it.

**Types and units:** JSON values retain their unit tag. Expression nodes are exactly one of a literal, a name, or an operator with arguments.

**Defaults:** Canonical JSON fills the typed project model; source output omits default-valued declarations when the concise source can preserve their meaning.

**Errors:** A project that has no representation in its selected source edition is rejected with <code>CICADA-PARAM</code>. Invalid or unsupported data is not silently removed.

**Example:**

```cicada
title "Agent sketch"
tempo 120
key c major
track melody acid {}
pattern phrase { 1 . 3 . 5 . 3 . }
scene main { melody = phrase }
song { main*4 }
```

**Edition history:** Available for <code>cicada.project/1</code>. A future JSON format change must use an explicit migration.

## <code>cicada fix</code> and migrations

**Status:** Implemented.

**Syntax:** <code>cicada fix score.cicada</code> migrates edition-1 source to edition 2. <code>cicada fix score.cicada --check</code> reports whether a rewrite or manifest update is needed without writing. The Go API <code>project.Migrate1To2(p)</code> migrates a semantic project.

**Meaning:** The source migration edits tokens while preserving comments and unrelated layout. It checks that the source compiles before migration and that the semantic project remains equal afterward. A loose score gets a <code>cicada.mod</code> manifest; an existing edition-1 manifest is upgraded to edition 2. <code>Migrate1To2</code> validates a /1 project, returns a detached copy with <code>format: cicada.project/2</code> and <code>version: 2</code>, and leaves the input unchanged.

**Types and units:** <code>fix</code> rewrites <code>send_a</code> and <code>send_b</code> to named delay and reverb sends, moves <code>send_pre = true</code> onto each send as <code>pre</code>, changes <code>bus = sfx</code> to <code>out = sfx</code>, adds <code>bus music { insert = comp }</code> for a legacy compressor, and changes track-block <code>level = off</code> to <code>mute = on</code>. It also expands shorthand effect declarations, removes default-only legacy settings, and records edition 2 in the project manifest. <code>Migrate1To2</code> accepts only a valid <code>cicada.project/1</code> value and carries its legacy mixer values into /2 records.

**Defaults:** <code>--check</code> is read-only. A score that already uses the canonical form is reported as already fixed. A migrated project with no scene settings is still written as /1 by <code>CanonicalJSON</code>.

**Errors:** A score must parse, validate, and compile before source migration. If a legacy spelling is ambiguous or comments cannot safely move with it, migration refuses that rewrite. <code>Migrate1To2</code> rejects nil, non-/1, or invalid projects. A semantic change after source rewriting is an error.

**Example:** <code>cicada fix score.cicada --check</code> is suitable for an editor or CI preflight; omit <code>--check</code> to write the source migration. A Go caller can write <code>v2, err := project.Migrate1To2(v1)</code>.

**Edition history:** Source <code>cicada fix</code> migrates edition 1 to edition 2. <code>Migrate1To2</code> bridges semantic JSON /1 to /2 while preserving legacy mixer meaning.

## Diagnostics

**Status:** Implemented.

**Syntax:** CLI diagnostics use <code>path:line:column: severity CODE: message</code>; JSON diagnostics add a JSON Pointer after the path when available. LSP diagnostics use the same source diagnostic and map columns to the protocol's UTF-16 position encoding.

**Meaning:** Syntax diagnostics come from parsing; semantic diagnostics come from name, type, unit, range, and engine checks. A well-formed score can still fail semantic validation or engine compilation.

**Types and units:** Source line and column positions are one-based; columns count Unicode scalar values. A slide into a rest may be a warning (<code>CICADA-SLIDE-REST</code>), not an error.

**Defaults:** Diagnostics are sorted by source position. <code>cicada check</code> adds a source caret and selected repair suggestions.

**Errors:** Stable source and command codes include CICADA-SYNTAX, CICADA-VERSION, CICADA-KEY, CICADA-SCALE-DEGREE, CICADA-SEED, CICADA-PARAM, CICADA-UNIT, CICADA-REFERENCE, CICADA-DUPLICATE, CICADA-EXPANSION, CICADA-USE, CICADA-UNSUPPORTED, CICADA-LIMIT, CICADA-SLIDE-REST, and CICADA-IO. Phrase-mutation APIs also return CICADA-LOCKED when a requested repair would need to change a locked step; this is not a source-validation diagnostic.

**Example:** An invalid example in the language reference names its expected code in the fence tag; CI fails if the real validator returns a different diagnostic.

**Edition history:** The listed source codes are used by edition 1. New diagnostics are part of the public validator and should remain stable once published.

## Conformance

**Status:** Implemented.

**Syntax:** The authoritative source grammar is the grammargen Go DSL in <code>language/grammar/grammar.go</code>. <code>go generate ./notation</code> builds the parser blob; <code>make grammar-check</code> checks it against the DSL.

**Meaning:** An edition-1 or edition-2 implementation accepts the grammar and edition-specific semantic limits in this specification and rejects unsupported constructs. <code>cicada validate</code> checks a score; <code>cicada check</code> checks a score or project; <code>cicada fmt --check</code> checks normalized formatting.

**Types and units:** The semantic gate includes instrument graph typing and engine compilation, not only parsing. CI runs <code>go test ./...</code> through <code>make test</code>, plus the repository grammar and engine checks.

**Defaults:** Every <code>cicada</code> block in <code>docs/spec</code> and <code>docs/manual</code> is validated in Go tests. <code>cicada-invalid</code> blocks must fail with their named diagnostic. <code>cicada-accepted</code> blocks are counted and skipped until implementation lands.

**Errors:** Grammar drift, a nonconforming example, an unexpected diagnostic, or an EBNF production-name mismatch fails the relevant test.

**Example:** Run <code>go test ./cmd/cicada -run 'TestDocumentation'</code> to check documentation examples and the EBNF production names locally.

**Edition history:** The conformance tests in this specification exercise both source editions. The audio golden fingerprint is a separate regression check: eight bars of the first-acid score are compared using 100 ms spectral frames and 64 frequency bands; it does not judge whether a render sounds good.
