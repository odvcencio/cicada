FROM golang:1.25.1 AS go-toolchain

FROM tinygo/tinygo:0.41.1 AS builder
USER root
# Replace the image's Go distribution so all artifacts use this toolchain.
RUN rm -rf /usr/local/go
COPY --from=go-toolchain /usr/local/go /usr/local/go
ENV GOWORK=off GOTOOLCHAIN=local CGO_ENABLED=0 GOFLAGS="-p=2 -buildvcs=false"
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG REVISION
RUN test "${#REVISION}" -eq 40 && \
    ! printf '%s' "$REVISION" | grep -q '[^0-9a-f]'
RUN mkdir -p build/demo && \
    tinygo build -p=2 -target=wasm-unknown -opt=z -panic=trap -no-debug \
      -gc=leaking -scheduler=none -o build/demo/kernel.wasm ./cmd/cicada-kernel-wasm && \
    "$(tinygo env TINYGOROOT)/bin/wasm-opt" -Oz build/demo/kernel.wasm -o build/kernel.optimized.wasm && \
    mv build/kernel.optimized.wasm build/demo/kernel.wasm && \
    go run ./cmd/cicada-wasm-size build/demo/kernel.wasm host/web/processor.min.js
RUN GOOS=js GOARCH=wasm go build -trimpath -o build/demo/demo.wasm ./cmd/cicada-demo-browser && \
    cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" build/demo/wasm_exec.js && \
    go build -trimpath -ldflags "-s -w -X main.revision=$REVISION" -o build/cicada-demo ./cmd/cicada-demo

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /src/build/cicada-demo /cicada-demo
COPY --from=builder /src/build/demo /demo
USER 65532:65532
EXPOSE 8170
ENTRYPOINT ["/cicada-demo", "-bundle", "/demo", "-listen", ":8170"]
