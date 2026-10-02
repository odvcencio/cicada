# Local live audition

From the repository root, using Go 1.25 or later:

```sh
GOOS=js GOARCH=wasm go build -o experimental/expressive/web/expressive.wasm ./cmd/cicada-expressive-wasm
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" experimental/expressive/web/wasm_exec.js
python3 -m http.server 8080 --bind 127.0.0.1 --directory experimental/expressive/web
```

Open http://localhost:8080 and choose **Enable audio**. The `.wasm` and `wasm_exec.js` files are generated build artifacts; do not commit them. Use the same Go installation to compile and copy its runtime support file.

Cloud workspace compiler, if Go is not on PATH:

```sh
export PATH=/workspace/.cloud-setup/go/bin:$PATH
export GOCACHE=/tmp/cicada-go-cache
export GOMODCACHE=/tmp/cicada-go-mod
```

Use the keyboard A W S E D F T G Y H U J K, or hold onscreen note buttons. This is one voice, with last-held-note priority. Releasing the final key releases the excitation; switching models resets the voice. Continuous sliders control pitch bend, pressure, excitation position, brightness, vibrato, damping, and guitar amp drive. Listening level is a separate smoothed browser gain. The browser sample rate is supplied to the model constructors. No samples, external IRs, or CDN requests are used.

## Acceptance limits

The bridge batches 1024 mono float samples per callback. It uses the deprecated main-thread ScriptProcessor API deliberately for this isolated research page; it is not an AudioWorklet or a production integration. Callback time is 21.3 ms at 48 kHz, plus platform buffering. Main-thread work and Go garbage collection can cause dropouts. Controls and DSP run serially on that same thread. Rendering does not establish polyphonic capacity, browser portability, artifact-free interaction, tuning accuracy, or acoustic realism.

Manual acceptance still requires a real browser with audio output: enable audio, audition all three models, sweep controls while holding notes, check note releases and focus loss, and listen for dropouts under normal UI activity. A successful Go/WASM build or headless bridge execution cannot establish listening acceptance. No browser listening acceptance is claimed by this page or these instructions.

## Reproducible headless browser check

With Node, Playwright, and Chromium available, from the repository root:

```sh
GOOS=js GOARCH=wasm go build -o /tmp/cicada-expressive.wasm ./cmd/cicada-expressive-wasm
WASM_PATH=/tmp/cicada-expressive.wasm \
WASM_EXEC_PATH="$(go env GOROOT)/lib/wasm/wasm_exec.js" \
CHROMIUM=/usr/bin/chromium \
node experimental/expressive/web/check-browser.cjs
```

The script starts and closes its own loopback HTTP server. It uses headless Chromium with `--no-sandbox` for this managed container and synthetic audio output. It observes real ScriptProcessor callbacks while sustaining each voice and sweeping all synthesis sliders, verifies nonzero finite samples, checks keyboard release, and checks that switching a held instrument resets to silence. It fails on JavaScript page errors. `browser-check.txt` records the 2026-10-02 workspace run (Chromium 151.0.7922.173, 44.1 kHz). Block counts and energy vary with scheduling. This test does not measure actual audible output, audio-device latency, callback deadline misses, perceptual realism, or support in other browsers.
