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

macOS currently plays through Oto using the system default output. This backend
does not support native microphone recording or selecting audio devices yet.

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

The bundle is self-contained and builds from a clean checkout. The desktop
host pins the published GoSX Darwin backend at
[`efb6f4a234d8`](https://github.com/jvcencio/gosx/commit/efb6f4a234d89ec2f48e3ac360dd641f5de932ee)
through a versioned replacement in `desktop/go.mod`. Go downloads and verifies
it using `desktop/go.sum`; no local GoSX checkout or `go.work` is needed. This
replacement can be removed once an upstream GoSX release includes the backend.

Packaging sets `GOWORK=off` for every executable and the runtime, so a local
workspace cannot override release dependencies. It also checks desktop backend
availability before building the bundle. The workstation runtime retains its
own pinned GoSX version.
An existing output app is replaced only after the new bundle passes signature
verification. Packaging reuses the stripped workstation executable from the
production runtime build, avoiding a second compilation.

CI packages and launches the app on Apple Silicon and Intel macOS runners.
Run the same native launch and shutdown checks locally with Python 3:

```sh
python3 desktop/smoke.py --app 'dist/macos/Cicada Studio.app' --output build/macos-smoke
```

The smoke checks use disposable scores and the null audio backend. Reports
and app logs are kept in the output directory.

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

Linux: run `cicada studio` and open the printed address in a browser. The
GoSX desktop runtime supports Windows and macOS natively; Linux stays on the
browser path for now.
