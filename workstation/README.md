# Cicada's GoSX workstation

Studio is a GoSX server application. Tymbal opens native audio devices; Cicada's
allocation-free kernel renders their buffers. The workstation handles the score
editor, session, pattern grid, generator, live performance, mixer, takes,
history, audio settings, and export.
It does not put a web framework inside the realtime callback.

## Framework conventions

The implementation was checked against GoSX 0.57.5 source and compiler tests,
rather than assuming that examples on `main` are supported by the pinned version.

- Author strict components in `app/ui/components.gsx`, with same-file props,
  scalar fields, slots, and boolean conditions. Generated Go is a projection.
  `go generate ./...` transpiles components, includes their stylesheet, and
  builds the content-addressed Go/WASM engine module. Generation is part of the
  GoSX build hook and CI checks that checked-in projections remain reproducible.
- Use GoSX's programmatic `server.Page` API for this private, single-score
  workspace. Navigation changes the selected panel without a client router.
  The framework handles history, managed forms, runtime loading, and disposal.
- Mutations are typed GoSX server actions behind encrypted cookie sessions and
  CSRF protection. Native forms and managed forms use the same handlers and
  POST-redirect-GET behavior. Every source mutation carries a disk revision;
  Cicada's existing CST patchers remain authoritative.
- Arrangement blocks, the piano roll, drum lanes, step dynamics, range edits,
  pattern variations, and project metadata use server-rendered GoSX controls
  and these same actions. Step numbers are one-based in the UI and lowered once
  at the action boundary. Phrase uses expand inside the edited pattern rather
  than modifying a shared definition. Variations preserve current MIDI pitches
  and drop explicit slot placement; scene assignment allocates a free slot.
  Copy/reverse/rotate/transpose/clear and resize are atomic source edits with one
  Undo entry. No additional application JavaScript is required.
- Export delivery uses GoSX live text and attribute bindings for progress,
  measurements, button availability, and the completed WAV link. Rendering
  carries the displayed revision; an opaque job ID can select only the current
  finished artifact. The server streams it privately without exposing a client
  filesystem selector, placing audio in public/, or limiting it to JSON size.
- The GoSX editor supplies the code surface, gutter, keyboard behavior, form
  submission, and initial parser-derived highlighting. Parser byte offsets are
  mapped to UTF-16 in one pass. Invalid enhanced submissions retain the draft;
native submissions store a bounded, session-owned draft with a small receipt.
- Transport and export use GoSX live bindings. Output meters use a registered
  GoSX Go/WASM engine and reactive signals. Each mount owns its watcher and
  polling context; disposal cancels both. Meter code never produces audio.
- Live keyboard, pointer, and explicitly permitted Web MIDI input run in a
  registered GoSX Go/WASM engine. MIDI curves come from the domain parameter
  registry. Ordered mount leases release owned notes on blur or disposal and
  reject late commands; releasing one mount cannot stop another's held pitch.
  Session-owned note previews require explicit commit and retain revision
  conflicts. Native sample audition stays a private, no-store WAV stream,
  rendered by the existing sample voice. GoSX disposal stops its media element.
  Live and Takes keep their SSR controls inside their engine mount. Stable
  props preserve that subtree during navigation; changed preview receipts or
  take projections cause a remount with fresh controls.
- Native capture forms work without browser scripting. A GoSX engine polls a
  compact capture projection for count-in, durable frames, and incomplete-input
  status. It never transfers PCM or retained source candidates in polling data.
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
Both Studio's public GoSX server and its private production domain service
exclude the old `studio-*.js` application, WebSocket audio bridge, kernel WASM,
and browser capture adapters. The explicit core `--service` mode supports the
existing host qualification fixtures. Browser AudioWorklet input is not an
alternate production recording path: recording uses Tymbal's selected native
input. Web MIDI is explicitly feature-detected inside the unrestricted
Go/WASM engine; no nonexistent framework MIDI capability is declared.

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
