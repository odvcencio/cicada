# Use: make -f tools/audio/mix.mk mix-wasm mix-wasm-test
export GOWORK := off
.PHONY: mix-wasm mix-wasm-test
mix-wasm:
	mkdir -p build
	GOFLAGS=-buildvcs=false tinygo build -target=wasm-unknown -opt=2 -panic=trap -no-debug -gc=leaking -scheduler=none -o build/cicada-mix.wasm ./cmd/cicada-mix-wasm
mix-wasm-test: mix-wasm
	go test -tags wasm_integration ./cmd/cicada-mix-wasm -count=1
	node tools/audio/mix-cpu.mjs build/cicada-mix.wasm
