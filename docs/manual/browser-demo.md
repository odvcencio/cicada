# Play Cicada in a browser

The browser demo plays the TinyGo kernel in an AudioWorklet. Its instrument
sketches include nine modeled instruments, an authored synth, acid bass, and
a modeled drum kit. The page shows callback p99, underruns, WASM memory, and
the messages drained from the kernel while it plays.

Build and preview with Go, TinyGo, and Node 22 or newer:

```sh
GOWORK=off make build-demo-browser
./build/cicada-demo
```

Open `http://127.0.0.1:8170` and press **Play in browser**. Choose an
instrument sketch or edit the score and press **Apply score**. While playing,
the validated score is staged and takes effect at the next bar; invalid edits
leave the playing score alone. Undo and Redo restore validated scores; Reset
clears the edit history.
Reloading starts a new session and restores the last applied score and editor
draft from this browser, including drafts that have not yet been applied.
Undo and Redo history does not survive a reload. The sketches target about
−20 LUFS, and Volume starts at 75%.

For a short first score, choose **Marimba**. Change a note in `pattern phrase`
or change the tempo, then apply the score. In the editor, Ctrl+Enter (or
Command+Enter on macOS) also applies the score.

The demo plays the core kernel, which has no keyboard patches. A score that
names one, such as the `fm_bass` and `poly_keys` tracks in the starter that
`cicada new` writes, is refused with a message; the piano, the modeled
instruments, authored synths and `acid` play.

Score parsing and compilation run in a separate Go/WASM module on the page;
the audio kernel runs on the worklet thread. These are separate downloads.
The score tools load once per visit. Drafts are stored locally in the browser;
the page reports when storage is unavailable. **Open score** reads a UTF-8
score of up to 128 KiB from your device into the editor, and **Download score**
saves the current editor text, including unapplied edits. Neither sends a score
to the server. External score imports, instrument assets, and audio export are
unavailable.
Stop, focus loss, a hidden tab, and AudioContext suspension stop the transport.
Press Play to resume suspended audio and finish loading the fresh kernel image.

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
The demo report also separates callback-duration and callback-gap threshold
exceedances after warmup. Both can count the same callback; they are
diagnostic counters rather than independent counts of audible dropouts.
Its p99 display has 0.25 ms histogram buckets; browsers without a high
resolution worklet clock have 1 ms timing resolution. The separate
`budget-browser` gate remains responsible for the 0.67 ms CPU budget.

Set `CICADA_DEMO_EVIDENCE_DIR` to retain screenshots outside the checkout and
`CICADA_DEMO_REPORT` to select the soak JSON destination. CI saves both
browser soak reports and screenshots as artifacts.
