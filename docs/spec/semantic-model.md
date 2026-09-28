# Typed semantic project and agent mapping

This page defines the current typed JSON interchange form. It is distinct from both Cicada source syntax and the parser's concrete syntax tree.

## Semantic JSON

**Status:** Implemented.

**Syntax:** JSON objects follow the public [project schema /1](../../project/schema/project-1.json) or [project schema /2](../../project/schema/project-2.json). The generated [field catalog /1](../../project/schema/cicada.fields-1.json) and [field catalog /2](../../project/schema/cicada.fields-2.json) index their constructs and fields. Run <code>go run ./cmd/cicada fields</code> to print the catalog or <code>go run ./cmd/cicada explain pattern.steps --json</code> to inspect one field.

**Meaning:** <code>cicada.project/1</code> stores the typed project consumed by validation, rendering, interchange, and agent tools when it has no scene parameter settings. <code>cicada.project/2</code> adds those settings. The field catalogs record each construct, field order, required flag, type, unit, range, default, meaning, profile, and format that introduced it.

**Types and units:** The project uses records for the key, instruments, expressions, kits, tracks, mixer values, patterns, steps, scenes, song entries, and effects. Tempo is an integer in milli-BPM. Step notes are resolved MIDI numbers; chance is an integer percentage; effect and track parameters are tagged number-or-text values. The schema rejects unknown fields.

**Defaults:** Source edition is 1. JSON written by Cicada records edition 1; older JSON that omits it reads as edition 1. Source defaults are resolved into the semantic project, including tempo, key, seed, mixer values, pattern metadata, and note defaults.

**Errors:** Invalid JSON reports a stable Cicada diagnostic and a JSON Pointer when a field can be located. JSON input and canonical output are limited to 2 MiB and nesting depth 64. Source validation reports <code>CICADA-LIMIT</code> if its canonical project would exceed 2 MiB.

**Example:** Convert a validated source file with <code>cicada convert score.cicada -o score.json</code>, then inspect the JSON with the project schema and field catalog.

**Edition history:** Semantic formats /1 and /2 and their field catalogs are implemented. The source edition and semantic format version are independent. Scene parameter settings require /2; automation remains accepted and is not present in either format.

## Semantic JSON version 2 scene settings

**Status:** Implemented.

**Syntax:** In <code>cicada.project/2</code>, a scene may contain an ordered <code>settings</code> array. Each entry contains a resolved <code>path</code> and a typed <code>value</code>. The [project /2 schema](../../project/schema/project-2.json) defines the complete object.

**Meaning:** The path records the source parameter address, such as <code>bass.cutoff</code>. The value records the registry-validated setting in semantic form. Source order is preserved, and a path may appear at most once in a scene.

**Types and units:** Numeric values use a <code>number</code> in the registry's base unit with a matching <code>unit</code>. Enumerated values use <code>unit: "enum"</code> and <code>text</code>; for example, <code>level = off</code> becomes enum text <code>off</code>. Numeric values do not include both number and text.

**Defaults:** A scene without settings omits <code>settings</code>. Canonical JSON uses <code>cicada.project/1</code> whenever no /2-only scene settings are present, including when the in-memory project carries a /2 marker. It writes <code>cicada.project/2</code> when at least one scene has settings.

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

## Source-to-JSON mapping

| Cicada source | Semantic JSON | Mapping |
| --- | --- | --- |
| <code>title</code>, <code>tempo</code>, <code>key</code>, <code>seed</code> | Project fields | Tempo is scaled to integer milli-BPM; key root becomes pitch class 0–11. |
| <code>instrument</code>, <code>param</code>, <code>let</code>, <code>out</code> | <code>instruments[]</code> | The typed graph is stored as expression nodes and ordered bindings. |
| <code>kit</code> | <code>kits[]</code> | Lane names map to an instrument ID or a <code>builtin.&lt;lane&gt;</code> recipe. |
| <code>track</code> | <code>tracks[]</code> | Parameters are typed values; the mixer is a separate record; each track has 16 pattern slots. |
| Note pattern | <code>patterns[].data[]</code> | Source degrees and spelling resolve to MIDI note numbers. |
| Drum pattern | <code>patterns[].lanes</code> | Each lane stores one nullable step per pattern position. |
| Scene | <code>scenes[].bindings</code>; <code>scenes[].settings[]</code> in /2 | Track names map to pattern IDs or the <code>off</code> action. <code>keep</code> is omitted from the resolved map. Version /2 settings preserve the path and typed value in source order. |
| Song | <code>song[]</code> | Each entry contains a scene ID and bar count. |
| <code>fx</code> | <code>effects[]</code> | The effect ID and typed parameter map are preserved. |

The JSON step record contains <code>note</code>, <code>accent</code>, <code>slide</code>, <code>tie</code>, <code>ratchet</code>, <code>probability</code>, and <code>velocity</code>. Nullable step entries represent rests. All source declarations that do not fit the current schema fail conversion; conversion does not silently discard them.

## Canonicalization and source round trips

**Status:** Implemented.

**Syntax:** <code>cicada convert source.cicada -o project.json</code> writes canonical semantic JSON. <code>cicada convert project.json -o source.cicada</code> writes normalized source. <code>cicada compare --semantic a b</code> compares compiled project meaning.

**Meaning:** Semantic comparison ignores comments, layout, and equivalent source spellings. JSON-to-source conversion writes the concise edition-1 form and checks that recompiling it produces the same canonical project. Canonical JSON writes /1 when there are no scene settings and /2 when any scene contains a setting, regardless of an unused /2 marker on the in-memory project.

**Types and units:** JSON values retain their unit tag. Expression nodes are exactly one of a literal, a name, or an operator with arguments.

**Defaults:** Canonical JSON fills the typed project model; source output omits default-valued declarations when the concise source can preserve their meaning.

**Errors:** A project that has no edition-1 source representation is rejected with <code>CICADA-PARAM</code>. Invalid or unsupported data is not silently removed.

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

**Syntax:** <code>cicada fix score.cicada</code> applies the edition-1 spelling migration. <code>cicada fix score.cicada --check</code> reports whether a rewrite or manifest is needed without writing. The Go API <code>project.Migrate1To2(p)</code> migrates a semantic project.

**Meaning:** The source migration edits tokens while preserving comments and unrelated layout. It checks that the source compiles before migration and that the semantic project remains equal afterward. A loose score gets a <code>cicada.mod</code> manifest. <code>Migrate1To2</code> validates a /1 project, returns a detached copy with <code>format: cicada.project/2</code> and <code>version: 2</code>, and leaves the input unchanged.

**Types and units:** <code>fix</code> runs only for edition 1. It moves a standalone <code>cicada 1</code> header into the manifest; writes missing instrument octaves; removes redundant <code>steps</code>, <code>acid</code>/<code>notes</code>, and inferred unit annotations; removes no-op <code>keep</code> actions; rewrites unambiguous <code>off</code> actions to <code>stop</code>; moves pattern attributes into braces; shortens positive phrase transposes; changes <code>%N</code> chance to <code>?N</code>; normalizes SI unit case; and removes statement terminators. <code>Migrate1To2</code> accepts only a valid <code>cicada.project/1</code> value and sets the format and version to /2; it does not add scene settings.

**Defaults:** <code>--check</code> is read-only. A score that already uses the canonical form is reported as already fixed. A migrated project with no scene settings is still written as /1 by <code>CanonicalJSON</code>.

**Errors:** A score must parse, validate, and compile before source migration. If a legacy spelling is ambiguous or comments cannot safely move with it, migration refuses that rewrite. <code>Migrate1To2</code> rejects nil, non-/1, or invalid projects. A semantic change after source rewriting is an error.

**Example:** <code>cicada fix score.cicada --check</code> is suitable for an editor or CI preflight; omit <code>--check</code> to write the source migration. A Go caller can write <code>v2, err := project.Migrate1To2(v1)</code>.

**Edition history:** The source migration targets edition 1. <code>Migrate1To2</code> bridges semantic /1 to /2 without introducing source syntax or scene settings.

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

**Meaning:** An edition-1 implementation accepts the grammar and semantic limits in this specification and rejects unsupported constructs. <code>cicada validate</code> checks a score; <code>cicada check</code> checks a score or project; <code>cicada fmt --check</code> checks normalized formatting.

**Types and units:** The semantic gate includes instrument graph typing and engine compilation, not only parsing. CI runs <code>go test ./...</code> through <code>make test</code>, plus the repository grammar and engine checks.

**Defaults:** Every <code>cicada</code> block in <code>docs/spec</code> and <code>docs/manual</code> is validated in Go tests. <code>cicada-invalid</code> blocks must fail with their named diagnostic. <code>cicada-accepted</code> blocks are counted and skipped until implementation lands.

**Errors:** Grammar drift, a nonconforming example, an unexpected diagnostic, or an EBNF production-name mismatch fails the relevant test.

**Example:** Run <code>go test ./cmd/cicada -run 'TestDocumentation'</code> to check documentation examples and the EBNF production names locally.

**Edition history:** The conformance tests in this specification exercise edition 1. The audio golden fingerprint is a separate regression check: eight bars of the first-acid score are compared using 100 ms spectral frames and 64 frequency bands; it does not judge whether a render sounds good.
