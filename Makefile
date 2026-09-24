.PHONY: test test-kernel test-golden test-alloc test-timing grammar-check probe-wasm build build-kernel-wasm test-kernel-wasm

test:
	go test ./... -count=1

grammar-check:
	mkdir -p build
	go run github.com/odvcencio/gotreesitter/cmd/grammargen doctor -grammar language/cicada.grammar -sample examples/first-acid.cicada > build/grammar-doctor.txt
	go run github.com/odvcencio/gotreesitter/cmd/grammargen emit -grammar language/cicada.grammar -bin build/cicada.bin
	cmp build/cicada.bin notation/cicada.bin
	go run github.com/odvcencio/gotreesitter/cmd/grammargen emit -grammar language/cicada.grammar -highlight > build/highlights.scm
	cmp build/highlights.scm language/highlights.scm

build:
	mkdir -p build
	GOFLAGS=-buildvcs=false go build -o build/cicada ./cmd/cicada

test-kernel:
	go test ./kernel/... -count=1

test-alloc:
	go test ./kernel/... -run 'Test.*(Allocate|Allocs|AllocationFree)' -count=1 -v

test-timing:
	go test ./kernel/seq ./kernel/engine -run 'Test.*(Clock|Timing|Tick|Tempo|Quantize|Gate|Slide|Chain|Mask|Block|Swing|Ratchet|Tie|Probability|Restart|Scene)' -count=1 -v

test-golden:
	go run ./cmd/cicada golden

# This builds only the sequencer probe, not the eventual audio kernel.
probe-wasm:
	mkdir -p build
	GOFLAGS=-buildvcs=false tinygo build -target=wasm-unknown -opt=2 -panic=trap -no-debug -o build/cicada-seq.wasm ./cmd/cicada-seq-wasm

build-kernel-wasm:
	mkdir -p build
	GOFLAGS=-buildvcs=false tinygo build -target=wasm-unknown -opt=2 -panic=trap -no-debug -gc=leaking -scheduler=none -o build/cicada-kernel.wasm ./cmd/cicada-kernel-wasm
	go run ./cmd/cicada-wasm-size build/cicada-kernel.wasm

test-kernel-wasm: build-kernel-wasm
	go test -tags wasm_integration ./cmd/cicada-kernel-wasm -run '^TestAudioWASM' -count=1
