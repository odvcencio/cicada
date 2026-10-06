# Play Cicada in a browser

The browser demo plays the TinyGo kernel in an AudioWorklet. Its instrument
sketches include acid bass, an authored synth, nine modeled instruments, and
a modeled drum kit. The page shows callback p99, underruns, WASM memory, and
the messages drained from the kernel while it plays.

Build and preview with Go, TinyGo, and Node 22 or newer:

```sh
GOWORK=off make build-demo-browser
./build/cicada-demo
```

Open `http://127.0.0.1:8170` and press **Play in browser**. Choose an
instrument sketch or edit the score and press **Apply score**. Applying a
score stops playback and initializes a fresh kernel before Play is enabled
again. Undo and Redo restore validated scores; Reset clears the edit history.
Reloading starts a new session. Volume starts at 15%.

Score parsing and compilation run in a separate Go/WASM module on the page;
the audio kernel runs on the worklet thread. These are separate downloads.
The score tools load once per visit. Scores stay in the tab: the demo does
not upload, save, or export them. External file imports are unavailable.
Stop, focus loss, a hidden tab, and AudioContext suspension stop the transport.

For public hosting, serve the executable behind HTTPS with the public demo
host preserved in the HTTP Host header:

```sh
./build/cicada-demo -listen :8170
```

The server admits only its fixed page/runtime assets and rejects native,
agent, and export APIs. It uses the public host already defined by the demo
policy; local previews accept the exact loopback listener address. Run
`make build-demo-browser` again after changing source to update both the
embedded page and the source revision returned in response headers.

Run the browser checks with Chrome for Testing:

```sh
CHROME_BIN=chrome-headless-shell make test-demo-browser
CHROME_BIN=chrome-headless-shell make test-browser-soak
```

The scheduled CI soak uses an isolated runner for the Studio edit checks and the public demo
for 30 minutes after a 60-second warmup. Both must pass. The demo report
requires zero additional underruns and faults, advancing playback, received
messages, and stable kernel memory. Callback timing uses the existing
worklet quantum, clock allowance, and AudioContext latency thresholds.
Its p99 display has 0.25 ms histogram buckets; browsers without a high
resolution worklet clock have 1 ms timing resolution. The separate
`budget-browser` gate remains responsible for the 0.67 ms CPU budget.

Set `CICADA_DEMO_EVIDENCE_DIR` to retain screenshots outside the checkout and
`CICADA_DEMO_REPORT` to select the soak JSON destination. CI saves both
browser soak reports and screenshots as artifacts.
