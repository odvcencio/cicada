.PHONY: test test-kernel grammar-check probe-wasm build

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

# This builds only the sequencer probe, not the eventual audio kernel.
probe-wasm:
	mkdir -p build
	GOFLAGS=-buildvcs=false tinygo build -target=wasm-unknown -opt=2 -panic=trap -no-debug -o build/cicada-seq.wasm ./cmd/cicada-seq-wasm
