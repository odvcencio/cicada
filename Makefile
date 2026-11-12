.PHONY: test-gosx-worklet test-director build-worklets test grammar test-kernel test-golden test-alloc test-timing grammar-check probe-wasm build build-kernel-wasm build-loudness-wasm test-kernel-wasm test-parity test-loudness build-phrase-wasm test-phrase-wasm test-midi-virtual test-wasm test-browser test-browser-soak budget-size budget-browser release-cpu-report engine-metrics test-engine-measurements test-chord-wasm test-worklet-negotiation
.PHONY: build-keys-wasm test-keys-wasm build-vocals-wasm build-modular-wasm build-keys-modular-wasm test-tinygo-digests

export GOWORK := off

# The official SDK stays in build output and is copied into exported plugins.
.PHONY: build-wam2 test-wam2
build-wam2: build-kernel-wasm build-vocals-wasm
	npm install --prefix build/wam2-sdk --ignore-scripts --no-package-lock --no-save @webaudiomodules/sdk@0.0.12

test-wam2: build-wam2
	node --test host/wam2/processor.test.cjs host/wam2/message-drain.test.cjs
	go test ./host/wam2 ./cmd/cicada -run WAM2 -count=1
	go test -tags browser ./cmd/cicada -run '^TestBrowserWAM2$$' -count=1 -timeout=3m -v

.PHONY: test-chord-wasm
.PHONY: build-core build-workstation build-workstation-release test-workstation

# Keep a TinyGo/Binaryen regression from consuming the full CI job budget.
KERNEL_WASM_BUILD_TIMEOUT ?= 180s
# Keep the core's standard library identical across host compilers and runners.
KERNEL_WASM_GO_VERSION ?= go1.25.6
# Size optimization keeps the richer bounded voice engine within its existing
# download budget. Audio parity and browser callback budgets gate this build.
# A second Binaryen invocation folds code after the initial optimization.
KERNEL_WASM_OPT ?= z
KERNEL_WASM_BINARYEN_FLAGS ?= -Oz --converge --strip-producers --strip-target-features

# Binaryen may be installed separately from TinyGo. The desktop packager
# accepts the same override for an executable outside PATH.
WASMOPT ?=
export WASMOPT

# Bulk-memory copies keep the shared schedule inside the existing size budget.
# Append +simd128 when measuring WebAssembly vector instructions.
KERNEL_WASM_LLVM_FEATURES ?= +bulk-memory,+bulk-memory-opt

# Redirect measurements outside the checkout; see docs/manual/engine-metrics.md.
ENGINE_METRICS_ARGS ?=

test: test-build-tools
	go test ./... -count=1
	node --test cmd/cicada/studio-chord-grid.test.cjs cmd/cicada/browser_cpu_node.test.cjs scripts/demo-browser/soak-timing.test.cjs scripts/demo-browser/server-url.test.cjs cmd/cicada/state-poll.test.cjs cmd/cicada/source-written.test.cjs cmd/cicada/audio-sync-client.test.cjs
	node host/web/chord_capability_test.cjs
	node --test host/web/render-quantum.test.cjs host/web/scene-tempo.test.cjs

.PHONY: test-ci-core test-cmd-shard test-cmd-shard-check

# CI runs ./cmd/cicada in parallel shards (see scripts/cmdshard), so the core
# target covers every other package. It skips TestRenderPathsAgree, which CI
# runs in its own job (make test-render-paths). `make test` still runs everything.
test-ci-core: test-build-tools
	go test $$(go list ./... | grep -v '^m31labs.dev/cicada/cmd/cicada$$') -skip '^TestRenderPathsAgree$$' -count=1
	node --test cmd/cicada/studio-chord-grid.test.cjs cmd/cicada/browser_cpu_node.test.cjs scripts/demo-browser/soak-timing.test.cjs scripts/demo-browser/server-url.test.cjs cmd/cicada/state-poll.test.cjs cmd/cicada/source-written.test.cjs cmd/cicada/audio-sync-client.test.cjs
	node host/web/chord_capability_test.cjs
	node --test host/web/render-quantum.test.cjs host/web/scene-tempo.test.cjs

.PHONY: test-build-tools
test-build-tools:
	python3 -B -m unittest discover -s scripts -p 'build_tools_test.py'

# SHARD=k/N runs shard k of N; SHARDS=N checks that N shards cover every test once.
SHARD ?= 1/3
SHARDS ?= 3
test-cmd-shard:
	go run ./scripts/cmdshard run $(SHARD)

test-cmd-shard-check:
	go run ./scripts/cmdshard check $(SHARDS)

.PHONY: test-audio-ideation test-editor-transcription test-record-worklet

test-audio-ideation:
	go test ./host/transcription -count=1 -v

test-editor-transcription:
	go test ./cmd/cicada -run 'Test.*Transcri' -count=1
	node --test cmd/cicada/studio-transcribe.test.cjs
	cd workstation && go test ./... -run 'Test.*Transcri' -count=1

test-record-worklet:
	node --test host/web/capture.test.cjs cmd/cicada/studio-capture.test.cjs cmd/cicada/studio-transcribe.test.cjs

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

# Times an agent's loop with build/cicada on the reference scores and checks
# the speed gates in cmd/cicada-bench/gates.go; BENCH_ARGS=--enforce fails on
# a gate. The large song's render and audition cover its first 8 bars.
.PHONY: bench-agent-loop test-render-identity
BENCH_ARGS ?=
bench-agent-loop: build-core
	go run ./cmd/cicada-bench --cicada build/cicada --large-bars 8 --json build/bench-agent-loop.json $(BENCH_ARGS)

# Renders every example score with and without the stored mix (render.MasterCache)
# and compares the bytes: plain, normalized, and at -14 and -9 LUFS.
test-render-identity:
	CICADA_RENDER_IDENTITY_SWEEP=1 go test ./cmd/cicada -run '^TestRenderCacheIdentity$$' -count=1 -timeout=110m -v

# Regenerates the committed agent front door (llms.txt, AGENTS.md's generated
# region, the quick start the binary embeds, examples/first-song/AGENTS.md),
# whose only inputs are the quick start and stable text, and builds
# llms-full.txt from the manual, which is not committed.
.PHONY: agent-front-door
agent-front-door:
	CICADA_UPDATE_AGENT_FRONT_DOOR=1 CICADA_LLMS_FULL_OUT=$(CURDIR)/llms-full.txt go test ./internal/agentguide -run '^(TestAgentFrontDoorIsCurrent|TestLLMsFullFitsItsBudget)$$' -count=1

# The two render paths (the direct mixer and the scheduled engine) must play the
# same notes and write the same float32 bytes at 44.1 kHz (TestRenderPathsAgree;
# CI job "M0 render paths agree"). The audio target compares whole songs at
# 48 kHz and only reports (nightly).
.PHONY: test-render-paths test-render-paths-audio
test-render-paths:
	go test ./render -run '^TestRenderPathsAgree$$' -count=1 -v

test-render-paths-audio:
	CICADA_PATHS_AUDIO=full go test ./render -run '^TestRenderPathsAudio$$' -count=1 -timeout=60m -v

# The agent task suite (internal/agentsuite) drives build/cicada through one
# scripted task that reads only JSON: help, intents, symbols, check, apply,
# render to a loudness target and audition. It must finish within 2 minutes.
# It starts by following the quick start that cicada agents prints, and runs the
# examples of docs/manual/agents.md and docs/manual/agent-quickstart.md too.
.PHONY: test-agent-surface
test-agent-surface: build-core
	CICADA_BIN=$(CURDIR)/build/cicada CICADA_AGENT_SUITE=1 go test ./internal/agentsuite -run '^(TestAgentTaskSuite|TestAgentDocumentationExamples)$$' -count=1 -v

# The GoSX app has its own Go 1.26 module; the realtime TinyGo core stays 1.25.
build-workstation:
	cd workstation && go run -mod=mod m31labs.dev/gosx/cmd/gosx build .
	mkdir -p build/workstation
	cp workstation/dist/server/app build/cicada-workstation.new
	mv -f build/cicada-workstation.new build/cicada-workstation
	cp workstation/dist/build.json build/workstation/build.json
	rm -rf build/workstation/assets build/workstation/public
	cp -R workstation/dist/assets workstation/dist/public build/workstation/

build-workstation-release:
	cd workstation && go run -mod=mod m31labs.dev/gosx/cmd/gosx build --prod .
	mkdir -p build/workstation
	cp workstation/dist/server/app build/cicada-workstation.new
	mv -f build/cicada-workstation.new build/cicada-workstation
	cp workstation/dist/build.json build/workstation/build.json
	rm -rf build/workstation/assets build/workstation/public
	cp -R workstation/dist/assets workstation/dist/public build/workstation/

test-workstation:
	cd workstation && go generate ./... && go test -race ./... -count=1

.PHONY: engine-size-report
# What fills the UI engine WebAssembly, by section, runtime table, package and
# module, with the linker's reason for each large package and two floor
# programs. Writes workstation/build/engine-size.json; see workstation/README.md.
ENGINE_SIZE_ARGS ?=
engine-size-report:
	cd workstation && go run ./internal/enginesize -o build/engine-size.json $(ENGINE_SIZE_ARGS)

.PHONY: test-collaboration
test-collaboration:
	cd workstation && GOWORK=off go test -race ./collab ./ -run 'Test(Concurrent|Undo|Transactional|SeededOffline|Collaboration)' -count=1 -v

.PHONY: test-studio-continuity
# Requires a running Studio with the marked disposable browser-test score.
test-studio-continuity:
	node --test workstation/browser/continuity.test.cjs

.PHONY: test-studio-latency
# Requires a fresh Studio (the benchmark claims its one-use owner session) on a
# disposable fixture from continuity.test.cjs or placements.test.cjs, with
# CICADA_STUDIO_URL and CICADA_STUDIO_STARTUP_LOG set. Report mode by default;
# CICADA_BENCH_GATES=enforce fails on a missed gate. Also reads
# CICADA_BENCH_VIEWPORT, CICADA_BENCH_PLAYBACK, CICADA_BENCH_WARMUPS and CICADA_BENCH_TRIALS.
CICADA_BENCH_REPORT ?= build/studio-latency.json
export CICADA_BENCH_REPORT
test-studio-latency:
	mkdir -p $(dir $(CICADA_BENCH_REPORT))
	CICADA_BENCH_GATES=$${CICADA_BENCH_GATES:-report} node workstation/browser/edit-undo-benchmark.cjs > /dev/null

.PHONY: test-studio-modulation-source
# Requires a fresh Studio process, disposable score, and proof directory.
test-studio-modulation-source:
	node --test workstation/browser/modulation-source.test.cjs

.PHONY: test-studio-chorus
# Requires a fresh Studio process, disposable score, and proof directory.
test-studio-chorus:
	node --test workstation/browser/chorus.test.cjs

.PHONY: test-studio-pattern-machine
# Requires a fresh Studio process, disposable score, and proof directory.
test-studio-pattern-machine:
	node --test workstation/browser/pattern-machine.test.cjs

.PHONY: test-studio-shell
# Requires a fresh Studio process with a disposable score; this suite edits the score.
test-studio-shell:
	node --test workstation/browser/shell.test.cjs

.PHONY: test-studio-mixer-strips
# Requires a fresh Studio process with a disposable score; this suite mutes and unmutes a track.
test-studio-mixer-strips:
	node --test workstation/browser/mixer-strips.test.cjs

.PHONY: test-studio-edits
# Requires a fresh Studio process with a disposable continuity score; this suite
# draws notes, undoes and redoes, and refuses a stale write.
test-studio-edits:
	node --test workstation/browser/studio-edits.test.cjs

.PHONY: test-studio-notes
# Requires a fresh Studio process on note-editor.test.cjs --write-fixture; this suite quantizes and undoes.
test-studio-notes:
	node --test workstation/browser/note-editor.test.cjs

.PHONY: test-studio-columns
# Requires a fresh Studio process and an absolute CICADA_COLUMN_PROOF_DIR.
test-studio-columns:
	node --test workstation/browser/columns.test.cjs

.PHONY: test-studio-first-song
# Requires an untouched disposable first-light project and CICADA_SONG_PROOF_DIR.
test-studio-first-song:
	node --test workstation/browser/first-song.test.cjs

.PHONY: test-studio-export-sources
# Requires a disposable examples/studio-project copy and CICADA_EXPORT_PROOF_DIR.
test-studio-export-sources:
	node --test workstation/browser/export-sources.test.cjs

.PHONY: test-studio-export-stems
# Requires a disposable examples/studio-project copy and CICADA_STEMS_PROOF_DIR.
test-studio-export-stems:
	node --test workstation/browser/export-stems.test.cjs

.PHONY: test-studio-portable-song
# Requires a generated portable-song fixture and CICADA_PORTABLE_PROOF_DIR.
test-studio-portable-song:
	node --test workstation/browser/portable-song.test.cjs

.PHONY: test-studio-code-recovery
# Requires a disposable studio-project copy; restart it for each recovery phase.
test-studio-code-recovery:
	node --test workstation/browser/code-recovery.test.cjs

.PHONY: test-studio-placements
test-studio-placements:
	node --test workstation/browser/placements.test.cjs

.PHONY: test-studio-collaboration
test-studio-collaboration:
	node --test workstation/browser/collaboration.test.cjs

.PHONY: test-studio-dock-fit
# Requires a fresh Studio on a continuity.test.cjs fixture and an absolute CICADA_DOCK_PROOF_DIR.
test-studio-dock-fit:
	node --test workstation/browser/dock-fit.test.cjs

.PHONY: test-studio-meter-performance
# Requires a fresh Studio and CICADA_METERS_URL; it checks for no repaint of unchanged meters and for a poll gap of at most 100 ms (page clock).
test-studio-meter-performance:
	node --test workstation/browser/meter-performance.test.cjs

.PHONY: test-studio-recording-setup
# Requires a fresh Studio on recording-setup.test.cjs --write-fixture, CICADA_SETUP_URL and CICADA_VOCAL_PROOF_DIR.
test-studio-recording-setup:
	node --test workstation/browser/recording-setup.test.cjs

.PHONY: test-studio-recording-levels
# Requires a fresh Studio on recording-levels.test.cjs --write-fixture, CICADA_LEVELS_URL and CICADA_LEVELS_PROOF_DIR.
test-studio-recording-levels:
	node --test workstation/browser/recording-levels.test.cjs

.PHONY: test-studio-vocals
# Requires a fresh Studio on the print-sources fixture (--primary-arrangement) and CICADA_VOCAL_PROOF_DIR.
test-studio-vocals:
	node --test workstation/browser/vocals.test.cjs

.PHONY: test-studio-vocal-levels
# Requires a fresh Studio on the print-sources fixture, CICADA_LEVELING_URL, CICADA_LEVELING_SCORE and CICADA_LEVELING_PROOF_DIR.
test-studio-vocal-levels:
	node --test workstation/browser/vocal-levels.test.cjs

.PHONY: test-studio-shared-capture
# Requires a fresh Studio on a continuity.test.cjs fixture and CICADA_SHARED_CAPTURE_PROOF_DIR;
# run it once for MPE and once with CICADA_CAPTURE_KIND=channel, each on a fresh Studio.
test-studio-shared-capture:
	node --test workstation/browser/shared-capture.test.cjs

.PHONY: test-studio-performance
# Requires a fresh Studio on performance.test.cjs --write-fixture and CICADA_PERFORMANCE_PROOF_DIR.
# Run once with CICADA_PERFORMANCE_PART=capacity (CICADA_CAPACITY_URL) and once with =buffer (CICADA_BUFFER_URL), each on a fresh Studio.
test-studio-performance:
	node --test workstation/browser/performance.test.cjs

.PHONY: test-studio-scene-launch-keys
# Requires a fresh Studio on a continuity.test.cjs fixture.
test-studio-scene-launch-keys:
	node --test workstation/browser/scene-launch-keys.test.cjs

.PHONY: test-studio-live-write
# Requires Studio with a synthetic microphone: build the harness (go test -c ./cmd/cicada), start
# TestSyntheticStudioUIHarness on live-fixture.cjs --no-curve, and put cicada-workstation in front of it.
# Set CICADA_LIVE_WRITE_SCORE and CICADA_LIVE_WRITE_PROOF_DIR. scripts/ci-studio-browser.sh live_write shows how.
test-studio-live-write:
	node --test workstation/browser/live-write.test.cjs

.PHONY: test-studio-live-write-uncertain
# As test-studio-live-write, on a fresh Studio per CICADA_LIVE_WRITE_FAILURE (reject, lost, timeout, late).
test-studio-live-write-uncertain:
	node --test workstation/browser/live-write-uncertain.test.cjs

.PHONY: test-studio-amp-automation
# Requires a fresh Studio on amp-automation.test.cjs --write-fixture DIR (score DIR/main.cicada).
# Set CICADA_AMP_AUTOMATION_FIXTURE to that score and CICADA_AMP_AUTOMATION_PROOF_DIR.
test-studio-amp-automation:
	node --test workstation/browser/amp-automation.test.cjs

test-kernel:
	go test ./kernel/... -count=1

test-alloc:
	go test ./kernel/... -run 'Test.*(Allocate|Allocs|AllocationFree)' -count=1 -v

test-timing:
	go test ./kernel/seq ./kernel/engine -run 'Test.*(Clock|Timing|Tick|Tempo|Quantize|Gate|Slide|Chain|Mask|Block|Swing|Ratchet|Tie|Probability|Restart|Scene)' -count=1 -v
	CICADA_WALLCLOCK_TIMING=1 go test ./kernel/amp -run '^TestAmpP99BlockBudget$$' -count=1 -v

test-golden:
	go run ./cmd/cicada golden

# Renders every example score with --jobs 1 and with 2, 4, 8 and the automatic
# job count, plain and at -14 LUFS, and compares the bytes (render lanes).
.PHONY: test-parallel-identity
test-parallel-identity:
	CICADA_PARALLEL_IDENTITY_SWEEP=1 go test ./cmd/cicada -run '^TestParallelRenderIdentity$$' -count=1 -timeout=110m -v

# Runs the render lanes under the race detector with the lanestress hook. At
# each hand-off point of the lane protocol the hook sleeps 1 ms (five times a
# lane's spin window) at about 1 in 32 hand-offs and yields at about 1 in 4 of
# the rest, chosen by a hash of the seed and the hand-off (site, segment,
# lane, chunk), with no state shared between goroutines. Each test binary
# prints its seed; CICADA_LANE_STRESS_SEED=<seed> gives every hand-off the same
# delay again. It runs the forced-lane tests and the identity subset with every
# segment on every lane, once per GOMAXPROCS value in LANE_STRESS_PROCS ("all"
# is every CPU), so the job counts 4 and 8 exceed the processors.
# LANE_STRESS_COUNT repeats the quick sets (renderlanes, render) and
# LANE_STRESS_HEAVY_COUNT the slow ones (the engine tests, the identity subset).
# The nightly CI job runs 10 and 10 at 2, 3 and all; pull requests that touch
# the lanes run LANE_STRESS_PROCS=2 LANE_STRESS_COUNT=3 LANE_STRESS_HEAVY_COUNT=1.
LANE_STRESS_PROCS ?= 2 3 all
LANE_STRESS_COUNT ?= 10
LANE_STRESS_HEAVY_COUNT ?= 10
.PHONY: test-lane-stress
test-lane-stress:
	@set -e; for procs in $(LANE_STRESS_PROCS); do \
		if [ "$$procs" = all ]; then run="env -u GOMAXPROCS"; else run="env GOMAXPROCS=$$procs"; fi; \
		echo "== lane stress: GOMAXPROCS $$procs, -count=$(LANE_STRESS_COUNT), heavy -count=$(LANE_STRESS_HEAVY_COUNT)"; \
		$$run go test -race -tags lanestress ./internal/renderlanes -run '^Test(RunRendersEveryTrackOnce|LightSegmentsStaySerial|PanicInALaneReachesTheCaller|PanicInTheCallersLane|BudgetLimitsConcurrentPools|LaneCountChangesBetweenSegments|ForcedLanesRenderEachSegmentOnce)$$' -count=$(LANE_STRESS_COUNT) -timeout=30m -v; \
		$$run go test -race -tags lanestress ./render -run '^Test(DirectLanes|AudioClipLanes)' -count=$(LANE_STRESS_COUNT) -timeout=30m -v; \
		$$run go test -race -tags lanestress ./kernel/engine -run '^TestOfflineParallel' -count=$(LANE_STRESS_HEAVY_COUNT) -timeout=60m -v; \
		$$run CICADA_PARALLEL_IDENTITY_STRESS=1 go test -race -tags lanestress ./cmd/cicada -run '^TestParallelRenderIdentity$$' -count=$(LANE_STRESS_HEAVY_COUNT) -timeout=100m -v; \
	done

test-midi-virtual: build-core
	GOWORK=off PULSE_SERVER=unix:/nonexistent node ./cmd/cicada/test-midi-virtual.cjs

.PHONY: test-mpe-virtual
test-mpe-virtual: build
	GOWORK=off PULSE_SERVER=unix:/nonexistent node ./cmd/cicada/test-mpe-virtual.cjs

# This builds only the sequencer probe, not the eventual audio kernel.
probe-wasm:
	mkdir -p build
	GOFLAGS=-buildvcs=false tinygo build -target=wasm-unknown -opt=2 -panic=trap -no-debug -o build/cicada-seq.wasm ./cmd/cicada-seq-wasm

build-kernel-wasm:
	mkdir -p build
	@python3 scripts/run-with-timeout.py --kill-after=5s $(KERNEL_WASM_BUILD_TIMEOUT) env GOFLAGS=-buildvcs=false GOTOOLCHAIN=$(KERNEL_WASM_GO_VERSION) sh -c '\
		tinygo version && sh scripts/wasm-opt.sh --version && \
		tinygo build -tags cicada_reactor -target=wasm-unknown $(if $(KERNEL_WASM_LLVM_FEATURES),-llvm-features=$(KERNEL_WASM_LLVM_FEATURES)) -opt=$(KERNEL_WASM_OPT) -panic=trap -no-debug -gc=leaking -scheduler=none -o build/cicada-kernel.wasm ./cmd/cicada-kernel-wasm && \
		sh scripts/wasm-opt.sh $(KERNEL_WASM_BINARYEN_FLAGS) build/cicada-kernel.wasm -o build/cicada-kernel.optimized.wasm && \
		sh scripts/wasm-opt.sh --enable-bulk-memory --enable-nontrapping-float-to-int $(if $(findstring +simd128,$(KERNEL_WASM_LLVM_FEATURES)),--enable-simd) --code-folding -Oz --converge build/cicada-kernel.optimized.wasm -o build/cicada-kernel.wasm' || { \
		status=$$?; \
		if [ $$status -eq 124 ] || [ $$status -eq 137 ]; then \
			echo "FAIL build-kernel-wasm: TinyGo kernel build exceeded $(KERNEL_WASM_BUILD_TIMEOUT) budget" >&2; \
		fi; \
		exit $$status; \
	}
	go run ./cmd/cicada-wasm-size build/cicada-kernel.wasm

# Optional full track-processing core. Automatically selected by project images;
# its added bytes are reported separately from the default kernel budget.
build-vocals-wasm:
	mkdir -p build
	@python3 scripts/run-with-timeout.py --kill-after=5s $(KERNEL_WASM_BUILD_TIMEOUT) env GOFLAGS=-buildvcs=false sh -c '\
		tinygo build -tags trackfx,cicada_reactor -target=wasm-unknown $(if $(KERNEL_WASM_LLVM_FEATURES),-llvm-features=$(KERNEL_WASM_LLVM_FEATURES)) -opt=$(KERNEL_WASM_OPT) -panic=trap -no-debug -gc=leaking -scheduler=none -o build/cicada-vocals.wasm ./cmd/cicada-kernel-wasm && \
		sh scripts/wasm-opt.sh -Oz build/cicada-vocals.wasm -o build/cicada-vocals.optimized.wasm && \
		mv build/cicada-vocals.optimized.wasm build/cicada-vocals.wasm'
	go run ./cmd/cicada-wasm-size --report-only build/cicada-vocals.wasm

# Optional modulator module: the vocals module's tags plus the detail layer
# (spec 24). Images with header flag bit 1 need it; the default core refuses them.
build-modular-wasm:
	mkdir -p build
	@python3 scripts/run-with-timeout.py --kill-after=5s $(KERNEL_WASM_BUILD_TIMEOUT) env GOFLAGS=-buildvcs=false sh -c '\
		tinygo build -tags modular,trackfx,cicada_reactor -target=wasm-unknown $(if $(KERNEL_WASM_LLVM_FEATURES),-llvm-features=$(KERNEL_WASM_LLVM_FEATURES)) -opt=$(KERNEL_WASM_OPT) -panic=trap -no-debug -gc=leaking -scheduler=none -o build/cicada-modular.wasm ./cmd/cicada-kernel-wasm && \
		sh scripts/wasm-opt.sh -Oz build/cicada-modular.wasm -o build/cicada-modular.optimized.wasm && \
		mv build/cicada-modular.optimized.wasm build/cicada-modular.wasm'
	go run ./cmd/cicada-wasm-size --report-only build/cicada-modular.wasm

# Modulated scores that also contain keyboard voices need both optional sets.
build-keys-modular-wasm:
	mkdir -p build
	@python3 scripts/run-with-timeout.py --kill-after=5s $(KERNEL_WASM_BUILD_TIMEOUT) env GOFLAGS=-buildvcs=false GOTOOLCHAIN=$(KERNEL_WASM_GO_VERSION) sh -c '\
		tinygo build -tags keys,modular,trackfx,cicada_reactor -target=wasm-unknown $(if $(KERNEL_WASM_LLVM_FEATURES),-llvm-features=$(KERNEL_WASM_LLVM_FEATURES)) -opt=$(KERNEL_WASM_OPT) -panic=trap -no-debug -gc=leaking -scheduler=none -o build/cicada-keys-modular.wasm ./cmd/cicada-kernel-wasm && \
		sh scripts/wasm-opt.sh -Oz build/cicada-keys-modular.wasm -o build/cicada-keys-modular.optimized.wasm && \
		mv build/cicada-keys-modular.optimized.wasm build/cicada-keys-modular.wasm'
	go run ./cmd/cicada-wasm-size --keys-modular build/cicada-keys-modular.wasm

build-loudness-wasm:
	mkdir -p build
	GOWORK=off GOFLAGS=-buildvcs=false tinygo build -target=wasm-unknown -opt=2 -panic=trap -no-debug -gc=leaking -scheduler=none -o build/cicada-loudness.wasm ./cmd/cicada-loudness-wasm

# Optional keyboard module; same ABI and AudioWorklet, loaded only for keys.
build-keys-wasm:
	mkdir -p build
	GOWORK=off GOFLAGS=-buildvcs=false tinygo build -p=2 -tags 'keys trackfx cicada_reactor' -target=wasm-unknown $(if $(KERNEL_WASM_LLVM_FEATURES),-llvm-features=$(KERNEL_WASM_LLVM_FEATURES)) -opt=z -panic=trap -no-debug -gc=leaking -scheduler=none -o build/cicada-keys.wasm ./cmd/cicada-kernel-wasm
	sh scripts/wasm-opt.sh -Oz build/cicada-keys.wasm -o build/cicada-keys.optimized.wasm
	mv build/cicada-keys.optimized.wasm build/cicada-keys.wasm
	go run ./cmd/cicada-wasm-size --report-only build/cicada-keys.wasm
	wc -c build/cicada-keys.wasm
	test $$(wc -c < build/cicada-keys.wasm) -le 524288

test-keys-wasm: build-kernel-wasm build-keys-wasm
	CICADA_KEYS_CORE_WASM=$(CURDIR)/build/cicada-kernel.wasm GOWORK=off go test -p=2 -tags keys_wasm ./host/kernelimage -run '^TestKeysCoreWASM' -count=1 -v
	CICADA_WASM_PATH=$(CURDIR)/build/cicada-keys.wasm GOWORK=off go test -p=2 -timeout=20m -tags 'wasm_integration keys' ./cmd/cicada-kernel-wasm -run '^TestAudioWASMKeys' -count=1 -v

test-kernel-wasm: build-kernel-wasm build-vocals-wasm build-loudness-wasm
	CICADA_CHORD_WASM_PATH=$(CURDIR)/build/cicada-kernel.wasm go test -timeout=3m -tags chord_wasm ./cmd/cicada-kernel-wasm -count=1
	go test -timeout=20m -tags wasm_integration ./cmd/cicada-kernel-wasm -run '^TestAudioWASM' -count=1
	go test -timeout=3m -tags stream_wasm ./kernel/stream -run '^TestStreamNativeWASMParity$$' -count=1 -v
	go test -timeout=3m -tags modulate_wasm ./kernel/modulate -run '^TestModulateNativeWASMParity$$' -count=1 -v

# The kernel WASM is compiled by TinyGo (LLVM), not by the Go compiler. This
# runs the deterministic-math and processor digests compiled by TinyGo for
# WebAssembly (under Node), so a TinyGo code-generation difference fails here as
# it would on arm64 or with GOAMD64=v3. go test exits 0 when -run matches
# nothing, so every digest must print a PASS line.
TINYGO_DIGEST_PACKAGES = ./kernel/dsp/detmath ./kernel/fx ./kernel/fx/ampcab ./kernel/fx/chorus ./kernel/fx/convolution ./kernel/fx/tape ./kernel/fx/pro ./kernel/mix ./kernel/loudness ./kernel/voice/acid
TINYGO_DIGEST_TESTS = TestIdentityDigest TestSinPiDigest TestAtanDigest TestTanh32Digest TestLog1pDigest TestNearDigest TestFilterCoefficientDigest TestExp2F32Digest \
	TestCompressorDigest TestDeesserDigest TestDelayDigest TestDriveDigest TestReverbDigest TestAmpcabDigest TestAmpcabOutputDigest TestChorusDigest \
	TestConvolutionDigest TestTapeDigest TestLimiterDigest TestTransientDigest TestWidthDigest TestRoomDigest TestChainDigest TestNormalizeDigest \
	TestEQDigestIsIdenticalOnEveryTarget TestMixDigest TestMeterDigestIsIdenticalOnEveryTarget TestAcidDigest
test-tinygo-digests:
	mkdir -p build
	bash -o pipefail -c 'GOWORK=off GOFLAGS=-buildvcs=false tinygo test -target=wasm -v -run Digest $(TINYGO_DIGEST_PACKAGES) | tee build/tinygo-digests.txt'
	@missing=0; for name in $(TINYGO_DIGEST_TESTS); do grep -q -- "^--- PASS: $$name " build/tinygo-digests.txt || { echo "No PASS line for $$name under TinyGo" >&2; missing=1; }; done; exit $$missing

test-chord-wasm: build-kernel-wasm test-worklet-negotiation
	node host/web/chord_capability_test.cjs
	bash -o pipefail -c 'CICADA_CHORD_WASM_PATH="$(CURDIR)/build/cicada-kernel.wasm" go test -timeout=2m -tags chord_wasm ./cmd/cicada-kernel-wasm -run "^TestChordWASM" -count=1 -v | tee build/chord-wasm-report.txt build/test-chord-wasm.log'

test-parity: build-kernel-wasm build-modular-wasm build-keys-modular-wasm
	mkdir -p build/parity
	bash -o pipefail -c 'CICADA_MODULAR_WASM_PATH="$$PWD/build/cicada-modular.wasm" CICADA_PARITY_DIR="$$PWD/build/parity" go test -timeout=20m -tags wasm_integration ./cmd/cicada-kernel-wasm -run "^TestExamplesPCM24Parity$$" -count=1 -v | tee build/parity/report.txt'
	CICADA_MODULAR_WASM_PATH="$$PWD/build/cicada-modular.wasm" go test -timeout=5m -tags wasm_integration ./cmd/cicada-kernel-wasm -run '^TestMappedMacroNativeWASMParity$$' -count=1 -v
	CICADA_MODULAR_WASM_PATH="$$PWD/build/cicada-modular.wasm" go test -timeout=5m -tags wasm_integration ./cmd/cicada-kernel-wasm -run '^TestInstrumentVibratoNativeWASMParity$$' -count=1 -v
	CICADA_MODULAR_WASM_PATH="$$PWD/build/cicada-modular.wasm" go test -timeout=5m -tags wasm_integration ./cmd/cicada-kernel-wasm -run '^TestChorus(NativeWASMParity|InstrumentModulatorNativeWASMParity|GraphLFONativeWASMParity)$$' -count=1 -v
	CICADA_WASM_PATH="$$PWD/build/cicada-kernel.wasm" go test -timeout=5m -tags wasm_integration ./cmd/cicada-kernel-wasm -run '^TestChorusRejectedByCoreKernel$$' -count=1 -v
	CICADA_WASM_PATH="$$PWD/build/cicada-kernel.wasm" go test -timeout=5m -tags wasm_integration ./cmd/cicada-kernel-wasm -run '^TestOscillatorBankReuseWASM$$' -count=1 -v
	CICADA_KEYS_MODULAR_WASM_PATH="$$PWD/build/cicada-keys-modular.wasm" go test -timeout=5m -tags wasm_integration ./cmd/cicada-kernel-wasm -run '^TestKeysChorusNativeWASMParity$$' -count=1 -v
	CICADA_WASM_PATH="$$PWD/build/cicada-kernel.wasm" CICADA_MODULAR_WASM_PATH="$$PWD/build/cicada-modular.wasm" go test -timeout=5m -tags wasm_integration ./cmd/cicada-kernel-wasm -run '^TestInstrumentModulatorNativeWASMParity$$' -count=1 -v
	CICADA_WASM_PATH="$$PWD/build/cicada-kernel.wasm" CICADA_MODULAR_WASM_PATH="$$PWD/build/cicada-modular.wasm" go test -timeout=5m -tags wasm_integration ./cmd/cicada-kernel-wasm -run '^TestGraphLFONativeWASMParity$$' -count=1 -v
	CICADA_KEYS_MODULAR_WASM_PATH="$$PWD/build/cicada-keys-modular.wasm" go test -timeout=5m -tags wasm_integration ./cmd/cicada-kernel-wasm -run '^TestGraphLFOKeysNativeWASMParity$$' -count=1 -v
	CICADA_MODULAR_WASM_PATH="$$PWD/build/cicada-modular.wasm" go test -timeout=5m -tags wasm_integration ./cmd/cicada-kernel-wasm -run '^TestSamplerExpressionNativeWASMParity$$' -count=1 -v
	CICADA_KEYS_MODULAR_WASM_PATH="$$PWD/build/cicada-keys-modular.wasm" go test -timeout=20m -tags wasm_integration ./cmd/cicada-kernel-wasm -run '^TestKeysModularWASMParity$$' -count=1 -v

test-loudness: build-loudness-wasm
	GOWORK=off go test ./kernel/loudness -count=1 -v

# CI splits the audio WASM tests in two jobs by overriding the next three
# variables: one job skips the one-hour memory test and the other runs only it.
# With no override, make test-wasm runs every TestAudioWASM test as before.
TEST_WASM_RUN ?= ^TestAudioWASM
TEST_WASM_SKIP ?=
TEST_WASM_LOG ?= build/test-wasm.log

test-wasm: build-kernel-wasm build-vocals-wasm test-chord-wasm test-wasm-audio

.PHONY: test-wasm-audio
test-wasm-audio: build-kernel-wasm build-vocals-wasm
	mkdir -p build
	bash -o pipefail -c "GOWORK=off nice -n 10 go test -timeout=20m -tags wasm_integration ./cmd/cicada-kernel-wasm -run '$(TEST_WASM_RUN)' $(if $(TEST_WASM_SKIP),-skip '$(TEST_WASM_SKIP)') -count=1 -v | tee $(TEST_WASM_LOG)"

# Required unified image and opcode negotiation gates use the freshly built reactor.
test-worklet-negotiation: build-worklets
	node host/web/chord_capability_test.cjs
	node host/web/client_capability_test.cjs
	node --test host/web/worklet-followups.test.cjs host/web/client-lifecycle.test.cjs host/web/message-drain.test.cjs host/web/keys_client.test.cjs

test-browser: build-kernel-wasm build-modular-wasm
	mkdir -p build
	bash cmd/cicada/browser-runner.sh browser '^TestBrowser(Parity|SpatialParity|RenderSizeHint|StudioFlow|CaptureTargets|CaptureFault|ProcessorAllocations|StepEditQueueRegression|DraftConflict|UnifiedMixedParity|ChordGridIntegration|StudioLibrary|StudioLibraryProcessorAllocations|TripletTakePreviewAndCommit|ScoreRecordQuantizationPreviewAndCommit|ContinuousAutomationLanes|InstrumentRecording|MasterChain)$$' 5m build/test-browser.log
	bash cmd/cicada/browser-runner.sh browser,worklet_testhooks '^TestBrowserUnderrunDetector$$' 2m build/test-worklet-stall.log

# E1 measurement suite: renders every voice family and effect, writes
# build/engine-measurements.json (override with CICADA_ENGINE_MEASUREMENTS),
# and fails when a gated metric is worse than kernel/measure/testdata/tolerances.json.
# Takes about 2 seconds. See kernel/measure/README.md.
test-engine-measurements:
	mkdir -p build
	go test -p 1 -count=1 -timeout=5m -v ./kernel/measure

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

.PHONY: build-demo-browser test-demo-browser
build-demo-browser: build-kernel-wasm build-modular-wasm
	mkdir -p build/demo
	GOOS=js GOARCH=wasm go build -tags 'modular trackfx' -trimpath -o build/demo/demo.wasm ./cmd/cicada-demo-browser
	cp "$$(go env GOROOT)/lib/wasm/wasm_exec.js" build/demo/wasm_exec.js
	cp build/cicada-modular.wasm build/demo/kernel.wasm
	go run ./cmd/cicada-wasm-size --modular build/demo/kernel.wasm
	go build -trimpath -ldflags "-X main.revision=$$(git rev-parse HEAD)" -o build/cicada-demo ./cmd/cicada-demo

test-demo-browser: build-demo-browser
	go test -tags 'modular trackfx' ./host/demobrowser
	node scripts/demo-browser/test.cjs

test-browser-soak: export CICADA_BROWSER_SOAK := 1
test-browser-soak: build-demo-browser budget-browser
	@bash -c 'set +e; bash cmd/cicada/browser-runner.sh browser_soak "^TestBrowserSoak$$" 40m build/browser-soak.log & studio_pid=$$!; node scripts/demo-browser/test.cjs --soak & demo_pid=$$!; wait $$studio_pid; studio_status=$$?; wait $$demo_pid; demo_status=$$?; test $$studio_status -eq 0 && test $$demo_status -eq 0'

build-phrase-wasm:
	mkdir -p build
	GOFLAGS=-buildvcs=false tinygo build -target=wasm-unknown -opt=2 -panic=trap -no-debug -gc=conservative -scheduler=none -o build/cicada-phrase.wasm ./cmd/cicada-phrase-wasm

test-phrase-wasm: build-phrase-wasm
	go test -tags wasm_integration ./cmd/cicada-phrase-wasm -run '^TestPhraseWASMParity$$' -count=1

# One source, two profiles: the core asset keeps its existing 5 KiB gate.
build-worklets:
	npm exec --yes --package=terser@5.39.0 -- terser host/web/processor.js --define CICADA_CAPTURE=false --define CICADA_TEST=false --ecma 2020 -c passes=5,unsafe=true -m toplevel -o host/web/processor.min.js
	npm exec --yes --package=terser@5.39.0 -- terser host/web/processor.js --define CICADA_CAPTURE=true --define CICADA_TEST=false --ecma 2020 -c passes=5,unsafe=true -m toplevel -o host/web/processor-capture.min.js

# Optional sample kernel: one prepared immutable instrument per instance.
# Audio packs stay external; the core kernel keeps its shared size gate.
.PHONY: build-sampler-wasm test-sampler-wasm
build-sampler-wasm:
	mkdir -p build
	@python3 scripts/run-with-timeout.py --kill-after=5s $(KERNEL_WASM_BUILD_TIMEOUT) env GOFLAGS=-buildvcs=false tinygo build -target=wasm-unknown -opt=2 -panic=trap -no-debug -gc=leaking -scheduler=none -o build/cicada-sampler.wasm ./cmd/cicada-sampler-wasm
	@test "$$(wc -c < build/cicada-sampler.wasm)" -le 65536 || { echo 'FAIL sampler kernel exceeds 64 KiB'; exit 1; }
	@wc -c build/cicada-sampler.wasm

test-sampler-wasm: build-sampler-wasm
	go test -tags wasm_integration ./cmd/cicada-sampler-wasm -count=1
	go test -tags sample_wasm ./kernel/voice/sample -run '^TestSampleNativeWASMDeterminism$$' -count=1

# GoSX hosting: the score pack and controller, and sample-exact native/worklet parity.
# GOSX_WORKLET_PROCESSOR is GoSX's game/audio/worklet/processor.js (a checkout of
# m31labs.dev/gosx, or its module cache directory).
test-gosx-worklet: build-kernel-wasm
	@test -n "$(GOSX_WORKLET_PROCESSOR)" || { echo "set GOSX_WORKLET_PROCESSOR to GoSX's game/audio/worklet/processor.js" >&2; exit 2; }
	go test ./sdk/gosxhost ./host/gamescore -count=1
	go test ./sdk/gosxhost -run TestWorkletMatchesNativeKernel -count=1 -v

# M5: SDK and layer-state parity over 64 bars across four native/WASM clients.
test-director: build-kernel-wasm
	node --test sdk/js/index.test.js
	go test ./sdk/director ./notation ./project ./kernel/cmd ./kernel/engine -run 'TestDirector' -count=1
	go test -timeout=5m -tags wasm_integration ./cmd/cicada-kernel-wasm -run '^TestAudioWASMDirectorM5$$' -count=1 -v
