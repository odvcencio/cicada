# Use: make -f tools/audio/convolution.mk convolution-wasm convolution-wasm-test
export GOWORK := off
.PHONY: convolution-wasm convolution-wasm-test
convolution-wasm:
	mkdir -p build
	GOFLAGS=-buildvcs=false tinygo build -target=wasm-unknown -opt=2 -panic=trap -no-debug -gc=leaking -scheduler=none -o build/cicada-convolution.wasm ./cmd/cicada-convolution-wasm
convolution-wasm-test: convolution-wasm
	go test -tags wasm_integration ./cmd/cicada-convolution-wasm -count=1
