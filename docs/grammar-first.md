# Cicada language direction

The `.cicada` score is source code for music. Its compiler parses notation, expands phrases, checks instrument code, resolves tracks and scenes, and creates timestamped events. The audio kernel renders custom mono instruments, the built-in acid voice, and eleven synthesized drum lanes to WAV. The same event and instrument model is intended to serve live output, games, and stems.

```text
.cicada source
  -> grammargen Go DSL grammar + gotreesitter CST
  -> typed score with source positions
  -> phrase expansion + semantic checks
  -> packed patterns + deterministic timed events
  -> instrument DSP graphs + shared audio kernel
  -> WAV (custom mono, acid, and eleven drum lanes)
  -> MIDI export, live engine, and future workstation views
```

This grammar-first prototype broadens an acid-focused music engine toward an extensible DAW. The grammar and examples implement the first syntax slice; the wider workstation design remains a proposal.

## Core language

The parser accepts tracks, authored drum kits, note and drum patterns, scenes, song arrangements, named phrases, and custom instrument declarations. Braces make the syntax independent of indentation. `//` comments are named CST nodes, so editor tooling can retain them. Every syntax node has a source span.

```cicada
cicada 1
tempo 138
key a minor
seed 4242

phrase hook acid { 1^ . 1~ 5 . 1 7, . }

pattern bass-b acid steps=16 swing=54 {
  use hook
  use hook transpose=12
}

instrument glassbass {
  param cutoff: hz = 720hz;
  param bite: unit = 0.65;
  voice mono {
    let osc = saw(pitch);
    let sub = square(pitch / 2);
    let tone = mix(osc, sub, 0.25);
    let shape = env(gate, 380ms);
    out = ladder(tone, cutoff * exp2(shape * 3), bite) * shape;
  }
}

track lead glassbass { cutoff = 900hz }
pattern lead-a notes steps=16 { 5 . . . 3 . . . | 1' . . . 7 . . . }
scene main { lead=lead-a }
song { main*16 }
```

`use` is bounded and expands to explicit steps before scheduling. `transpose` changes the resulting MIDI notes while retaining the authored phrase and transform in the typed score. The broader notation example is [`examples/first-acid.cicada`](../examples/first-acid.cicada). The [circuit kit](../examples/circuit-kit.cicada) is an executable score where bass, kick, snare, and hat are synthesized from instrument code.

## Instrument code

Custom instruments are first-class declarations. A `voice` contains parameters, sequential `let` bindings, and one `out` expression. The current compiler lowers expressions to a typed acyclic graph with a maximum of 128 nodes and 32 stateful nodes per voice. Names must refer to parameters, built-in inputs, or earlier bindings. The type checker understands audio, hertz, milliseconds, dB, unit values, and gates. Its first primitives cover oscillators, noise, envelopes, filters, shaping, mixing, and arithmetic.

The graph executes in the pure-Go audio kernel with fixed state allocated at load time. `Voice.Next` performs no allocation, lock, or goroutine work. A project can define and render sounds without a native plugin or separate executable. This graph is a useful first profile: it composes known primitives into new sounds, but it cannot yet express an arbitrary new DSP algorithm.

The full language should give every sound source the same contract: timestamped note and control events in, declared parameters with units, channel audio out, explicit state, and declared resource needs. An instrument may be a portable source program, a graph, a sample or physical-model asset, or an adapter to an external device. The project should record which implementation it uses and, where possible, a portable fallback. The score and arrangement should not need to change when a sound engine changes.

To support sounds that have not been invented yet, a later language profile needs user-defined DSP functions with bounded loops, delays and state, and explicit memory and CPU limits. It should compile ahead of the audio callback, prohibit allocation and blocking operations there, and state whether its output is deterministic across targets. Versioned libraries and content-addressed assets can then carry reusable instruments without hiding their dependencies. This is design direction; the current grammar implements only the typed graph profile.

Drums use the same event and instrument model. The working [circuit kit](../examples/circuit-kit.cicada) defines independent tracks for code-generated kick, snare, and hat voices. Eleven built-in drum lanes render through the current audio kernel. An [authored kit](../examples/authored-kit.cicada) binds any of those lanes to a built-in recipe or a compiled mono instrument graph. Unbound lanes are silent, and each bound lane owns one voice. Sample-based instruments and audio clips need explicit asset and portability rules.

## Workstation scope

A modern DAW needs more than a parser and renderer. Cicada should share one project model between text editing, a piano roll, drum grid, timeline, mixer, and automation lanes. Each view should edit the same semantic objects, so agents and humans can collaborate without translating between a hidden UI format and notation. Scenes and patterns already provide the first arrangement layer. Clips, multitrack audio, routable buses, effects, automation, undo history, and low-latency live transport still need versioned syntax and runtime semantics.

The next implementation boundary is a host-independent engine contract for instruments, effects, and audio assets. That contract should expose latency, channel layout, parameter units, tail length, supported sample rates, and portability requirements. It will let an analog-inspired bass, a digital drum, a sampled orchestra, and a new code-defined synthesizer occupy the same track and routing system.

## Determinism and editing

- Grammar and semantics are versioned by `cicada 1`.
- Random operations use explicit seeds and fixed algorithms.
- Phrase expansion and instrument graphs have static size limits.
- Syntax and semantic diagnostics carry line, column, and stable codes.
- JSON is typed interchange for workstation and collaboration state. Text/JSON/text conversion preserves semantic meaning; an unedited CST prints the original bytes, including spelling and comments.
- The CST and the [editor queries](editor-tooling.md) give editors an incremental parsing path: highlights, locals, tags, folds, and indents, with one capture for every token. The formatter works; source patches that preserve unaffected text are still planned.

The current implementation compiles scores to note events and instrument graph IR, and renders custom mono graphs, acid, eleven drum lanes, authored kits, a drive insert, delay and reverb returns, music and SFX buses, and an optional music-bus compressor to WAV. Track compressor inserts, the worklet, and the workstation remain open.
