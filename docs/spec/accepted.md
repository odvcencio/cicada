# Accepted source syntax that has not merged

These designs are accepted for later additive changes to edition 1. Each section states whether source syntax is available and whether engine support has landed. Remaining `cicada-accepted` examples are design records, not runnable scores.

## Automation blocks

**Status:** Accepted; not available in the current build.

**Syntax (EBNF):**

```ebnf
automate_decl ::= "automate" , parameter_path , "{" , { automation_point } , "}" ;
automation_point ::= position , value , [ shape ] ;
position ::= "@" , integer , "." , integer , "." , integer ;
shape ::= "step" | "linear" | "smooth" | "curve" , number ;
```

**Meaning:** A lane changes a registered parameter over song time. Lanes may live at song, scene, or pattern scope. A pattern row can hold per-step values in the accepted flexible grid. Interpolation uses the parameter's control space; scope precedence follows the accepted composition model.

**Types and units:** Positions are one-based bar.beat.step values. Point values use the addressed parameter's registered type and unit. Shapes are step, linear, smooth, or a curve with a numeric tension.

**Defaults:** `linear` is the default shape. An omitted lane contributes no automation.

**Errors:** Invalid positions, unresolved paths, incompatible units, and out-of-order points must be rejected. Stable diagnostics and boundary behavior have not landed.

**Example:**

```cicada-accepted
automate bass.cutoff {
  @1.1.1 400Hz
  @5.1.1 2400Hz smooth
}
```

**Edition history:** Accepted as additive edition-1 syntax and semantic JSON version 2 work. The current build has no automation record or Studio lane.

## Flexible grid and pattern chains

**Status:** Accepted; not available in the current build.

**Syntax (EBNF):**

```ebnf
grid_setting ::= "step" , "=" , fraction ;
tuplet_group ::= "[" , { acid_step } , "]" ;
parameter_row ::= identifier , ":" , { value | "." } ;
chain_decl ::= "chain" , "=" , identifier , { identifier } ;
```

**Meaning:** A pattern-level step duration selects its grid resolution. A bracketed group subdivides one cell evenly; a following tie extends the group. Labeled rows attach one-step values such as cutoff, velocity, nudge, vibrato, or cents. A source chain plays its listed patterns in order and loops.

**Types and units:** Step duration is a note division. At 960 pulses per quarter note (PPQ), accepted values must occupy a whole number of ticks while the sequencer uses integer time. `nudge` is a percentage of one step; each other row uses the parameter's type or the MIDI velocity range. Chains contain at most 32 pattern names.

**Defaults:** `step = 1/16` preserves the current grid. A missing row value (`.`) means that the step has no value. A track without a chain keeps its current pattern behavior.

**Errors:** Divisions that do not produce whole ticks, groups without valid cells, row lengths that differ from the pattern, and invalid chain references must be rejected. For example, `1/16t` and `1/20` fit the 960-PPQ grid; `1/28` does not.

**Example:**

```cicada-accepted
pattern triplet {
  step = 1/8t
  [1 3 5] -
  cutoff: 600Hz . 900Hz
}
track bass acid { chain = intro triplet chorus }
```

**Edition history:** Accepted as an additive edition-1 form. The current source grammar still uses a fixed sixteenth-note grid and has no parameter rows or source chain declaration.

## Multi-file projects

**Status:** Accepted; not available in the current build.

**Syntax (EBNF):**

```ebnf
import_decl ::= "import" , string ;
```

**Meaning:** All `.cicada` files under one `cicada.mod` share one namespace, like files in one Go package. Declarations may appear in any file and any order. Imports are for libraries; a qualified name selects a library declaration. Library declarations whose names begin with underscore are private. A library has its own manifest and edition.

**Types and units:** Imports name library paths. `require` directives pin library versions; `cicada.sum` records their SHA-256 hashes.

**Defaults:** Files in the same project need no import to reference one another. A single-file project remains valid without changes.

**Errors:** Duplicate declarations across files must report both source locations. Unresolved cross-file references and missing or mismatched library versions must be rejected. Loader diagnostics are not implemented.

**Example:**

```cicada-accepted
// parts/bass.cicada
pattern bass-a { 1^ . 1~ 5 }

// main.cicada
import "std/theremin"
track lead theremin.classic {}
scene main {
  bass = bass-a
  lead = lead-a
}
song { main*8 }
```

**Edition history:** Accepted as additive edition-1 support. Current project tools can walk multiple files, but they compile each score independently and cannot resolve declarations across files.
