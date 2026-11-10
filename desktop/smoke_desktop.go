//go:build windows || darwin

package main

import (
	"fmt"
	"net/url"
)

// socketObserverScript runs at document start in every page the smoke window
// loads, before any page script. It wraps WebSocket to count sockets and
// frames, and records the revision of each shared-score state frame in
// window.__cicadaSocketObserver. It never injects a frame or changes a
// handler, the traffic, or the constructor's behavior. The workflow script
// runs after the page loads, which is too late: a socket that connected 0.5 s
// after load never reached its proxy.
const socketObserverScript = `(() => {
  if (window.top !== window || window.__cicadaSocketObserver) return;
  const NativeWebSocket = window.WebSocket;
  const observer = window.__cicadaSocketObserver = {sockets: 0, scoreSockets: 0, frames: 0, snapshots: []};
  window.WebSocket = new Proxy(NativeWebSocket, {
    construct(target, args, newTarget) {
      const socket = Reflect.construct(target, args, newTarget);
      observer.sockets++;
      let score = false;
      try {
        const address = new URL(String(args[0]), location.href);
        score = address.host === location.host && address.pathname === '/collaboration/score';
      } catch { /* A malformed address throws in the constructor above. */ }
      if (score) {
        observer.scoreSockets++;
        socket.addEventListener('message', event => {
          observer.frames++;
          try {
            const message = JSON.parse(event.data);
            if (message.event === 'state' && message.data?.document && typeof message.data.revision === 'string')
              observer.snapshots.push({revision: message.data.revision});
          } catch { /* Non-state frames are outside this snapshot assertion. */ }
        });
      }
      return socket;
    },
  });
})();`

// Run inside the actual owner window, using its normal cookie and CSRF checks.
// The server's connection log confirms that the native subscription is held.
// macOS does not yet transport messages through the GoSX desktop bridge.
func midiSmokeScript(startup string) string {
	address, err := url.Parse(startup)
	if err != nil {
		return ""
	}
	origin := address.Scheme + "://" + address.Host
	return fmt.Sprintf(`(async () => {
  if (location.origin !== %q || window.__cicadaSmokeMIDI) return;
  window.__cicadaSmokeMIDI = true;
  const controller = new AbortController();
  const deadline = setTimeout(() => controller.abort(), 10000);
  try {
    const page = await fetch("/?panel=live", {signal: controller.signal});
    if (!page.ok) throw new Error("Live page returned " + page.status);
    const doc = new DOMParser().parseFromString(await page.text(), "text/html");
    const token = doc.querySelector('input[name="csrf_token"]')?.value;
    if (!token) throw new Error("Live page has no CSRF token");
    const response = await fetch("/api/midi/input", {
      method: "POST", body: "{}", signal: controller.signal,
      headers: {"Content-Type": "application/json", "X-CSRF-Token": token}
    });
    if (!response.ok) throw new Error("MIDI input returned " + response.status);
    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let text = "";
    while (!text.includes("\n")) {
      const chunk = await reader.read();
      if (chunk.done) throw new Error("MIDI input ended before its device snapshot");
      text += decoder.decode(chunk.value, {stream: true});
      if (text.length > 1048576) throw new Error("MIDI device snapshot exceeds its size limit");
    }
    const event = JSON.parse(text.split("\n")[0]);
    if (event.type !== "devices") throw new Error("MIDI input did not send a device snapshot");
    // Keep the native input subscription open until the test signals shutdown.
    window.__cicadaSmokeMIDI = {reader, controller};
    clearTimeout(deadline);
  } catch (error) {
    clearTimeout(deadline);
    controller.abort();
    console.error("Native MIDI smoke input: " + String(error));
  }
})();`, origin)
}
