# Cicada Studio desktop (Windows, macOS)

`cicada-studio` is Cicada Studio as a desktop app. It starts `cicada studio`
as a helper process, so the native audio engine keeps running in Go,
and shows the Studio page in a window hosted by the GoSX desktop runtime
(Microsoft Edge WebView2 on Windows, WKWebView on macOS).

What the app adds over `cicada studio` in a browser:

- File menu: New Project, Open, Save As, and Open Recent.
- One window: opening a `.cicada` file while the app runs switches the
  running window to that file.
- The helper process ends on normal quit, SIGTERM, and SIGINT, including
  while it is still starting. Windows also stops it after a host crash;
  macOS does not guarantee cleanup after a crash or SIGKILL.
- Windows: an installer (`Cicada-Studio-Setup-<version>.exe`) and a portable
  ZIP. An installed copy adds itself to Explorer's "Open with" list for
  `.cicada` files (per user, under `HKCU\Software\Classes`).
- macOS: an ad-hoc signed `Cicada Studio.app` bundle (`dev.cicada.studio`),
  ready to drop in `~/Applications` or `/Applications`.

Settings and logs (`logs/studio.log`) live in `%LOCALAPPDATA%\Cicada Studio`
on Windows and `~/Library/Application Support/Cicada Studio` on macOS. The
WebView2 profile is Windows-only; WKWebView keeps its website data in the
default store keyed by the bundle identifier. New projects go to
`Documents\Cicada` or `~/Documents/Cicada`.

Live saves private note checkpoints and finished performances in the desktop
profile, scoped to the score's path. After a restart, use **Review saved
performance** to open a copy without changing the score. **Previous note
performances** also retains applied and dismissed takes. Recovery uses the last
successful checkpoint; an interrupted held note has an estimated end. Keeping
the profile and score path preserves this history.

macOS uses CoreAudio for native playback and microphone recording, with input,
output, and buffer selection in Studio's Audio settings. The first recording
asks for microphone access; playback and device discovery do not. If access is
denied, enable Cicada Studio under System Settings → Privacy & Security →
Microphone, then try recording again. Run the packaged app for recording so
the capture helper uses its microphone usage description and app identity.
Oto remains available for playback through the system default output.

The Mac File menu uses native Open and Save panels. Finder can open `.cicada`
scores in the running window; Open Recent returns to your previous projects.
Command-N creates a project, Command-O opens one, Command-Shift-S saves a copy
with its audio assets, Command-R reloads Studio, and Command-W closes the window.
The Edit menu supports native Cut, Copy, Paste, and Select All. Command-Z and
Command-Shift-Z use song history outside text fields and typing history inside
them. The application menu retains the standard Command-Q and Command-H actions.

## Build

Windows (development):

```sh
make build-workstation-release
(cd workstation && GOOS=windows GOARCH=amd64 go build -o ../build/cicada-workstation.exe .)
GOOS=windows GOARCH=amd64 go build -o build/cicada.exe ./cmd/cicada
(cd desktop && GOOS=windows GOARCH=amd64 go build -ldflags=-H=windowsgui -o ../build/cicada-studio.exe .)
```

macOS (development):

```sh
GOWORK=off make build-workstation-release
(cd workstation && GOWORK=off CGO_ENABLED=0 go build -o ../build/cicada-workstation .)
GOWORK=off CGO_ENABLED=0 go build -o build/cicada ./cmd/cicada
(cd desktop && GOWORK=off CGO_ENABLED=0 go build -o ../build/cicada-studio .)
```

`cicada-studio.exe` looks for `cicada.exe` next to itself, then on `PATH`;
`--cicada <path>` overrides both. It also needs `cicada-workstation.exe`, the
`workstation/` runtime bundle, and `WebView2Loader.dll` beside it.

On macOS, the equivalent executables are `cicada-studio`, `cicada`, and
`cicada-workstation`. The `workstation/` runtime bundle sits next to them in
the `.app` bundle at `Contents/MacOS/workstation/`. No WebView loader is
needed; WKWebView ships with the OS.

## Package

### Windows

```sh
desktop/package.sh --version 0.1.0 \
  --public-key <base64 Ed25519 public key> \
  --manifest-key <Ed25519 private key file>
```

The script downloads the WebView2 SDK (pinned by SHA-256) for
`WebView2Loader.dll`, builds the core, GoSX workstation, and desktop
executables, and runs `gosx desktop package`. Output goes to `dist/desktop`.

### macOS

Install [TinyGo](https://tinygo.org/getting-started/install/macos/) and
[Binaryen](https://github.com/WebAssembly/binaryen/releases) (`wasm-opt`) on
`PATH` for the production runtime build; CI uses TinyGo 0.41.1. `WASMOPT` can
select a `wasm-opt` executable outside `PATH`. Go, `codesign`, and `plutil`
must also be available.

```sh
desktop/package.sh --target darwin --version 0.1.0
```

Builds the three executables and the GoSX runtime, assembles `Cicada
Studio.app` with the standard macOS layout, writes an `Info.plist`
(`CFBundleIdentifier=dev.cicada.studio`, `LSMinimumSystemVersion=12.0`), and
ad-hoc signs the bundle. Output goes to `dist/macos`.
The bundle declares the `.cicada` score type and explains microphone access to
macOS. Both the UI host and its bundled audio helper read that same metadata.

The bundle is self-contained and builds from a clean checkout. The desktop
host pins the published GoSX Darwin backend at
[`088e1fe1f18d`](https://github.com/jvcencio/gosx/commit/088e1fe1f18d02b2ca8bccf59f4baf3cfec6fab5)
through a versioned replacement in `desktop/go.mod`. Go downloads and verifies
it using `desktop/go.sum`; no local GoSX checkout or `go.work` is needed. This
replacement can be removed once an upstream GoSX release includes the backend.

Packaging sets `GOWORK=off` for every executable and the runtime, so a local
workspace cannot override release dependencies. It also checks desktop backend
availability before building the bundle. The workstation runtime retains its
own matching GoSX pin, including the SVG namespace fix for dynamic note charts.
An existing output app is replaced only after the new bundle passes signature
verification. Packaging reuses the stripped workstation executable from the
production runtime build, avoiding a second compilation.

Audit the built GoSX runtime with its canonical measurement environment:

```sh
mkdir -p build
bash scripts/check-gosx-size.sh --json \
  'dist/macos/Cicada Studio.app/Contents/MacOS/workstation/build.json' \
  > build/macos-gosx-size.json
```

The audit builds the published GoSX CLI from `tools/gosx-size` with Go 1.26.0
and Brotli 1.2.1, the exact identities required by its canonical compressor.
The Go toolchain may download on the first run. The isolated module keeps
Cicada's compiler and compression dependencies unchanged. Paths remain relative
to the caller's directory, and the audit works without a local GoSX checkout.
Its module must match the workstation's GoSX pin; `go test ./internal/buildaudit`
checks that match. When updating the SDK, update both module pins and resolve
the audit module with
`GOWORK=off GOTOOLCHAIN=go1.26.0 go -C tools/gosx-size mod tidy`.
The build uses a read-only module graph, and the temporary tool is removed on
success or failure. CI checks the production runtime and final macOS bundle,
retaining both JSON reports, and rejects deliberately corrupted copies of the
raw, gzip, and Brotli representations. Run those integration checks locally
with `CICADA_SIZE_AUDIT_MANIFEST=/absolute/path/to/build.json go test -race ./internal/buildaudit`.
These reports cover GoSX runtime assets; Cicada's
custom UI engine keeps its separate build-time byte limit.

CI packages and launches the app on Apple Silicon and Intel macOS runners.
Run the same native launch and shutdown checks locally with Python 3:

```sh
python3 desktop/smoke.py --app 'dist/macos/Cicada Studio.app' --output build/macos-smoke
python3 desktop/smoke.py --app 'dist/macos/Cicada Studio.app' --output build/macos-smoke/arrange --mode normal --timeout 120 --workflow desktop/testdata/native-arrange-workflow.js
```

The smoke checks use disposable scores and the null audio backend. Reports
and app logs are kept in the output directory. The MIDI shutdown check opens
its input inside the authenticated native window and keeps it open until
SIGTERM, using the same cookie and CSRF checks as normal MIDI input.

The smoke checks copy the app into a temporary directory, assign a unique
bundle identifier, and ad-hoc sign the copy. This isolates WKWebView's store
from an installed or running Cicada app without changing the supplied bundle.
Each case uses disposable scores, its own desktop data directory and
`CICADA_STATE_DIR`, and the null audio backend. The copied app and its unique
WebKit/cache directories are removed afterward. Reports, signing output, and
app logs are kept in the output directory.

The Arrange workflow exercises saved placement shortcuts, contextual looping,
Device sound and mix controls, Automation points, and exact Undo in WKWebView.
It verifies the saved source after navigation and checks that focused editors
keep their own keys. It leaves microphone input off and uses silent null audio.
This longer workflow has a 120-second total budget; individual conditions still
must settle within 15 seconds. Other smoke cases retain their 60-second budget.

Development builds are ad-hoc signed (no Developer ID, no notarization), so
Gatekeeper may ask the first time: right-click the bundle → **Open**.

## Options

| Flag | Meaning |
|---|---|
| `--audio tymbal\|oto\|null` | Audio backend passed to `cicada studio` |
| `--cicada <path>` | Use this `cicada.exe` |
| `--data-dir <dir>` | Settings, logs, and WebView2 profile folder |
| `--devtools` | Enable the WebView2 developer tools (F12) |
| `--mute` | Mute the page (tests) |
| `--smoke-out <file>` | Write a startup report and exit once Studio loads (tests) |
| `--smoke-midi` | With `--smoke-out`, keep native MIDI input open for shutdown testing |

Linux: run `cicada studio` and open the printed address in a browser. The
GoSX desktop runtime supports Windows and macOS natively; Linux stays on the
browser path for now.
