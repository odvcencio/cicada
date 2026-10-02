# Device performance and local capture slice

Base: `954826cfd34f6f6ec3c1fde9f03c0dd0c111ea03` (main, including PR #105).
This lane changes input ownership and existing capture orchestration. It does
not migrate Studio to GoSX, change a kernel image/opcode/parameter ABI, change
network listeners or authorization, modify dependency pins, or publish a demo.

## Ownership and integration boundary

Owned implementation: `cmd/cicada/studio-midi.js`, `studio-audio.js`,
`studio-live.js`, `studio-capture.js`, their tests and `package.json`;
`cmd/cicada/studio_audio.go` for native WebSocket owner cleanup;
`host/liveplay/player.go` and `note_test.go` for native ownership;
`host/web/capture-client.js` and `capture.test.cjs` for microphone admission,
selection and interruptions. This document is the review handoff.

`view.html`, CSS, GoSX migration files, `host/web/client.js`, processor assets,
`host/browsercontrol`, public-demo policy and hosting are unchanged. The small
legacy Live/Record controls are constructed by their existing adapters. The
migration owner can replace this construction while keeping the domain APIs.
Mobile capture has its own backend selector inside Record; it does not depend
on the toolbar's hidden-at-640px audio-mode selector. Existing mobile toolbar
MIDI visibility still belongs to the UI owner and is not qualified here.

## Musical ownership

`cicadaMidi.createInputRouter` takes synchronous `noteOn`, `noteOff`, `onPress`
and `onRelease` callbacks. A press identity is source/device/channel/input;
the note, selected track, velocity, monotonic note ID and event timestamp are
bound at the admitted press. MIDI runtime identity uses the port ID, not the
port display name. Keyboard codes and gamepad index/ID/button are independent
sources. Recording uses the note ID, so same-pitch inputs do not close another
input's recorded note.

Acid/melodic routing uses last-held priority. Drum ownership is per GM lane,
including aliases. Repeated presses and unmatched/double releases are harmless;
releasing the newest melodic note restores its previous owner. Sustain is
scoped to MIDI port/channel and delays musical release; it does not extend
percussion gates. Repressing a sustained pitch on a newly selected track closes
the old track's ownership before playing the new one.

`router.panic(filter)` releases matching owners and sustain state. Physically
held inputs remain suppressed until a release/neutral observation. Native
WebSocket messages carry optional `noteId`; the receiver namespaces it per
connection, binds it at admission, ignores repeated/unknown messages, and
releases remaining connection owners on teardown. `liveplay.Player.NoteID`
maintains 128 fixed held slots on its audio reader. Its 256-entry input queue
admits at most 128 presses and reserves the remaining capacity for releases.
Regular native note events now use precomputed strings, avoiding the old
per-note `fmt.Sprintf` callback allocation.

Native live notes are never queued for socket reconnection. Blur, pagehide,
hidden documents, backend changes and socket loss clear source ownership.
Native WebSocket teardown also releases owners if the client cannot send.
The native facade preserves local OSS export behavior.

Ownership release is distinct from audible silence. Normal NoteOff follows
voice release/decay and effect tails. It does not establish a resonator's audible
end. The explicit **Silence and reset** action closes native playback; its
browser bridge invokes the qualified controller Panic and detaches output.
This handles pulse/resonator and autonomous graph sources independently of
their NoteOff behavior. New browser playback must use the host's fresh-kernel
initialization workflow. No universal attack/latency offset is added.

## MIDI, gamepad and browser controller

Learned CC mappings now resolve the current score address and live descriptor,
then dispatch bounded values using its linear/log/fader/toggle/enum curve.
Missing/non-live addresses, malformed values and forbidden `off` values fail
before dispatch. CC64 controls sustain; CC120/123 release that input channel.
Note-learn scene/pattern actions are edge-triggered. Existing display-name
mapping persistence remains compatible; runtime ownership remains port-scoped.
Pitch bend and channel/poly aftertouch show an unsupported message in this
current-main Studio slice. They are not silently advertised as implemented.

The opt-in standard Gamepad API prototype has .65/.35 trigger/button hysteresis,
a .12 right-stick deadzone and neutral-before-play on first observation or
reconnect. Face buttons play C3, Eb3, G3 and C4. Left/right shoulder buttons
launch the selected authored track pattern/scene. These are authored pattern
triggers, not fabricated polyphonic chords. Right trigger and right-stick X can
be explicitly assigned to validated live parameters. Disconnection releases
that gamepad. Enablement does not pair devices or use generic Bluetooth GATT.

PR #109 at `dcd315cb31b9524d88963bf966335d7498086eab` owns qualified browser
note/control routing. Read its
[contract](https://github.com/odvcencio/cicada/blob/dcd315cb31b9524d88963bf966335d7498086eab/docs/demo-integration.md)
and `host/browsercontrol/control.go`. This lane does **not** add another browser
note encoder or infer routing from native API metadata. Instead,
`cicadaMidi.createBrowserPerformanceSink` takes wrappers:

```js
const sink = cicadaMidi.createBrowserPerformanceSink({
  down: (token, track, note, velocity, repeat) => controllerDown(token, track, note, velocity, repeat),
  up: token => controllerUp(token),
  setParam: (address, value) => controllerSetParam(address, value), // null -> Go nil
  panic: () => controllerPanic(),
  disconnect: () => node.disconnect(),
  getCatalog: () => currentValidatedInMemoryRegistry
});
```

Wrappers return an empty string/undefined on success, a nonempty error string
or false on rejection, or throw. Tokens are compact and bounded for PR #109's
128-character check. The adapter calls its existing Down/Up/SetParam/Panic;
it neither encodes commands, starts transport nor exposes `KernelResetReady`.
`ErrResetRequired` propagates. A failed panic detaches the worklet. Only the host
may acknowledge a matching fresh-image `t:'t'` while stopped, or a new node's
`t:'r'`; a Stop receipt is insufficient. Preserve serialized/coalesced resets.

Legacy Studio can bind this sink with
`window.cicadaPerformance.setBrowserController(sink)`. Without that binding,
Browser live inputs fail explicitly and never fall through to a native socket.
The GoSX owner should use the router/dispatchers directly and its own in-memory
catalog and common policy dispatcher. Do not load native Studio's fetch-based
orchestration in the public demo. Actual migrated-app/browser qualification is
still required; the bridge tests here use a synthetic controller contract.

PR #106 (`9b9c55d...`, v13/capability bit 1) and #107 (`769b854...`, experimental
guitar/v15 on the old expressive branch) were inspected as unmerged lanes.
Neither is imported. PR #105 standalone expressive research is not Studio live
routing. Frozen-v15/chord compatibility must be qualified by those owners.

## Microphone and phone capture

The existing recorder already journals PCM through OPFS or IndexedDB and has
native retained-take recovery. This slice reuses it.

Browser **Arm** requests a microphone only after the user's control action,
with mono/stereo layout and processing-off requests. **Refresh audio inputs**
uses `enumerateDevices` without opening a permission stream. Labels may be
hidden until permission. An explicit input uses `deviceId: {exact: ...}`;
if unavailable it fails rather than choosing another microphone. Once granted,
the effective settings/device ID are reported and monitored. No controller
brand or USB/Bluetooth microphone support is hardcoded: a controller mic is
usable only if the OS/browser exposes it as an audio input. Gamepad recognition
is separate evidence and does not establish microphone exposure.

**Stop and retain locally** and **Recover last take** do not publish or upload
browser PCM. A recovered take can audition locally. **Commit to project** is a
separate explicit import to the selected current audio track/scene and revision.
Import errors/conflicts retain the browser data for retry. Native **Stop and
save** keeps the existing local project-journal behavior. Public-demo capture
policy and any upload allowance remain separate owners' decisions.

Track end/mute, selected-device loss, hidden page, pagehide, audio-context interruption
and acknowledged playback interruption stop accompaniment/input and retain an
incomplete take. A permission result arriving after canceled/hidden arming is
immediately released without admitting a worker or recording. A Stop before
Play's acknowledgement still queues a forced Stop. Record always waits for
`stageCurrentScore('', true)`'s fresh image receipt before Play; do not replace
that forced staging with a same-revision shortcut. Navigation may terminate the
worker before finalization; recovery keeps only committed journal blocks and
reports an incomplete take. Timing remains uncalibrated/unavailable.

## Acceptance matrix

| Surface | Implemented/verified in this checkout | Remaining acceptance |
|---|---|---|
| Native acid/GM drums | Note IDs, overlapping client/source ownership, restoration, idempotent/pitch-aware releases, reserved release capacity; native tests pass | Studio WebSocket integration tests/CI are blocked locally by a dependency download; physical listening/device output not tested |
| Native input callback | Fixed held arrays; zero allocations/run measured over 100 prepared input/render runs | Max-burst CPU/deadline and hardware soak |
| WASM/kernel ABI | No image/opcode/parameter/worklet asset changes | No local TinyGo installed; no new actual-WASM acceptance claimed |
| Browser acid/graph/drums | Adapter contract for exact PR #109 API and reset barrier tested synthetically | PR #109 and GoSX owner must wire/requalify actual migrated app; current legacy Browser live is unavailable until binding |
| WebMIDI | Synthetic notes/velocity, GM aliases, CC curves/dispatch, sustain, CC120/123, learn edges, hotplug and focus/socket loss | Real controllers, browser permission refusal, Bluetooth/USB delivery and latency |
| Standard gamepad | Synthetic notes, pattern/scene actions, expression mapping, deadzone, hysteresis, initial-held/disconnect/reconnect suppression | Real OS-paired USB/Bluetooth pads and browser polling/latency |
| PS5/controller microphone | OS audio-input enumeration/exact-selection and synthetic loss | USB and Bluetooth exposure must be verified separately on each OS/browser; no DualSense support claim |
| Phone/local browser PCM | OPFS/IDB existing recovery; local Stop/Recover vs explicit import, permission-delay/interruption tests; zero typed allocations across 10,000 existing capture callbacks | iPhone Safari/Android real microphone, foreground/background/call interruption, storage quota, audible output and responsive UI |
| Explicit silence | Native close action/bridge output detachment are distinct from gate release; synthetic bridge assertions | Actual application/voice-family silence and reinitialization receipts, especially resonators; acoustic tails are not inferred from note duration |
| Hosted public demo | No hosted files/policy/deploy changes | Demo owner keeps export/capture policy separate; no deployment requested or performed |

## Reproduce and blockers

```sh
npm --prefix cmd/cicada test
GOWORK=off go test ./host/liveplay ./host/capture ./kernel/engine ./kernel/cmd -count=1
GOWORK=off go test -race ./host/liveplay -count=1
GOWORK=off go vet ./host/liveplay ./host/capture ./kernel/engine ./kernel/cmd
```

Use Go 1.25.0+; in this cloud environment it is
`/workspace/.cloud-setup/go/bin/go` (the `/usr/bin/go` command is not that
compiler). Local Studio and `host/kernelimage` test setup was blocked when the
module proxy's archive for the **pinned** `github.com/odvcencio/gotreesitter@v0.54.0`
returned HTTP Forbidden. No pin changes, replacements or alternate download
routes were used. TinyGo was not present. Full repository checks, WASM build,
actual Studio/browser acceptance and hardware qualification are review/CI gates.
No merge, device pairing, permission grant, real recording, recording upload,
credential creation, listener/security change or deployment was performed.
