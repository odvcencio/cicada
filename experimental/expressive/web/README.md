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

## Playback limits

The bridge batches 1024 mono float samples per callback. It uses the deprecated main-thread ScriptProcessor API deliberately for this local audition page; it is not an AudioWorklet or a production integration. Callback time is 21.3 ms at 48 kHz, plus platform buffering. Main-thread work and Go garbage collection can cause dropouts. Controls and DSP run serially on that same thread. Rendering does not establish polyphonic capacity, browser portability, artifact-free interaction, tuning accuracy, or acoustic realism.

Use a real browser with audio output to assess sound quality: enable audio, audition all three models, sweep controls while holding notes, check note releases and focus loss, and listen for dropouts under normal UI activity. A successful Go/WASM build or headless bridge execution cannot establish acoustic realism.
