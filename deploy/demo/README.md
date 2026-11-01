# Host the browser demo

Build the server and its browser assets together from a clean checkout:

```sh
docker build -f deploy/demo/Dockerfile \
  --build-arg REVISION="$(git rev-parse HEAD)" -t cicada-demo .
docker run --rm --read-only --cap-drop=ALL \
  --security-opt=no-new-privileges -p 127.0.0.1:8170:8170 \
  cicada-demo -host 127.0.0.1:8170
```

The build uses TinyGo 0.41.1 and Go 1.25.6, including the matching
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

For the M31 cluster, build from the committed tree so the revision header
identifies the actual source, then push the image and apply the manifest:

```sh
revision=$(git rev-parse HEAD)
image="harbor.draco.quest/orchard/cicada-demo:$revision"
git archive "$revision" | docker build -f deploy/demo/Dockerfile \
  --build-arg REVISION="$revision" -t "$image" -
docker push "$image"
sed "s|CICADA_IMAGE|$image|" deploy/demo/kubernetes.yaml | kubectl apply -f -
kubectl rollout status -n m31labs deployment/cicada-demo --timeout=180s
```

The manifest creates a public ingress at `cicada.m31labs.dev`. Validate a
private preview and the browser checks before applying it. The cluster needs
its existing `regcred` image pull secret and `cloudflare-issuer` certificate
issuer. HTTP probes set the public Host header required by the demo server.
