.PHONY: build-worklets test grammar test-kernel test-golden test-alloc test-timing grammar-check probe-wasm build build-kernel-wasm build-loudness-wasm test-kernel-wasm test-loudness build-phrase-wasm test-phrase-wasm test-midi-virtual test-wasm test-browser test-browser-soak budget-size budget-browser release-cpu-report engine-metrics

export GOWORK := off

.PHONY: build-core build-workstation build-workstation-release test-workstation

# Keep a TinyGo/Binaryen regression from consuming the full CI job budget.
KERNEL_WASM_BUILD_TIMEOUT ?= 180s
# Size optimization keeps the richer bounded voice engine within its existing
# download budget. Audio parity and browser callback budgets gate this build.
KERNEL_WASM_OPT ?= s

# Redirect measurements outside the checkout; see docs/manual/engine-metrics.md.
ENGINE_METRICS_ARGS ?=

test:
	go test ./... -count=1

engine-metrics:
	GOMAXPROCS=1 go run ./cmd/cicada-engine-metrics $(ENGINE_METRICS_ARGS)

grammar:
	go generate ./notation

grammar-check:
	mkdir -p build
	go run ./cmd/cicada-grammar -bin build/cicada.bin -sample examples/first-acid.cicada > build/grammar-doctor.txt
	cmp build/cicada.bin notation/cicada.bin
	go test ./language/... -count=1

build: build-core build-workstation

build-core:
	mkdir -p build
	GOFLAGS=-buildvcs=false go build -o build/cicada ./cmd/cicada

# The GoSX app has its own Go 1.26 module; the realtime TinyGo core stays 1.25.
build-workstation:
	cd workstation && go run -mod=mod m31labs.dev/gosx/cmd/gosx build .
	mkdir -p build/workstation
	cp workstation/dist/server/app build/cicada-workstation.new
	mv -f build/cicada-workstation.new build/cicada-workstation
	cp workstation/dist/build.json build/workstation/build.json
	cp -R workstation/dist/assets workstation/dist/public build/workstation/

build-workstation-release:
	cd workstation && go run -mod=mod m31labs.dev/gosx/cmd/gosx build --prod .
	mkdir -p build/workstation
	cp workstation/dist/server/app build/cicada-workstation.new
	mv -f build/cicada-workstation.new build/cicada-workstation
	cp workstation/dist/build.json build/workstation/build.json
	cp -R workstation/dist/assets workstation/dist/public build/workstation/

test-workstation:
	cd workstation && go generate ./... && go test -race ./... -count=1

.PHONY: test-studio-continuity
# Requires a running Studio with the marked disposable browser-test score.
test-studio-continuity:
	node --test workstation/browser/continuity.test.cjs

test-kernel:
	go test ./kernel/... -count=1

test-alloc:
	go test ./kernel/... -run 'Test.*(Allocate|Allocs|AllocationFree)' -count=1 -v

test-timing:
	go test ./kernel/seq ./kernel/engine -run 'Test.*(Clock|Timing|Tick|Tempo|Quantize|Gate|Slide|Chain|Mask|Block|Swing|Ratchet|Tie|Probability|Restart|Scene)' -count=1 -v

test-golden:
	go run ./cmd/cicada golden

test-midi-virtual: build-core
	GOWORK=off PULSE_SERVER=unix:/nonexistent node ./cmd/cicada/test-midi-virtual.cjs

# This builds only the sequencer probe, not the eventual audio kernel.
probe-wasm:
	mkdir -p build
	GOFLAGS=-buildvcs=false tinygo build -target=wasm-unknown -opt=2 -panic=trap -no-debug -o build/cicada-seq.wasm ./cmd/cicada-seq-wasm

build-kernel-wasm:
	mkdir -p build
	@timeout --kill-after=5s $(KERNEL_WASM_BUILD_TIMEOUT) env GOFLAGS=-buildvcs=false tinygo build -target=wasm-unknown -opt=$(KERNEL_WASM_OPT) -panic=trap -no-debug -gc=leaking -scheduler=none -o build/cicada-kernel.wasm ./cmd/cicada-kernel-wasm || { \
		status=$$?; \
		if [ $$status -eq 124 ] || [ $$status -eq 137 ]; then \
			echo "FAIL build-kernel-wasm: TinyGo kernel build exceeded $(KERNEL_WASM_BUILD_TIMEOUT) budget" >&2; \
		fi; \
		exit $$status; \
	}
	go run ./cmd/cicada-wasm-size build/cicada-kernel.wasm

build-loudness-wasm:
	mkdir -p build
	GOWORK=off GOFLAGS=-buildvcs=false tinygo build -target=wasm-unknown -opt=2 -panic=trap -no-debug -gc=leaking -scheduler=none -o build/cicada-loudness.wasm ./cmd/cicada-loudness-wasm

test-kernel-wasm: build-kernel-wasm build-loudness-wasm
	go test -timeout=20m -tags wasm_integration ./cmd/cicada-kernel-wasm -run '^TestAudioWASM' -count=1
	go test -timeout=3m -tags stream_wasm ./kernel/stream -run '^TestStreamNativeWASMParity$$' -count=1 -v

test-loudness: build-loudness-wasm
	GOWORK=off go test ./kernel/loudness -count=1 -v

test-wasm: build-kernel-wasm
	bash -o pipefail -c "GOWORK=off nice -n 10 go test -timeout=20m -tags wasm_integration ./cmd/cicada-kernel-wasm -run '^TestAudioWASM' -count=1 -v | tee build/test-wasm.log"

test-browser: build-kernel-wasm
	mkdir -p build
	bash cmd/cicada/browser-runner.sh browser '^TestBrowser(Parity|StudioFlow|CaptureTargets|CaptureFault|UnderrunDetector|ProcessorAllocations|StepEditQueueRegression)$$' 5m build/test-browser.log

budget-size: build-kernel-wasm
	bash -o pipefail -c "go run ./cmd/cicada-wasm-size build/cicada-kernel.wasm host/web/processor.min.js | tee build/budget-size-report.txt"

budget-browser: build-kernel-wasm
	mkdir -p build
	bash -o pipefail -c "GOWORK=off nice -n 10 go test -tags browser ./cmd/cicada -run '^TestBrowserCPUReport$$' -count=1 -timeout=5m -v | tee build/budget-browser.log"

# Release gate: run this from WSL with Windows Chrome before any release that changes the kernel or
# the AudioWorklet processor, and keep build/release-cpu-report.log with the release record.
# It records the real p99 against the 0.67 ms budget that the Node fallback in CI does not gate.
release-cpu-report: build-kernel-wasm
	mkdir -p build
	CICADA_BROWSER=windows bash -o pipefail -c "GOWORK=off go test -tags browser ./cmd/cicada -run '^TestBrowserCPUReport$$' -count=1 -timeout=5m -v | tee build/release-cpu-report.log"
	grep -q 'Windows Chrome CPU budget' build/release-cpu-report.log

test-browser-soak: build-kernel-wasm budget-browser
	bash cmd/cicada/browser-runner.sh browser_soak '^TestBrowserSoak$$' 40m build/browser-soak.log

build-phrase-wasm:
	mkdir -p build
	GOFLAGS=-buildvcs=false tinygo build -target=wasm-unknown -opt=2 -panic=trap -no-debug -gc=conservative -scheduler=none -o build/cicada-phrase.wasm ./cmd/cicada-phrase-wasm

test-phrase-wasm: build-phrase-wasm
	go test -tags wasm_integration ./cmd/cicada-phrase-wasm -run '^TestPhraseWASMParity$$' -count=1

# One source, two profiles: the core asset keeps its existing 5 KiB gate.
build-worklets:
	npm exec --yes --package=terser@5.39.0 -- terser host/web/processor.js --define CICADA_CAPTURE=false --ecma 2020 -c passes=5,unsafe=true -m toplevel -o host/web/processor.min.js
	npm exec --yes --package=terser@5.39.0 -- terser host/web/processor.js --define CICADA_CAPTURE=true --ecma 2020 -c passes=5,unsafe=true -m toplevel -o host/web/processor-capture.min.js
