# Public demo integration contract

This branch adds tested integration primitives for the GoSX Studio migration.
It is **not a completed GoSX Studio, a deployed demo, or an adapter that makes
native Studio public**. No existing Studio/CLI files are changed. The intended
public hostname is `cicada.m31labs.dev`.

## Connect the actual migrated app

1. Construct `demopolicy.PublicDemo()` in the browser app. Use `LocalOSS()` for
   the existing OSS product. Select this in trusted app construction; never
   decode a client flag that selects the local profile.
2. Put `Policy.Dispatch` before all export side effects and other commands in
   the common dispatcher. Use the same dispatcher for buttons, keyboard,
   validated agent tools and any experimental WebMCP. Do not register export,
   render-job, project-download, filesystem or native-device tools in demo
   discovery. Unknown command IDs fail closed. WebMCP remains off until its
   implementation and browser support are verified.
3. Render the real GoSX UI from those permissions. Remove export controls and
   shortcuts in the demo profile. Do not leave a hidden button's handler,
   browser Blob/download implementation or raw agent execute route available.
   Keep all local OSS exports. A permission describes what is allowed, not
   evidence that a particular feature is implemented.
4. Give each browser app instance its own `demopolicy.Session` and the actual
   score compiler/validator. All edits and undo/redo live in bounded page
   memory. Reload starts a fresh seed; Reset discards both histories. Say this
   plainly in the UI before visitors start editing. This first boundary does
   not promise account storage, cross-tab drafts or reload persistence.
5. Compile the validated session score and playback image inside the browser.
   Do not fetch the native `/api/kernel-image`, `/api/params`, `/api/source`,
   filesystem-backed asset or capture routes. Pass this project's actual
   sample-rate-specific engine config to `browsercontrol.New`. Its sender is
   `browsercontrol.WorkletSender(node.port)` on the visitor's local worklet.
6. Wire note, live registry parameter, mute and solo controls to that Go
   controller. It validates tracks, notes, drum lanes and parameter values
   through the existing 24-byte kernel command ABI. Current-main mono acid,
   graph and supported drum notes are qualified; unsupported voice types and
   unregistered graph parameters fail explicitly. These changes do not add
   sustain, pitch bend, aftertouch, controller learn application or gamepad
   input. Implement and qualify those separately before advertising them.
7. Audio initialization requires an explicit gesture and a low initial
   listening gain. Never auto-open microphone/MIDI devices. Request browser
   microphone or `requestMIDIAccess({sysex:false})` only from the appropriate
   control, handle refusal/unavailability, and show the active input/output.
   Bind `browsercontrol.BindLifecycle` after initialization; pass an already
   authorized MIDI access object if present. Stop on blur, hidden document,
   suspension, input disconnect, score replacement and disposal. Disconnect
   the worklet immediately if a panic send fails.
8. **After Stop, reinitialize the worklet kernel before permitting Play.** The
   current worklet does not render/drain commands while stopped. A queued live
   NoteOn can otherwise execute after Stop when Play quickly follows. This
   was reproduced against the actual TinyGo reactor in Chromium. The control
   package returns `ErrResetRequired` until the host calls `KernelResetReady`
   after the matching fresh-image `t:'t'` receipt while stopped (or a new node's
   `t:'r'`). A Stop-state receipt is insufficient. Serialize/coalesce reset
   requests and match their identity; disable Play while initialization is
   pending. This host acknowledgement is not an agent command. Do not remove
   the barrier to make UI checks pass.
9. Deploy the reviewed compiled GoSX entry document and explicit runtime/
   playback assets through `demoweb.NewHandler`. It takes immutable bytes and
   an explicit manifest, has no filesystem root or fallback handler, and
   refuses every native, export, agent and server-action API regardless of
   method. It serves only the exact public hostname (or explicit loopback
   preview), sets a restrictive CSP and allows no sandbox downloads. Extract
   any required inline bootstrap script hashes from the exact GoSX build.
   Do not use a working-directory walk or lift native Studio loopback checks.

Bundled immutable WAV assets necessary for playback remain available. The
source shown in an editor and executable playback assets necessarily reach
the visitor. A no-export product policy cannot prevent copying visible source,
recording audible output or capturing a browser's audio. It is not DRM.

## Feature availability and integration boundaries

Canonical main was checked as
`954826cfd34f6f6ec3c1fde9f03c0dd0c111ea03` on 2026-10-02. It still uses native
Studio's HTML/JavaScript UI and has no GoSX dependency. The migration must
provide the real GoSX app before this branch can become a demo.

PR #105 on that main adds experimental bowed string, brass and guitar research
models and a separate audition page. They are not automatically Studio graph
instruments. The fresh expressive-page browser check exercises their actual
Go/WASM audio callbacks but does not prove GoSX/DAW integration or listening
acceptance.

PRs #100, #102 and #103 were open when inspected. Their opt-in graph chords,
chord-aware grid and arrangement schedules are not assumed present in main.
The separately owned integration tree
`c4c6387adbe43bb7ef047074ed0a37123d0dee85` was not available as a GitHub commit
or tree or in this workspace. Its integrated ABI, compiler, UI and actual
feature availability remain unverified. Do not cherry-pick or rewrite those
lanes as part of this policy branch. The controller is qualified against
current-main mono routing and must be requalified after that integration.

All added paths are owned by this lane: `host/demopolicy`, `host/demoweb`,
`host/browsercontrol`, `scripts/demo-browser`, and this document. The migration
owner wires these primitives into shared Studio files. No overlap was edited.

## Reproduce the qualification

Use Go 1.25.0+ and TinyGo 0.41.1 for the unchanged current-main kernel build.
Set `GOWORK=off`, use matching `wasm_exec.js`, and keep cache directories in a
writable location. Node/Playwright and Chromium are required for the last step.

```sh
go test ./host/demopolicy ./host/demoweb ./host/browsercontrol -count=1
go test -race ./host/demopolicy ./host/demoweb ./host/browsercontrol -count=1
go vet ./host/demopolicy ./host/demoweb ./host/browsercontrol
GOMAXPROCS=1 GOOS=js GOARCH=wasm go test ./host/browsercontrol ./host/demopolicy \
  -exec "$(go env GOROOT)/lib/wasm/go_js_wasm_exec" -count=1
make build-kernel-wasm
GOMAXPROCS=1 GOOS=js GOARCH=wasm go build \
  -o build/demo-control-bridge.wasm ./host/browsercontrol/testdata/browser
go build -o build/demo-static-test-server ./host/demoweb/testdata/server
WASM_EXEC_PATH="$(go env GOROOT)/lib/wasm/wasm_exec.js" \
  CHROMIUM=/usr/bin/chromium node scripts/demo-browser/check.cjs
```

`scripts/demo-browser` is a qualification fixture, not an alternative Studio
or a demo to publish. It uses the real reactor, worklet, command encoder,
browser lifecycle binding and static host. It tests finite nonzero acid,
mono graph and drum output; Stop silence; held-repeat suppression and fresh
press; the rapid Stop/Play reset barrier; simulated MIDI disconnect, blur,
visibility and context suspension; export command refusal; and direct API
refusal. Screenshots record the fixture at 390 and 1440 pixels, not the GoSX
Studio responsive layout. Real MIDI hardware, real microphone permission,
audible output, acoustic quality, Windows Chrome p99 and mobile Safari are
not qualified by this fixture.

The current-main reactor built as 266,005 raw bytes and 80,800 Brotli bytes
under its unchanged 307,200/122,880-byte limits. The fresh fixture run used
Chromium 151.0.7922.173 and 48 kHz. Before adding the reset barrier, the rapid
Stop/Play regression failed with nonzero output; the corrected run passed.
Do not label historical failing receipts as passes.

## Publication gate

Use the existing authorized M31 deployment/operator environment and an
isolated Cicada target. The main M31 site deployment must not be replaced.
Record source SHA, image digest, target namespace/service/ingress, current
deployment revision and exact rollback before any rollout. Check existing
domain routing and TLS without changing DNS, issuer/security permissions or
creating paid services/credentials. The selected workspace did not have that
operator context or registry authorization; publication was not attempted.

The final GoSX app still needs actual browser checks for playback, editing,
undo/redo, session/reset/reload isolation, keyboard/agent export bypasses,
permissions, layouts at narrow/desktop widths and service-worker/cache
behavior. A passing integration fixture does not clear those app gates.
