# Cicada's GoSX workstation

Studio is a GoSX server application. Tymbal opens native audio devices; Cicada's
allocation-free kernel renders their buffers. The workstation handles the score
editor, session, pattern grid, mixer, takes, history, audio settings, and export.
It does not put a web framework inside the realtime callback.

## Framework conventions

The implementation was checked against GoSX 0.57.5 source and compiler tests,
rather than assuming that examples on `main` are supported by the pinned version.

- Author strict components in `app/ui/components.gsx`, with same-file props,
  scalar fields, slots, and boolean conditions. Generated Go is a projection.
  `go generate ./...` transpiles components, includes their stylesheet, and
  builds the content-addressed Go/WASM meter module. Generation is part of the
  GoSX build hook and CI checks that checked-in projections remain reproducible.
- Use GoSX's programmatic `server.Page` API for this private, single-score
  workspace. Navigation changes the selected panel without a client router.
  The framework handles history, managed forms, runtime loading, and disposal.
- Mutations are typed GoSX server actions behind encrypted cookie sessions and
  CSRF protection. Native forms and managed forms use the same handlers and
  POST-redirect-GET behavior. Every source mutation carries a disk revision;
  Cicada's existing CST patchers remain authoritative.
- The GoSX editor supplies the code surface, gutter, keyboard behavior, form
  submission, and initial parser-derived highlighting. Parser byte offsets are
  mapped to UTF-16 in one pass. Invalid enhanced submissions retain the draft;
native submissions store a bounded, session-owned draft with a small receipt.
- Transport and export use GoSX live bindings. Output meters use a registered
  GoSX Go/WASM engine and reactive signals. Each mount owns its watcher and
  polling context; disposal cancels both. Meter code never produces audio.
- Use `gosx build --prod` from the pinned module for production runtimes and
  content hashes. Scene3D, video, payments, and relay are excluded. Keep scores,
  recordings, and secrets outside `public/` and the deployment bundle.
- Mark workspace responses `no-store`. During GoSX static export, start a
  private readiness page without a score or audio service. Export cannot bake
  session tokens, devices, project source, or recordings into shared HTML.
- Package the GoSX runtime beside the executable, and locate it at runtime.
  No source-checkout path is built into the installed workstation. Desktop and
  workstation pin the same framework version.

The native process starts the workstation with an ephemeral loopback service
origin and a per-launch credential. The browser talks to GoSX; the workstation
alone can call the private domain service. Parent EOF ends the workstation.
GoSX sessions, request limits, and loopback host checks apply before actions.

## Audio and compatibility boundaries

Tymbal is the default on Windows (WASAPI) and Linux (ALSA). `--audio null` permits
device-free transport testing; it is not hardware qualification. The existing
explicit Oto fallback remains available on other platforms.

GoSX's production engine API supports canvas, fetch, signals, and Go/WASM.
Automatic `//gosx:engine` discovery is a preview feature in this version; the
workstation uses the supported registration API. The current Tymbal pin does
not provide a browser AudioWorklet backend. GoSX's sample audio player does not
replace Tymbal or Cicada's audio kernel.

Legacy browser audio/MIDI adapters remain for portable-host qualification.
Studio's public GoSX server does not serve the old `studio-*.js` application or
proxy its audio adapters. The hidden core `--service` mode supports the existing
host qualification fixtures. Porting browser audio and MIDI needs their own
supported engine boundary; production capability names cannot be invented.

Realtime hubs and CRDT collaboration are appropriate for shared documents and
presence, once Cicada's collaboration milestone defines ownership and auth.
They do not replace revision checks for files on disk. Optional CMS, admin,
payments, Scene3D, and simulation packages are not dependencies of this editor.

## Checks

```sh
make test-workstation
make build-workstation-release
```

Core tests still run from the root module; workstation and desktop tests run
from their separate modules. The CI workstation job checks generation, races,
vet, a production build, and Windows cross-compilation. Kernel WASM and golden
audio gates remain in the core jobs.

## Primary sources

- [GoSX architecture and production API](https://github.com/odvcencio/gosx/blob/v0.57.5/README.md)
- [Strict component checker](https://github.com/odvcencio/gosx/tree/v0.57.5/strictcheck)
- [Server actions](https://github.com/odvcencio/gosx/tree/v0.57.5/action)
- [Sessions and CSRF](https://github.com/odvcencio/gosx/tree/v0.57.5/session)
- [Engine registration and lifecycle](https://github.com/odvcencio/gosx/tree/v0.57.5/engine/wasm)
- [Build and private static-export handling](https://github.com/odvcencio/gosx/tree/v0.57.5/cmd/gosx)
- [GoSX editor](https://github.com/odvcencio/gosx/tree/main/editor)
- [Tymbal platform backends](https://github.com/odvcencio/tymbal/tree/23968cefb0c6)
