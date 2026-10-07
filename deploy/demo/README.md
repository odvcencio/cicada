# Host the browser demo

Build the server and its browser assets together from a clean checkout:

```sh
docker build -f deploy/demo/Dockerfile \
  --build-arg REVISION="$(git rev-parse HEAD)" -t cicada-demo .
docker run --rm --read-only --cap-drop=ALL \
  --security-opt=no-new-privileges -p 127.0.0.1:8170:8170 \
  cicada-demo -host 127.0.0.1:8170
```

The build uses TinyGo 0.41.1 and Go 1.25.1, including the matching
`wasm_exec.js`. The final image runs as a nonroot user without a shell.
It needs no writable directories. Rebuild after each source change; the
server requires a full commit ID and returns it in `X-Cicada-Revision`.

For HTTPS hosting, omit the loopback `-host` argument and put a reverse proxy
in front of port 8170. Preserve the host specified
by `PublicHost` in [the handler](../../host/demoweb/handler.go); other public
hosts receive 403. To host on a different domain, change that constant before
building. For a local HTTP preview, pass an explicit loopback host and port
using the server's `-host` flag.

The server compresses text and WASM assets with gzip or Brotli and uses
`Cache-Control: no-cache` with ETags. Let the proxy preserve those headers,
`Vary`, and the app's content security policy. AudioWorklet requires HTTPS
outside loopback. Keep a private preview behind authentication and `noindex`
until its sketches and target browsers have been checked.
