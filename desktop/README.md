# Cicada Studio desktop (Windows)

`cicada-studio.exe` is Cicada Studio as a desktop app. It starts
`cicada studio` as a helper process, so the native Tymbal audio engine keeps
running in Go, and shows the Studio page in a window hosted by the GoSX
desktop runtime (Microsoft Edge WebView2).

What the app adds over `cicada studio` in a browser:

- File menu: New Project, Open, Save As, and Open Recent.
- One window: opening a `.cicada` file while the app runs switches the
  running window to that file.
- The helper process ends with the app, including after a crash.
- An installer (`Cicada-Studio-Setup-<version>.exe`) and a portable ZIP. An
  installed copy adds itself to Explorer's "Open with" list for `.cicada`
  files (per user, under `HKCU\Software\Classes`).

Settings, logs (`logs\studio.log`), and the WebView2 profile live in
`%LOCALAPPDATA%\Cicada Studio`. New projects go to `Documents\Cicada`.

## Build

```sh
# Development build next to a cicada.exe:
GOOS=windows GOARCH=amd64 go build -o build/cicada.exe ./cmd/cicada
(cd desktop && GOOS=windows GOARCH=amd64 go build -ldflags=-H=windowsgui -o ../build/cicada-studio.exe .)
```

`cicada-studio.exe` looks for `cicada.exe` next to itself, then on `PATH`;
`--cicada <path>` overrides both. It also needs `WebView2Loader.dll` beside it.

## Package

```sh
desktop/package.sh --version 0.1.0 \
  --public-key <base64 Ed25519 public key> \
  --manifest-key <Ed25519 private key file>
```

The script downloads the WebView2 SDK (pinned by SHA-256) for
`WebView2Loader.dll`, builds both executables, and runs
`gosx desktop package`. Output goes to `dist/desktop`.

## Options

| Flag | Meaning |
|---|---|
| `--audio tymbal\|oto\|null` | Audio backend passed to `cicada studio` |
| `--cicada <path>` | Use this `cicada.exe` |
| `--data-dir <dir>` | Settings, logs, and WebView2 profile folder |
| `--devtools` | Enable the WebView2 developer tools (F12) |
| `--mute` | Mute the page (tests) |
| `--smoke-out <file>` | Write a startup report and exit once Studio loads (tests) |

Linux and macOS: run `cicada studio` and open the printed address in a browser.
The GoSX desktop runtime supports Windows only today.
