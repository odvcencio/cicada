# AGENTS.md

Cicada is a music workstation in which a song is a readable text score. The
same score renders to the same audio bits on amd64, arm64 and WebAssembly, and
Studio is a fast editor over that score. This file serves two readers: an agent
changing the code (the sections below) and an agent making music with the
`cicada` binary (the last section, generated from the quick start).

## Build and test

- `make build-core` builds `build/cicada`. `GOWORK=off` is set by the Makefile.
- `make test` runs every Go and Node test; CI splits `./cmd/cicada` into shards
  (`make test-cmd-shard SHARD=1/3`, and `make test-cmd-shard-check` proves each
  test is in exactly one).
- `make test-agent-surface` builds the binary and runs `internal/agentsuite`:
  a scripted agent that reads the quick start from `cicada agents`, runs its
  loop and checks every claim the page makes. It must finish within 2 minutes.
- `make grammar` regenerates the parser after a change to
  `language/grammar/grammar.go`.
- Name packages when you test: `go test ./cmd/cicada -run TestName`.

## Where things live

| Folder | What |
| --- | --- |
| `cmd/cicada` | the commands, their `--json` documents, and the Studio service |
| `edit` | the edit intents behind `cicada apply` and Studio |
| `notation`, `language` | the parser, the grammar and the formatter |
| `project` | the semantic model, libraries and parameters |
| `render`, `kernel` | offline rendering and the DSP |
| `host`, `workstation` | the browser and native hosts, and Studio (its own Go module) |
| `internal/agentguide` | the quick start, the core reference and their generator |
| `internal/agentsuite` | the scripted agent run against the built binary |
| `docs/manual`, `docs/spec` | the manual and the language specification |

## Rules a change keeps

- The same score gives the same bits on every architecture: no fused
  multiply-adds and no architecture-dependent `math` calls on the render path
  (`kernel/dsp/detmath` has replacements). A pinned output changes only with a
  stated reason.
- One edit is one revision-checked score change and one Undo step, and an edit
  never changes the music without saying so.
- What Studio can do, an agent can do through a documented, versioned JSON
  interface (`docs/manual/agents.md`); a refusal names what failed.
- Size budgets are real: report sizes when you touch the WebAssembly builds.

## Generated files

Edit the source, then regenerate; a test fails while a generated file is stale.

| Generated | Command |
| --- | --- |
| this file's last section, `llms.txt`, `llms-full.txt`, `docs/manual/agent-reference.md`, `internal/agentguide/*.md`, `examples/first-song/AGENTS.md` | `CICADA_UPDATE_AGENT_FRONT_DOOR=1 go test ./cmd/cicada -run TestAgentFrontDoorIsCurrent` |
| `cmd/cicada/testdata/help/all-commands.golden` | `CICADA_UPDATE_HELP_GOLDEN=1 go test ./cmd/cicada -run TestAllCommandHelpTextGolden` |
| `edit/intent_docs.go` | `CICADA_UPDATE_INTENT_DOCS=1 go test ./edit -run TestIntentDocsAreCurrent` |
| `docs/manual/diagnostics.md` | `CICADA_UPDATE_DIAGNOSTICS_DOC=1 go test ./internal/diagnostics -run TestDiagnosticsReferenceIsCurrent` |
| `notation/cicada.bin` | `make grammar` |

The tests that read the documentation run in the `repo-checks` CI job, on
every pull request, including one that changes only documentation.

<!-- agent-front-door:begin -->
## Using Cicada to make music

<!-- Generated from docs/manual/agent-quickstart.md; run CICADA_UPDATE_AGENT_FRONT_DOOR=1 go test ./cmd/cicada -run TestAgentFrontDoorIsCurrent -->

An agent that makes music with an installed `cicada` binary starts with
`cicada agents`, which prints the quick start
([docs/manual/agent-quickstart.md](docs/manual/agent-quickstart.md)). Its loop:

1. Start: `cicada new demo` makes the project folder `demo` with a song in
   `main.cicada`. Run every other command inside `demo`.
2. Write: replace `main.cicada` with your score (the quick start's next section).
3. Check: `cicada check --json main.cicada`. Go on when `valid` is true.
   `scores[0].revision` is what your next edit must send.
4. Render: `cicada render main.cicada -o mix.wav --loudness -14 --json`. Go on
   when `ok` and `loudness.target_met` are true.
5. Listen: `cicada audition main.cicada --json`.
6. Edit: put intents in `edit.json`, preview with
   `cicada apply main.cicada edit.json --json`, then add `--write`. Go on when
   `ok` and `written` are true; repeat from step 3.

`cicada agents reference` prints the core reference
([docs/manual/agent-reference.md](docs/manual/agent-reference.md)), and
[llms.txt](llms.txt) lists the manual for agents.
<!-- agent-front-door:end -->
