#!/usr/bin/env bash
# Studio browser suites for CI, split into shards that run in parallel.
#
#   scripts/ci-studio-browser.sh run SHARD     run the segments of one shard
#   scripts/ci-studio-browser.sh list SHARD    print the segments of one shard
#   scripts/ci-studio-browser.sh check         fail unless every suite runs in exactly one shard
#
# "run" expects what the workflow's studio-build job produced: build/cicada,
# build/cicada-kernel.wasm, build/cicada-workstation and build/workstation/,
# already unpacked in the checkout. It also expects the variables GitHub
# Actions sets (RUNNER_TEMP, RUNNER_TOOL_CACHE, GITHUB_RUN_ID,
# GITHUB_RUN_ATTEMPT) and CHROME_VERSION.
#
# A segment is one function named seg_NAME. It starts one Studio process (or
# several, one per phase) on a fresh score and runs one suite against it, as
# the single "Studio browser continuity" step did before. Each segment writes
# its own fixture, so segments do not depend on each other and can move
# between shards. Every suite run goes through run_suite, which records
# NAME@CONFIG. segment_runs lists what each segment must record. "run" fails
# at the end of a shard unless every listed run was recorded, and "check"
# derives the suite names from segment_runs (not from comments) and compares
# them with the files on disk. CICADA_STUDIO_DRY_RUN=1 runs a shard without
# Studio, a browser or the network, so a test can prove that the recorded
# runs match segment_runs.
#
# Shard sizes come from step times in CI run 42717367458 (seconds, in the
# comment on each segment). When a suite grows, move segments between the
# lists in shard_segments to keep the shards near the same length.
set -euo pipefail

shard_count=4

# shard_segments SHARD prints the segments the shard runs, in order.
shard_segments() {
	case "$1" in
	1) echo "print_sources placements performance_pitch performance_sources placement_commands code_recovery pattern_machine portable_midi macro_controls preset_save" ;;
	2) echo "pattern_song precision first_song gestures portable_song sound_context continuity modulation_source collaboration columns midi_pickup shared_capture vocals vocal_levels dock_fit" ;;
	3) echo "owner_session page_stall scene_launch_keys pattern_grid tempo_editor recording_sources autoscroll arrangement_sources solo_clock mixer_strips navigation sampler_waveform export_sources library_recording midi_binding studio_edits recording_setup recording_levels meter_performance performance amp_automation" ;;
	4) echo "shutdown_proof live_write grid_sources shell direct_variation placement_variation editor performance_time startup tempo_clock export_stems chorus observation_clock note_editor" ;;
	*) echo "unknown shard: $1" >&2; return 1 ;;
	esac
}

# segment_runs SEGMENT prints what the segment must record with run_suite, as
# NAME or NAME@CONFIG. NAME is the file workstation/browser/NAME.test.cjs.
segment_runs() {
	case "$1" in
	shutdown_proof) ;;
	owner_session) echo owner-session ;;
	page_stall) echo page-stall ;;
	continuity) echo continuity ;;
	modulation_source) echo modulation-source ;;
	chorus) echo chorus ;;
	pattern_machine) echo pattern-machine ;;
	shell) echo shell ;;
	mixer_strips) echo mixer-strips ;;
	studio_edits) echo studio-edits ;;
	note_editor) echo note-editor ;;
	columns) echo columns ;;
	first_song) echo first-song@compose first-song@reopen ;;
	portable_song) echo portable-song@select portable-song@reopen ;;
	code_recovery) echo code-recovery@capture code-recovery@recover code-recovery@verify ;;
	export_sources) echo export-sources ;;
	export_stems) echo export-stems ;;
	collaboration) echo collaboration ;;
	placements) echo placements ;;
	navigation) echo arrangement-navigation ;;
	gestures) echo placement-gestures ;;
	precision) echo placement-precision ;;
	startup) echo startup ;;
	sampler_waveform) echo sampler-waveform ;;
	autoscroll) echo placement-autoscroll ;;
	placement_commands) echo placement-commands ;;
	arrangement_sources) echo arrangement-sources@edit arrangement-sources@reopen ;;
	tempo_editor) echo tempo-editor@primary-edit tempo-editor@primary-reopen tempo-editor@secondary-edit tempo-editor@secondary-reopen ;;
	solo_clock) echo solo-clock ;;
	portable_midi) echo portable-midi ;;
	midi_pickup) echo midi-pickup ;;
	macro_controls) echo macro-controls ;;
	midi_binding) echo midi-binding ;;
	preset_save) echo preset-save ;;
	observation_clock) echo observation-clock ;;
	tempo_clock) echo tempo-clock ;;
	library_recording) echo library-recording ;;
	pattern_song) echo pattern-song@scene-journey pattern-song@scene-reopen pattern-song@placement-journey pattern-song@placement-reopen ;;
	pattern_grid) echo pattern-grid@journey pattern-grid@reopen ;;
	placement_variation) echo placement-variation@journey placement-variation@reopen ;;
	performance_time) echo performance-time@journey performance-time@reopen ;;
	direct_variation) echo direct-variation@journey direct-variation@reopen ;;
	sound_context) echo sound-context@journey sound-context@reopen ;;
	performance_pitch) echo performance-pitch@journey performance-pitch@reopen ;;
	performance_sources) echo performance-sources@journey performance-sources@reopen ;;
	recording_sources) echo recording-sources@journey recording-sources@reopen ;;
	grid_sources) echo grid-sources@journey grid-sources@reopen ;;
	print_sources)
		echo sampler@primary-sampler producer-workflow@primary-producer print-roundtrip@primary-roundtrip print-roundtrip@primary-reopen
		echo sampler@secondary-sampler producer-workflow@secondary-producer print-roundtrip@secondary-roundtrip print-roundtrip@secondary-reopen
		echo producer-workflow@song-producer print-roundtrip@song-roundtrip print-roundtrip@song-reopen
		echo producer-workflow@controls-producer automation-recall@controls-automation-recall print-roundtrip@controls-roundtrip print-roundtrip@controls-reopen
		echo producer-workflow@tempo-producer automation-recall@tempo-automation-recall tempo-arrangement@tempo-tempo-arrangement print-roundtrip@tempo-roundtrip print-roundtrip@tempo-reopen
		;;
	editor) echo code-editor@buffer code-editor@project ;;
	dock_fit) echo dock-fit ;;
	meter_performance) echo meter-performance ;;
	recording_setup) echo recording-setup ;;
	recording_levels) echo recording-levels ;;
	vocals) echo vocals ;;
	vocal_levels) echo vocal-levels ;;
	shared_capture) echo shared-capture@mpe shared-capture@channel ;;
	performance) echo performance@capacity performance@buffer ;;
	amp_automation) echo amp-automation ;;
	scene_launch_keys) echo scene-launch-keys ;;
	live_write) echo live-write live-write-uncertain@reject live-write-uncertain@lost live-write-uncertain@timeout live-write-uncertain@late ;;
	*) return 1 ;;
	esac
}

# Suite files in workstation/browser that no shard runs, each with the reason.
# This list is an exemption, not coverage: it only keeps the completeness check
# from failing on them. Remove a name here when you add its suite to a segment.
not_run_by_ci=(
	# (none: every suite file runs in a shard)
)

self="${BASH_SOURCE[0]}"

# --- check ----------------------------------------------------------------

check_shards() {
	local root problems=0 seg shard name file run
	root="$(cd -- "$(dirname -- "$self")/.." && pwd)"
	local -A seg_shard=() suite_shard=() suite_segments=() declared=()

	complain() {
		echo "FAIL: $*" >&2
		problems=$((problems + 1))
	}

	# Every segment function sits in exactly one shard.
	mapfile -t defined < <(sed -n 's/^seg_\([a-z0-9_]*\)() {.*/\1/p' "$self")
	for shard in $(seq 1 "$shard_count"); do
		for seg in $(shard_segments "$shard"); do
			if [[ -n "${seg_shard[$seg]:-}" ]]; then
				complain "segment $seg is in shard ${seg_shard[$seg]} and shard $shard"
			fi
			seg_shard[$seg]="$shard"
		done
	done
	for seg in "${defined[@]}"; do
		if [[ -z "${seg_shard[$seg]:-}" ]]; then complain "segment $seg is in no shard"; fi
	done
	for seg in "${!seg_shard[@]}"; do
		if ! printf '%s\n' "${defined[@]}" | grep -qx "$seg"; then complain "shard ${seg_shard[$seg]} names segment $seg, which has no function"; fi
	done

	# Every suite in segment_runs sits in exactly one shard. A suite may run in
	# several segments (several configurations) if they share a shard.
	for seg in "${defined[@]}"; do
		body="$(sed -n "/^seg_${seg}() {/,/^}/p" "$self")"
		if ! segment_runs "$seg" > /dev/null; then
			complain "segment $seg has no entry in segment_runs"
			continue
		fi
		for run in $(segment_runs "$seg"); do
			name="${run%%@*}"
			declared[$name]=1
			shard="${seg_shard[$seg]:-}"
			if [[ -z "$shard" ]]; then continue; fi
			if [[ -n "${suite_shard[$name]:-}" && "${suite_shard[$name]}" != "$shard" ]]; then
				complain "suite $name runs in shard ${suite_shard[$name]} (${suite_segments[$name]}) and shard $shard ($seg)"
			fi
			suite_shard[$name]="$shard"
			suite_segments[$name]="${suite_segments[$name]:-} $seg"
			# The segment body must call run_suite for it (a computed name is
			# checked at run time by the dry run).
			if ! grep -q "run_suite \"\{0,1\}${name}[\"@ ]" <<<"$body" && ! grep -q 'run_suite "\$print_test@' <<<"$body"; then
				complain "segment $seg lists $name in segment_runs but never calls run_suite for it"
			fi
		done
		# Every literal run_suite call must be listed.
		for name in $(grep -o 'run_suite "\{0,1\}[a-z0-9-]*' <<<"$body" | sed 's/run_suite "\{0,1\}//'); do
			if ! segment_runs "$seg" | tr ' ' '\n' | sed 's/@.*//' | grep -qx "$name"; then
				complain "segment $seg calls run_suite for $name but segment_runs does not list it"
			fi
		done
	done

	# Every suite file on disk is declared, or listed as not run.
	for file in "$root"/workstation/browser/*.test.cjs; do
		name="$(basename "$file" .test.cjs)"
		skip=false
		for listed in "${not_run_by_ci[@]}"; do
			if [[ "$listed" == "$name" ]]; then skip=true; fi
		done
		if [[ "$skip" == true ]]; then
			if [[ -n "${declared[$name]:-}" ]]; then complain "suite $name is listed in not_run_by_ci but a segment runs it"; fi
			continue
		fi
		if [[ -z "${declared[$name]:-}" ]]; then complain "suite $name is in no shard (add it to a segment or to not_run_by_ci)"; fi
	done
	# Every declared name and every not-run name is a real file.
	for name in "${!declared[@]}" "${not_run_by_ci[@]}"; do
		if [[ ! -f "$root/workstation/browser/$name.test.cjs" ]]; then complain "no file workstation/browser/$name.test.cjs"; fi
	done

	if [[ "$problems" -gt 0 ]]; then
		echo "$problems problem(s)" >&2
		return 1
	fi
	echo "studio browser shards: ${#defined[@]} segments, ${#declared[@]} suites, each suite in exactly one of $shard_count shards"
}

# --- helpers used by segments ---------------------------------------------

studio_pid=
browser_root=
studio_port=
song_root=

# start_studio SCORE LOG [HEALTH_PATH] starts Studio on SCORE and waits for it.
start_studio() {
	local health="${3:-/api/state}"
	studio_log="$2"
	./build/cicada studio "$1" --audio null --listen "127.0.0.1:$studio_port" > "$2" 2>&1 &
	studio_pid=$!
	export CICADA_STUDIO_STARTUP_LOG="/proc/$studio_pid/fd/1"
	local _
	for _ in $(seq 1 100); do
		if curl --silent --fail "$CICADA_STUDIO_URL$health" > /dev/null; then break; fi
		kill -0 "$studio_pid"
		sleep 0.1
	done
}

stop_studio() {
	kill "$studio_pid"
	wait "$studio_pid" || true
	studio_log= # a later timeout must not print this server's log
}

# start_synthetic_studio SCORE NAME starts Studio with a synthetic microphone:
# the Go test harness TestSyntheticStudioUIHarness serves the domain API with
# synthetic PCM, and cicada-workstation serves the page in front of it. No
# device is opened. It sets studio_pid to the workstation. stop_synthetic_studio
# stops all three processes.
synthetic_pids=()
start_synthetic_studio() {
	local score="$1" name="$2" manifest backend token _
	manifest="$PWD/build/studio-$name-manifest.json"
	rm -f "$manifest"
	CICADA_SYNTHETIC_SCORE="$score" CICADA_SYNTHETIC_MANIFEST="$manifest" ./build/cicada-synthetic-ui.test -test.run '^TestSyntheticStudioUIHarness$' > "build/studio-$name-harness.log" 2>&1 &
	synthetic_pids=($!)
	for _ in $(seq 1 100); do
		if [[ -s "$manifest" ]]; then break; fi
		kill -0 "${synthetic_pids[0]}"
		sleep 0.1
	done
	backend="$(node -e 'process.stdout.write(require(process.argv[1]).backend)' "$manifest")"
	token="$(node -e 'process.stdout.write(require(process.argv[1]).token)' "$manifest")"
	# The workstation exits when its stdin closes; a sleeper holds it open.
	exec {synthetic_stdin}< <(sleep 3600)
	synthetic_pids+=($!)
	studio_log="build/studio-$name-server.log"
	CICADA_BACKEND_TOKEN="$token" ./build/cicada-workstation --backend "$backend" --listen "127.0.0.1:$studio_port" --state-dir "$browser_root/$name-state" --parent-watch <&"$synthetic_stdin" > "$studio_log" 2>&1 &
	studio_pid=$!
	exec {synthetic_stdin}<&-
	export CICADA_STUDIO_STARTUP_LOG="/proc/$studio_pid/fd/1"
	for _ in $(seq 1 100); do
		if curl --silent --fail "$CICADA_STUDIO_URL/" > /dev/null; then break; fi
		kill -0 "$studio_pid"
		sleep 0.1
	done
}

stop_synthetic_studio() {
	local pid
	for pid in "$studio_pid" "${synthetic_pids[@]}"; do kill "$pid" 2>/dev/null || true; done
	for pid in "$studio_pid" "${synthetic_pids[@]}"; do wait "$pid" 2>/dev/null || true; done
	synthetic_pids=()
	studio_log=
}

ran_file=

# run_suite NAME[@CONFIG] COMMAND... records the run, then runs the command.
# The record is a file, so a run inside a pipeline (| tee) still counts.
#
# Every suite runs under a time limit (CICADA_STUDIO_SUITE_TIMEOUT seconds,
# default 420). The slowest segment in run 42717367458 took 162 s and the
# slowest suite report 39 s, so 420 s is a hang, not a slow run. Four suites
# (grid-sources, direct-variation, performance-sources, recording-sources)
# set a 300 s limit of their own in node:test. This outer clock also counts
# node start-up and browser launch, so it must be longer: otherwise a run near
# its own budget is killed here before node:test can name the slow test. A suite that
# hits the limit fails with its name, the end of its report and the end of the
# Studio server log, instead of waiting for the 30-minute job limit.
suite_timeout="${CICADA_STUDIO_SUITE_TIMEOUT:-420}"
studio_log=

run_suite() {
	local name="$1" status=0 report
	echo "$name" >> "$ran_file"
	shift
	if [[ "${CICADA_STUDIO_DRY_RUN:-}" == 1 ]]; then return 0; fi
	report="$(mktemp "${TMPDIR:-/tmp}/ci-studio-suite.XXXXXX")"
	# pipefail (set above) makes $? the command's status, not tee's.
	timeout -k 15 "$suite_timeout" "$@" | tee "$report" || status=$?
	if [[ "$status" == 124 || "$status" == 137 ]]; then
		echo "FAIL: suite $name timed out after ${suite_timeout}s (exit $status)" >&2
		echo "--- last 60 lines of the report of $name ---" >&2
		tail -n 60 "$report" >&2 || true
		echo "--- last 60 lines of the Studio server log ${studio_log:-(none)} ---" >&2
		if [[ -n "$studio_log" ]]; then tail -n 60 "$studio_log" >&2 || true; fi
	fi
	rm -f "$report"
	return "$status"
}

# continuity_fixture NAME writes the continuity fixture score used by several suites.
continuity_fixture() {
	mkdir -p "$browser_root/$1"
	node workstation/browser/continuity.test.cjs --write-fixture "$browser_root/$1/continuity.cicada"
}

# --- segments ---------------------------------------------------------------
# Times are seconds in CI run 42717367458, including Studio start-up.

seg_owner_session() { # 1 s
	# Runs before NODE_OPTIONS loads the owner session helper for the others.
	run_suite owner-session env NODE_OPTIONS="$base_node_options" node --test workstation/browser/owner-session.test.cjs
}

seg_page_stall() { # about 35 s (32 s locally), no Studio
	# A frozen page must make its suite report and exit (see page-stall.cjs).
	run_suite page-stall env NODE_OPTIONS="$base_node_options" node --test workstation/browser/page-stall.test.cjs
}

seg_shutdown_proof() { # 10 s
	# suites:
	python3 scripts/check-studio-shutdown.py --cicada build/cicada --output build/studio-shutdown-proof
}

seg_continuity() { # 8 s
	continuity_fixture continuity
	start_studio "$browser_root/continuity/continuity.cicada" build/studio-continuity-server.log /
	run_suite continuity make test-studio-continuity | tee build/studio-continuity-report.txt
	stop_studio
}

seg_modulation_source() { # 5 s
	mkdir -p "$browser_root/modulation"
	export CICADA_MODULATION_SCORE="$browser_root/modulation/main.cicada"
	export CICADA_MODULATION_PROOF_DIR="$PWD/build/modulation-source-proof"
	node workstation/browser/modulation-source.test.cjs --write-fixture "$CICADA_MODULATION_SCORE"
	start_studio "$CICADA_MODULATION_SCORE" build/studio-modulation-server.log /
	run_suite modulation-source make test-studio-modulation-source | tee build/studio-modulation-report.txt
	stop_studio
}

seg_chorus() { # 4 s
	mkdir -p "$browser_root/chorus"
	export CICADA_CHORUS_SCORE="$browser_root/chorus/main.cicada"
	export CICADA_CHORUS_PROOF_DIR="$PWD/build/chorus-proof"
	node workstation/browser/chorus.test.cjs --write-fixture "$CICADA_CHORUS_SCORE"
	start_studio "$CICADA_CHORUS_SCORE" build/studio-chorus-server.log /
	run_suite chorus make test-studio-chorus | tee build/studio-chorus-report.txt
	stop_studio
}

seg_pattern_machine() { # 5 s
	mkdir -p "$browser_root/pattern-machine"
	export CICADA_MACHINE_SCORE="$browser_root/pattern-machine/main.cicada"
	export CICADA_MACHINE_PROOF="$PWD/build/pattern-machine-proof"
	mkdir -p "$CICADA_MACHINE_PROOF"
	node workstation/browser/pattern-machine.test.cjs --write-fixture "$CICADA_MACHINE_SCORE"
	start_studio "$CICADA_MACHINE_SCORE" build/studio-pattern-machine-server.log /
	run_suite pattern-machine make test-studio-pattern-machine | tee build/studio-pattern-machine-report.txt
	stop_studio
}

seg_shell() { # 44 s
	continuity_fixture shell
	start_studio "$browser_root/shell/continuity.cicada" build/studio-shell-server.log /
	run_suite shell make test-studio-shell | tee build/studio-shell-report.txt
	stop_studio
}

seg_mixer_strips() { # 15 s
	continuity_fixture mixer-strips
	start_studio "$browser_root/mixer-strips/continuity.cicada" build/studio-mixer-strips-server.log /
	run_suite mixer-strips make test-studio-mixer-strips | tee build/studio-mixer-strips-report.txt
	stop_studio
}

seg_studio_edits() { # 20 s
	continuity_fixture studio-edits
	start_studio "$browser_root/studio-edits/continuity.cicada" build/studio-edits-server.log /
	run_suite studio-edits make test-studio-edits | tee build/studio-edits-report.txt
	stop_studio
}

seg_note_editor() { # new; under 1 minute locally
	mkdir -p "$browser_root/note-editor"
	node workstation/browser/note-editor.test.cjs --write-fixture "$browser_root/note-editor/note-editor.cicada"
	export CICADA_NOTES_PROOF_DIR="$PWD/build/studio-notes-proof"
	start_studio "$browser_root/note-editor/note-editor.cicada" build/studio-notes-server.log /
	run_suite note-editor make test-studio-notes | tee build/studio-notes-report.txt
	stop_studio
}

seg_columns() { # 4 s
	continuity_fixture columns
	export CICADA_COLUMN_PROOF_DIR="$PWD/build/studio-column-proof"
	start_studio "$browser_root/columns/continuity.cicada" build/studio-column-server.log /
	run_suite columns make test-studio-columns | tee build/studio-column-report.txt
	stop_studio
}

seg_first_song() { # 22 s
	mkdir -p "$song_root"
	local song_cli="$PWD/build/cicada" phase
	(cd "$song_root" && "$song_cli" new first-light)
	export CICADA_SONG_PROOF_DIR="$PWD/build/studio-song-proof"
	for phase in compose reopen; do
		start_studio "$song_root/first-light/main.cicada" "build/studio-song-$phase-server.log" /
		export CICADA_SONG_REOPEN=0
		if [[ "$phase" == reopen ]]; then export CICADA_SONG_REOPEN=1; fi
		run_suite "first-song@$phase" make test-studio-first-song | tee "build/studio-song-$phase-report.txt"
		stop_studio
	done
	unset CICADA_SONG_REOPEN
}

seg_portable_song() { # 18 s
	local portable_score phase
	export CICADA_PORTABLE_FIXTURE="$PWD/build/studio-portable-fixture.json"
	export CICADA_PORTABLE_PROOF_DIR="$PWD/build/studio-portable-proof"
	go -C workstation run ./browser/testdata/portable-song > "$CICADA_PORTABLE_FIXTURE"
	portable_score="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["score"])' "$CICADA_PORTABLE_FIXTURE")"
	./build/cicada midi "$portable_score" -o build/studio-portable-notes.mid --report 2> build/studio-portable-midi-report.json
	python3 -c 'import json; r=json.load(open("build/studio-portable-midi-report.json")); assert r["notes"] > 0 and r["audio_tracks_omitted"] == ["guitar"], r'
	./build/cicada verify-midi build/studio-portable-notes.mid --ppq 960 --type 1
	./build/cicada midi "$portable_score" -o build/studio-portable-pattern.mid --pattern tone.melody
	./build/cicada verify-midi build/studio-portable-pattern.mid --ppq 960 --type 1
	for phase in select reopen; do
		start_studio "$portable_score" "build/studio-portable-$phase-server.log" /
		export CICADA_PORTABLE_REOPEN=0
		if [[ "$phase" == reopen ]]; then export CICADA_PORTABLE_REOPEN=1; fi
		run_suite "portable-song@$phase" make test-studio-portable-song | tee "build/studio-portable-$phase-report.txt"
		stop_studio
	done
	unset CICADA_PORTABLE_REOPEN
}

seg_code_recovery() { # 9 s
	local phase
	cp -R examples/studio-project "$browser_root/code-recovery"
	export CICADA_CODE_RECOVERY_PROOF="$PWD/build/studio-code-recovery-proof"
	for phase in capture recover verify; do
		start_studio "$browser_root/code-recovery/main.cicada" "build/studio-code-recovery-$phase-server.log" /
		export CICADA_CODE_RECOVERY_PHASE="$phase"
		run_suite "code-recovery@$phase" make test-studio-code-recovery | tee "build/studio-code-recovery-$phase-report.txt"
		stop_studio
	done
	unset CICADA_CODE_RECOVERY_PHASE
}

seg_export_sources() { # 5 s
	mkdir -p "$song_root"
	cp -R examples/studio-project "$song_root/export-sources"
	export CICADA_EXPORT_PROOF_DIR="$PWD/build/studio-export-proof"
	start_studio "$song_root/export-sources/main.cicada" build/studio-export-server.log /
	run_suite export-sources make test-studio-export-sources | tee build/studio-export-report.txt
	stop_studio
}

seg_export_stems() { # 5 s
	mkdir -p "$song_root"
	cp -R examples/studio-project "$song_root/export-stems"
	export CICADA_STEMS_PROOF_DIR="$PWD/build/studio-stems-proof"
	start_studio "$song_root/export-stems/main.cicada" build/studio-stems-server.log /
	run_suite export-stems make test-studio-export-stems | tee build/studio-stems-report.txt
	stop_studio
}

seg_collaboration() { # 5 s
	continuity_fixture collaboration
	start_studio "$browser_root/collaboration/continuity.cicada" build/studio-collaboration-server.log /
	run_suite collaboration make test-studio-collaboration | tee build/studio-collaboration-report.txt
	stop_studio
}

seg_placements() { # 24 s
	node workstation/browser/placements.test.cjs --write-fixture "$browser_root/placements.cicada"
	export CICADA_PLACEMENT_PROOF_DIR="$PWD/build/studio-placement-proof"
	export CICADA_PLACEMENT_BUILD="make build-workstation-release"
	start_studio "$browser_root/placements.cicada" build/studio-placement-server.log
	run_suite placements make test-studio-placements | tee build/studio-placement-report.txt
	stop_studio
}

seg_navigation() { # 12 s
	node workstation/browser/placements.test.cjs --write-fixture "$browser_root/navigation.cicada"
	start_studio "$browser_root/navigation.cicada" build/studio-navigation-server.log
	run_suite arrangement-navigation env CICADA_ARRANGEMENT_PROOF_DIR="$PWD/build/studio-arrangement-proof" node --test workstation/browser/arrangement-navigation.test.cjs | tee build/studio-navigation-report.txt
	stop_studio
}

seg_gestures() { # 21 s
	node workstation/browser/placements.test.cjs --write-fixture "$browser_root/gestures.cicada"
	start_studio "$browser_root/gestures.cicada" build/studio-gestures-server.log
	run_suite placement-gestures env CICADA_GESTURE_SCORE="$browser_root/gestures.cicada" CICADA_GESTURE_PROOF_DIR="$PWD/build/studio-gestures-proof" node --test workstation/browser/placement-gestures.test.cjs | tee build/studio-gestures-report.txt
	stop_studio
}

seg_precision() { # 30 s
	node workstation/browser/placement-precision.test.cjs --write-fixture "$browser_root/precision.cicada"
	start_studio "$browser_root/precision.cicada" build/studio-precision-server.log
	run_suite placement-precision env CICADA_PRECISION_PROOF_DIR="$PWD/build/studio-precision-proof" node --test workstation/browser/placement-precision.test.cjs | tee build/studio-precision-report.txt
	stop_studio
}

seg_startup() { # 13 s
	node workstation/browser/placements.test.cjs --write-fixture "$browser_root/startup.cicada"
	start_studio "$browser_root/startup.cicada" build/studio-startup-server.log
	run_suite startup env CICADA_STARTUP_PROOF_DIR="$PWD/build/studio-startup-proof" node --test workstation/browser/startup.test.cjs | tee build/studio-startup-report.txt
	stop_studio
}

seg_sampler_waveform() { # 6 s
	node workstation/browser/sampler-waveform.test.cjs --write-fixture "$browser_root/sampler-waveform.cicada"
	start_studio "$browser_root/sampler-waveform.cicada" build/studio-sampler-waveform-server.log
	run_suite sampler-waveform env CICADA_SAMPLER_PROOF_DIR="$PWD/build/studio-sampler-waveform-proof" node --test workstation/browser/sampler-waveform.test.cjs | tee build/studio-sampler-waveform-report.txt
	stop_studio
}

seg_autoscroll() { # 27 s
	node workstation/browser/placement-autoscroll.test.cjs --write-fixture "$browser_root/autoscroll.cicada"
	start_studio "$browser_root/autoscroll.cicada" build/studio-autoscroll-server.log
	run_suite placement-autoscroll env CICADA_AUTOSCROLL_PROOF_DIR="$PWD/build/studio-autoscroll-proof" node --test workstation/browser/placement-autoscroll.test.cjs | tee build/studio-autoscroll-report.txt
	stop_studio
}

seg_placement_commands() { # 13 s
	node workstation/browser/placement-commands.test.cjs --write-fixture "$browser_root/placement-commands.cicada"
	start_studio "$browser_root/placement-commands.cicada" build/studio-placement-commands-server.log
	run_suite placement-commands node --test workstation/browser/placement-commands.test.cjs | tee build/studio-placement-commands-report.txt
	stop_studio
}

seg_arrangement_sources() { # 22 s
	local phase
	node workstation/browser/arrangement-sources.test.cjs --write-fixture "$browser_root/arrangement-sources" > build/studio-arrangement-sources-fixture.json
	export CICADA_ARRANGEMENT_FIXTURE="$PWD/build/studio-arrangement-sources-fixture.json"
	export CICADA_ARRANGEMENT_PROOF_DIR="$PWD/build/studio-arrangement-sources-proof"
	for phase in edit reopen; do
		start_studio "$browser_root/arrangement-sources/main.cicada" "build/studio-arrangement-sources-$phase-server.log"
		export CICADA_ARRANGEMENT_REOPEN=0
		if [[ "$phase" == reopen ]]; then export CICADA_ARRANGEMENT_REOPEN=1; fi
		run_suite "arrangement-sources@$phase" node --test workstation/browser/arrangement-sources.test.cjs | tee "build/studio-arrangement-sources-$phase-report.txt"
		stop_studio
	done
	unset CICADA_ARRANGEMENT_REOPEN
}

seg_tempo_editor() { # 43 s
	local tempo_layout tempo_phase tempo_args
	for tempo_layout in primary secondary; do
		tempo_args=()
		if [[ "$tempo_layout" == primary ]]; then tempo_args+=(--primary); fi
		node workstation/browser/tempo-editor.test.cjs --write-fixture "$browser_root/tempo-$tempo_layout" "${tempo_args[@]}" > "build/studio-tempo-$tempo_layout-fixture.json"
		export CICADA_TEMPO_FIXTURE="$PWD/build/studio-tempo-$tempo_layout-fixture.json"
		export CICADA_TEMPO_PROOF_DIR="$PWD/build/studio-tempo-$tempo_layout-proof"
		for tempo_phase in edit reopen; do
			start_studio "$browser_root/tempo-$tempo_layout/main.cicada" "build/studio-tempo-$tempo_layout-$tempo_phase-server.log"
			export CICADA_TEMPO_REOPEN=0
			if [[ "$tempo_phase" == reopen ]]; then export CICADA_TEMPO_REOPEN=1; fi
			run_suite "tempo-editor@$tempo_layout-$tempo_phase" node --test workstation/browser/tempo-editor.test.cjs | tee "build/studio-tempo-$tempo_layout-$tempo_phase-report.txt"
			stop_studio
		done
	done
	unset CICADA_TEMPO_REOPEN
}

seg_solo_clock() { # 19 s
	node workstation/browser/solo-clock.test.cjs --write-fixture "$browser_root/solo-clock.cicada"
	start_studio "$browser_root/solo-clock.cicada" build/studio-solo-clock-server.log
	run_suite solo-clock env CICADA_SOLO_SCORE="$browser_root/solo-clock.cicada" CICADA_SOLO_PROOF_DIR="$PWD/build/studio-solo-clock-proof" node --test workstation/browser/solo-clock.test.cjs | tee build/studio-solo-clock-report.txt
	stop_studio
}

seg_portable_midi() { # 5 s
	node workstation/browser/portable-midi.test.cjs --write-fixture "$browser_root/portable-midi"
	start_studio "$browser_root/portable-midi/main.cicada" build/studio-portable-midi-server.log
	run_suite portable-midi env CICADA_PORTABLE_MIDI_SCORE="$browser_root/portable-midi/main.cicada" CICADA_PORTABLE_MIDI_PROOF_DIR="$PWD/build/studio-portable-midi-proof" node --test workstation/browser/portable-midi.test.cjs | tee build/studio-portable-midi-report.txt
	stop_studio
}

seg_midi_pickup() { # 2 s
	node workstation/browser/midi-pickup.test.cjs --write-fixture "$browser_root/midi-pickup"
	start_studio "$browser_root/midi-pickup/main.cicada" build/studio-midi-pickup-server.log
	run_suite midi-pickup node --test workstation/browser/midi-pickup.test.cjs | tee build/studio-midi-pickup-report.txt
	stop_studio
}

seg_macro_controls() { # 3 s
	node workstation/browser/macro-controls.test.cjs --write-fixture "$browser_root/macro-controls"
	start_studio "$browser_root/macro-controls/main.cicada" build/studio-macro-controls-server.log
	run_suite macro-controls env CICADA_MACRO_PROOF_DIR="$PWD/build/studio-macro-controls-proof" node --test workstation/browser/macro-controls.test.cjs | tee build/studio-macro-controls-report.txt
	stop_studio
}

seg_midi_binding() { # 3 s
	# The fixture generator is the portable-midi one, as before.
	node workstation/browser/portable-midi.test.cjs --write-fixture "$browser_root/midi-binding"
	start_studio "$browser_root/midi-binding/main.cicada" build/studio-midi-binding-server.log
	run_suite midi-binding env CICADA_PORTABLE_MIDI_SCORE="$browser_root/midi-binding/main.cicada" CICADA_PORTABLE_MIDI_PROOF_DIR="$PWD/build/studio-midi-binding-proof" node --test workstation/browser/midi-binding.test.cjs | tee build/studio-midi-binding-report.txt
	stop_studio
}

seg_preset_save() { # 3 s
	node workstation/browser/preset-save.test.cjs --write-fixture "$browser_root/preset-save"
	start_studio "$browser_root/preset-save/main.cicada" build/studio-preset-save-server.log
	run_suite preset-save env CICADA_PRESET_SCORE="$browser_root/preset-save/main.cicada" CICADA_PRESET_PROOF_DIR="$PWD/build/studio-preset-save-proof" node --test workstation/browser/preset-save.test.cjs | tee build/studio-preset-save-report.txt
	stop_studio
}

seg_observation_clock() { # 4 s
	local observation_score
	go -C workstation run ./browser/testdata/print-sources --primary-arrangement > build/studio-observation-clock-fixture.json
	export CICADA_OBSERVATION_FIXTURE="$PWD/build/studio-observation-clock-fixture.json"
	observation_score="$(node -e 'process.stdout.write(require(process.argv[1]).score)' "$CICADA_OBSERVATION_FIXTURE")"
	start_studio "$observation_score" build/studio-observation-clock-server.log
	run_suite observation-clock env CICADA_OBSERVATION_PROOF_DIR="$PWD/build/studio-observation-clock-proof" node --test workstation/browser/observation-clock.test.cjs | tee build/studio-observation-clock-report.txt
	stop_studio
}

seg_tempo_clock() { # 7 s
	node workstation/browser/tempo-clock.test.cjs --write-fixture "$browser_root/tempo-clock.cicada"
	start_studio "$browser_root/tempo-clock.cicada" build/studio-tempo-clock-server.log
	run_suite tempo-clock env CICADA_TEMPO_CLOCK_PROOF_DIR="$PWD/build/studio-tempo-clock-proof" node --test workstation/browser/tempo-clock.test.cjs | tee build/studio-tempo-clock-report.txt
	stop_studio
}

seg_library_recording() { # 5 s
	node workstation/browser/library-recording.test.cjs --write-fixture "$browser_root/library-recording.cicada"
	start_studio "$browser_root/library-recording.cicada" build/studio-library-recording-server.log
	run_suite library-recording env CICADA_LIBRARY_RECORDING_PROOF_DIR="$PWD/build/studio-library-recording-proof" node --test workstation/browser/library-recording.test.cjs | tee build/studio-library-recording-report.txt
	stop_studio
}

seg_dock_fit() { # new; 5 s locally
	continuity_fixture dock-fit
	export CICADA_DOCK_PROOF_DIR="$PWD/build/studio-dock-fit-proof"
	start_studio "$browser_root/dock-fit/continuity.cicada" build/studio-dock-fit-server.log /
	run_suite dock-fit make test-studio-dock-fit | tee build/studio-dock-fit-report.txt
	stop_studio
}

seg_meter_performance() { # new; 5 s locally
	# Despite the name this is a correctness test: no canvas paint while the meters are unchanged, and the poller keeps up (median gap between meter requests at most 100 ms, timed on the page clock).
	continuity_fixture meter-performance
	start_studio "$browser_root/meter-performance/continuity.cicada" build/studio-meter-performance-server.log /
	run_suite meter-performance env CICADA_METERS_URL="$CICADA_STUDIO_URL" CICADA_METERS_PROOF_DIR="$PWD/build/studio-meter-performance-proof" make test-studio-meter-performance | tee build/studio-meter-performance-report.txt
	stop_studio
}

seg_recording_setup() { # new; 5 s locally
	node workstation/browser/recording-setup.test.cjs --write-fixture "$browser_root/recording-setup/main.cicada"
	start_studio "$browser_root/recording-setup/main.cicada" build/studio-recording-setup-server.log /
	run_suite recording-setup env CICADA_SETUP_URL="$CICADA_STUDIO_URL" CICADA_VOCAL_PROOF_DIR="$PWD/build/studio-recording-setup-proof" make test-studio-recording-setup | tee build/studio-recording-setup-report.txt
	stop_studio
}

seg_recording_levels() { # new; 10 s locally
	node workstation/browser/recording-levels.test.cjs --write-fixture "$browser_root/recording-levels/main.cicada"
	start_studio "$browser_root/recording-levels/main.cicada" build/studio-recording-levels-server.log /
	run_suite recording-levels env CICADA_LEVELS_URL="$CICADA_STUDIO_URL" CICADA_LEVELS_PROOF_DIR="$PWD/build/studio-recording-levels-proof" make test-studio-recording-levels | tee build/studio-recording-levels-report.txt
	stop_studio
}

seg_vocals() { # new; 15 s locally
	local vocals_score
	go -C workstation run ./browser/testdata/print-sources --primary-arrangement > build/studio-vocals-fixture.json
	vocals_score="$(node -e 'process.stdout.write(require(process.argv[1]).score)' "$PWD/build/studio-vocals-fixture.json")"
	start_studio "$vocals_score" build/studio-vocals-server.log /
	run_suite vocals env CICADA_VOCAL_PROOF_DIR="$PWD/build/studio-vocals-proof" make test-studio-vocals | tee build/studio-vocals-report.txt
	stop_studio
}

seg_vocal_levels() { # new; 15 s locally
	local levels_score
	go -C workstation run ./browser/testdata/print-sources > build/studio-vocal-levels-fixture.json
	levels_score="$(node -e 'process.stdout.write(require(process.argv[1]).score)' "$PWD/build/studio-vocal-levels-fixture.json")"
	start_studio "$levels_score" build/studio-vocal-levels-server.log /
	run_suite vocal-levels env CICADA_LEVELING_URL="$CICADA_STUDIO_URL" CICADA_LEVELING_SCORE="$levels_score" CICADA_LEVELING_PROOF_DIR="$PWD/build/studio-vocal-levels-proof" make test-studio-vocal-levels | tee build/studio-vocal-levels-report.txt
	stop_studio
}

seg_shared_capture() { # new; 35 s locally
	# One fresh Studio per kind: the suite rewrites the score and claims edit access.
	local capture_kind capture_env
	for capture_kind in mpe channel; do
		continuity_fixture "shared-capture-$capture_kind"
		capture_env=()
		if [[ "$capture_kind" == channel ]]; then capture_env+=(CICADA_CAPTURE_KIND=channel); fi
		start_studio "$browser_root/shared-capture-$capture_kind/continuity.cicada" "build/studio-shared-capture-$capture_kind-server.log" /
		run_suite "shared-capture@$capture_kind" env CICADA_SHARED_CAPTURE_PROOF_DIR="$PWD/build/studio-shared-capture-proof" "${capture_env[@]}" make test-studio-shared-capture | tee "build/studio-shared-capture-$capture_kind-report.txt"
		stop_studio
	done
}

seg_performance() { # new; 15 s locally
	# Two Studios: playback leaves the audio runtime armed, which locks the audio
	# form the buffer check uses, so each half starts on a fresh process.
	local performance_part
	node workstation/browser/performance.test.cjs --write-fixture "$browser_root/performance/main.cicada"
	for performance_part in capacity buffer; do
		start_studio "$browser_root/performance/main.cicada" "build/studio-performance-$performance_part-server.log" /
		run_suite "performance@$performance_part" env CICADA_PERFORMANCE_PART="$performance_part" CICADA_CAPACITY_URL="$CICADA_STUDIO_URL" CICADA_BUFFER_URL="$CICADA_STUDIO_URL" CICADA_PERFORMANCE_PROOF_DIR="$PWD/build/studio-performance-proof" make test-studio-performance | tee "build/studio-performance-$performance_part-report.txt"
		stop_studio
	done
}

seg_scene_launch_keys() { # new; about 10 s locally
	continuity_fixture scene-launch-keys
	start_studio "$browser_root/scene-launch-keys/continuity.cicada" build/studio-scene-launch-keys-server.log /
	run_suite scene-launch-keys make test-studio-scene-launch-keys | tee build/studio-scene-launch-keys-report.txt
	stop_studio
}

seg_live_write() { # new; about 3 minutes locally, most of it the harness build and five Studio starts
	# These suites need a synthetic microphone: build the harness test binary
	# once, then start it with a score that has no amp_drive curve.
	local live_score live_mode
	go test -c -o build/cicada-synthetic-ui.test ./cmd/cicada
	live_score="$(node workstation/browser/live-fixture.cjs "$browser_root/live-write" --no-curve)"
	start_synthetic_studio "$live_score" live-write
	run_suite live-write env CICADA_LIVE_WRITE_SCORE="$live_score" CICADA_LIVE_WRITE_PROOF_DIR="$PWD/build/studio-live-write-proof" make test-studio-live-write | tee build/studio-live-write-report.txt
	stop_synthetic_studio
	for live_mode in reject lost timeout late; do
		live_score="$(node workstation/browser/live-fixture.cjs "$browser_root/live-write-$live_mode" --no-curve)"
		start_synthetic_studio "$live_score" "live-write-$live_mode"
		run_suite "live-write-uncertain@$live_mode" env CICADA_LIVE_WRITE_FAILURE="$live_mode" CICADA_LIVE_WRITE_PROOF_DIR="$PWD/build/studio-live-write-proof" make test-studio-live-write-uncertain | tee "build/studio-live-write-$live_mode-report.txt"
		stop_synthetic_studio
	done
}

seg_amp_automation() { # new; 10 s locally
	local amp_score
	amp_score="$(node workstation/browser/amp-automation.test.cjs --write-fixture "$browser_root/amp-automation")"
	start_studio "$amp_score" build/studio-amp-automation-server.log /
	run_suite amp-automation env CICADA_AMP_AUTOMATION_FIXTURE="$amp_score" CICADA_AMP_AUTOMATION_PROOF_DIR="$PWD/build/studio-amp-automation-proof" make test-studio-amp-automation | tee build/studio-amp-automation-report.txt
	stop_studio
}

seg_pattern_song() { # 132 s
	local song_layout song_phase song_args
	for song_layout in scene placement; do
		song_args=()
		if [[ "$song_layout" == placement ]]; then song_args+=(--placement); fi
		node workstation/browser/pattern-song.test.cjs --write-fixture "$browser_root/pattern-song-$song_layout.cicada" "${song_args[@]}"
		for song_phase in journey reopen; do
			start_studio "$browser_root/pattern-song-$song_layout.cicada" "build/studio-pattern-song-$song_layout-$song_phase-server.log"
			CICADA_PATTERN_SONG_REOPEN=0
			if [[ "$song_phase" == reopen ]]; then CICADA_PATTERN_SONG_REOPEN=1; fi
			export CICADA_PATTERN_SONG_REOPEN
			run_suite "pattern-song@$song_layout-$song_phase" env CICADA_PATTERN_SONG_PROOF_DIR="$PWD/build/studio-pattern-song-$song_layout-proof" node --test workstation/browser/pattern-song.test.cjs | tee "build/studio-pattern-song-$song_layout-$song_phase-report.txt"
			stop_studio
		done
	done
}

seg_pattern_grid() { # 65 s
	local grid_phase
	node workstation/browser/pattern-grid.test.cjs --write-fixture "$browser_root/pattern-grid.cicada"
	for grid_phase in journey reopen; do
		start_studio "$browser_root/pattern-grid.cicada" "build/studio-pattern-grid-$grid_phase-server.log"
		CICADA_PATTERN_GRID_REOPEN=0
		if [[ "$grid_phase" == reopen ]]; then CICADA_PATTERN_GRID_REOPEN=1; fi
		export CICADA_PATTERN_GRID_REOPEN
		run_suite "pattern-grid@$grid_phase" env CICADA_PATTERN_GRID_PROOF_DIR="$PWD/build/studio-pattern-grid-proof" node --test workstation/browser/pattern-grid.test.cjs | tee "build/studio-pattern-grid-$grid_phase-report.txt"
		stop_studio
	done
}

seg_placement_variation() { # 27 s
	local variation_phase
	go -C workstation run ./browser/testdata/placement-variation "$browser_root/placement-variation" > build/studio-placement-variation-fixture.json
	for variation_phase in journey reopen; do
		start_studio "$browser_root/placement-variation/main.cicada" "build/studio-placement-variation-$variation_phase-server.log"
		CICADA_VARIATION_REOPEN=0
		if [[ "$variation_phase" == reopen ]]; then CICADA_VARIATION_REOPEN=1; fi
		export CICADA_VARIATION_REOPEN
		run_suite "placement-variation@$variation_phase" env CICADA_VARIATION_PROOF_DIR="$PWD/build/studio-placement-variation-proof" node --test workstation/browser/placement-variation.test.cjs | tee "build/studio-placement-variation-$variation_phase-report.txt"
		stop_studio
	done
}

seg_performance_time() { # 19 s
	local time_phase
	node workstation/browser/performance-time.test.cjs --write-fixture "$browser_root/performance-time" > build/studio-performance-time-fixture.json
	for time_phase in journey reopen; do
		start_studio "$browser_root/performance-time/main.cicada" "build/studio-performance-time-$time_phase-server.log"
		CICADA_TIME_REOPEN=0
		if [[ "$time_phase" == reopen ]]; then CICADA_TIME_REOPEN=1; fi
		export CICADA_TIME_REOPEN
		run_suite "performance-time@$time_phase" env CICADA_TIME_PROOF_DIR="$PWD/build/studio-performance-time-proof" node --test workstation/browser/performance-time.test.cjs | tee "build/studio-performance-time-$time_phase-report.txt"
		if [[ "$time_phase" == journey ]]; then stop_studio; fi
	done
	python3 workstation/browser/performance-time-audio.py --cicada build/cicada --proof build/studio-performance-time-proof
	stop_studio
}

seg_direct_variation() { # 40 s
	local direct_phase
	go -C workstation run ./browser/testdata/direct-variation "$browser_root/direct-variation" > build/studio-direct-variation-fixture.json
	for direct_phase in journey reopen; do
		start_studio "$browser_root/direct-variation/main.cicada" "build/studio-direct-variation-$direct_phase-server.log"
		CICADA_DIRECT_REOPEN=0
		if [[ "$direct_phase" == reopen ]]; then CICADA_DIRECT_REOPEN=1; fi
		export CICADA_DIRECT_REOPEN
		run_suite "direct-variation@$direct_phase" env CICADA_DIRECT_PROOF_DIR="$PWD/build/studio-direct-variation-proof" node --test workstation/browser/direct-variation.test.cjs | tee "build/studio-direct-variation-$direct_phase-report.txt"
		if [[ "$direct_phase" == journey ]]; then stop_studio; fi
	done
	python3 workstation/browser/direct-variation-audio.py --cicada build/cicada --proof build/studio-direct-variation-proof --fixture build/studio-direct-variation-fixture.json
	stop_studio
}

seg_sound_context() { # 13 s
	local context_phase
	node workstation/browser/sound-context.test.cjs --write-fixture "$browser_root/sound-context" > build/studio-sound-context-fixture.json
	for context_phase in journey reopen; do
		start_studio "$browser_root/sound-context/main.cicada" "build/studio-sound-context-$context_phase-server.log"
		CICADA_CONTEXT_REOPEN=0
		if [[ "$context_phase" == reopen ]]; then CICADA_CONTEXT_REOPEN=1; fi
		export CICADA_CONTEXT_REOPEN
		run_suite "sound-context@$context_phase" env CICADA_CONTEXT_PROOF_DIR="$PWD/build/studio-sound-context-proof" node --test workstation/browser/sound-context.test.cjs | tee "build/studio-sound-context-$context_phase-report.txt"
		stop_studio
	done
}

seg_performance_pitch() { # 20 s
	local pitch_phase
	node workstation/browser/performance-pitch.test.cjs --write-fixture "$browser_root/performance-pitch" > build/studio-performance-pitch-fixture.json
	for pitch_phase in journey reopen; do
		start_studio "$browser_root/performance-pitch/main.cicada" "build/studio-performance-pitch-$pitch_phase-server.log"
		CICADA_PITCH_REOPEN=0
		if [[ "$pitch_phase" == reopen ]]; then CICADA_PITCH_REOPEN=1; fi
		export CICADA_PITCH_REOPEN
		run_suite "performance-pitch@$pitch_phase" env CICADA_PITCH_PROOF_DIR="$PWD/build/studio-performance-pitch-proof" node --test workstation/browser/performance-pitch.test.cjs | tee "build/studio-performance-pitch-$pitch_phase-report.txt"
		if [[ "$pitch_phase" == journey ]]; then stop_studio; fi
	done
	python3 workstation/browser/performance-pitch-audio.py --cicada build/cicada --proof build/studio-performance-pitch-proof
	stop_studio
}

seg_performance_sources() { # 17 s
	local sources_phase
	node workstation/browser/performance-sources.test.cjs --write-fixture "$browser_root/performance-sources" > build/studio-performance-sources-fixture.json
	export CICADA_SOURCES_FIXTURE="$PWD/build/studio-performance-sources-fixture.json"
	for sources_phase in journey reopen; do
		start_studio "$browser_root/performance-sources/main.cicada" "build/studio-performance-sources-$sources_phase-server.log"
		CICADA_SOURCES_REOPEN=0
		if [[ "$sources_phase" == reopen ]]; then CICADA_SOURCES_REOPEN=1; fi
		export CICADA_SOURCES_REOPEN
		run_suite "performance-sources@$sources_phase" env CICADA_SOURCES_PROOF_DIR="$PWD/build/studio-performance-sources-proof" node --test workstation/browser/performance-sources.test.cjs | tee "build/studio-performance-sources-$sources_phase-report.txt"
		if [[ "$sources_phase" == journey ]]; then stop_studio; fi
	done
	python3 workstation/browser/performance-sources-audio.py --cicada build/cicada --proof build/studio-performance-sources-proof
	stop_studio
}

seg_recording_sources() { # 36 s
	local record_sources_phase
	node workstation/browser/recording-sources.test.cjs --write-fixture "$browser_root/recording-sources" > build/studio-recording-sources-fixture.json
	export CICADA_RECORD_SOURCES_FIXTURE="$PWD/build/studio-recording-sources-fixture.json"
	for record_sources_phase in journey reopen; do
		start_studio "$browser_root/recording-sources/main.cicada" "build/studio-recording-sources-$record_sources_phase-server.log"
		CICADA_RECORD_SOURCES_REOPEN=0
		if [[ "$record_sources_phase" == reopen ]]; then CICADA_RECORD_SOURCES_REOPEN=1; fi
		export CICADA_RECORD_SOURCES_REOPEN
		run_suite "recording-sources@$record_sources_phase" env CICADA_RECORD_SOURCES_PROOF_DIR="$PWD/build/studio-recording-sources-proof" node --test workstation/browser/recording-sources.test.cjs | tee "build/studio-recording-sources-$record_sources_phase-report.txt"
		if [[ "$record_sources_phase" == journey ]]; then stop_studio; fi
	done
	python3 workstation/browser/recording-sources-audio.py --cicada build/cicada --proof build/studio-recording-sources-proof
	stop_studio
}

seg_grid_sources() { # 63 s
	local grid_sources_phase
	node workstation/browser/grid-sources.test.cjs --write-fixture "$browser_root/grid-sources" > build/studio-grid-sources-fixture.json
	export CICADA_GRID_SOURCES_FIXTURE="$PWD/build/studio-grid-sources-fixture.json"
	for grid_sources_phase in journey reopen; do
		start_studio "$browser_root/grid-sources/main.cicada" "build/studio-grid-sources-$grid_sources_phase-server.log"
		CICADA_GRID_SOURCES_REOPEN=0
		if [[ "$grid_sources_phase" == reopen ]]; then CICADA_GRID_SOURCES_REOPEN=1; fi
		export CICADA_GRID_SOURCES_REOPEN
		run_suite "grid-sources@$grid_sources_phase" env CICADA_GRID_SOURCES_PROOF_DIR="$PWD/build/studio-grid-sources-proof" node --test workstation/browser/grid-sources.test.cjs | tee "build/studio-grid-sources-$grid_sources_phase-report.txt"
		if [[ "$grid_sources_phase" == journey ]]; then stop_studio; fi
	done
	python3 workstation/browser/grid-sources-audio.py --cicada build/cicada --proof build/studio-grid-sources-proof
	stop_studio
}

seg_print_sources() { # 162 s
	local print_layout print_args print_phases print_phase print_test
	for print_layout in primary secondary song controls tempo; do
		print_args=()
		print_phases=(sampler producer roundtrip reopen)
		if [[ "$print_layout" == primary ]]; then print_args+=(--primary-arrangement); fi
		if [[ "$print_layout" == song ]]; then
			print_args+=(--scene-song --sampler-bank)
			print_phases=(producer roundtrip reopen)
		fi
		if [[ "$print_layout" == controls ]]; then
			print_args+=(--scene-controls --sampler-bank)
			print_phases=(producer automation-recall roundtrip reopen)
		fi
		if [[ "$print_layout" == tempo ]]; then
			print_args+=(--scene-tempo --sampler-bank)
			print_phases=(producer automation-recall tempo-arrangement roundtrip reopen)
		fi
		go -C workstation run ./browser/testdata/print-sources "${print_args[@]}" > "build/studio-print-$print_layout-fixture.json"
		export CICADA_PRINT_FIXTURE="$PWD/build/studio-print-$print_layout-fixture.json"
		CICADA_PRODUCER_SCORE="$(node -e 'process.stdout.write(require(process.argv[1]).score)' "$CICADA_PRINT_FIXTURE")"
		CICADA_PRODUCER_STORAGE="$(dirname "$CICADA_PRODUCER_SCORE")/session.json"
		export CICADA_PRODUCER_SCORE CICADA_PRODUCER_STORAGE
		export CICADA_PRINT_PROOF_DIR="$PWD/build/studio-print-$print_layout-proof/roundtrip"
		export CICADA_SAMPLER_PROOF_DIR="$PWD/build/studio-print-$print_layout-proof/sampler"
		export CICADA_PRODUCER_PROOF_DIR="$PWD/build/studio-print-$print_layout-proof/producer"
		if [[ "$print_layout" != primary ]]; then
			CICADA_SAMPLER_ARRANGEMENT_SOURCE="$(node -e 'process.stdout.write(require(process.argv[1]).arrangement)' "$CICADA_PRINT_FIXTURE")"
			CICADA_PRODUCER_ARRANGEMENT_SOURCE="$CICADA_SAMPLER_ARRANGEMENT_SOURCE"
			export CICADA_SAMPLER_ARRANGEMENT_SOURCE CICADA_PRODUCER_ARRANGEMENT_SOURCE
		fi
		for print_phase in "${print_phases[@]}"; do
			start_studio "$CICADA_PRODUCER_SCORE" "build/studio-print-$print_layout-$print_phase-server.log"
			export CICADA_PRODUCER_URL="$CICADA_STUDIO_URL" CICADA_PRINT_REOPEN=0
			print_test="$print_phase"
			if [[ "$print_phase" == producer ]]; then print_test=producer-workflow; fi
			if [[ "$print_phase" == roundtrip || "$print_phase" == reopen ]]; then print_test=print-roundtrip; fi
			if [[ "$print_phase" == reopen ]]; then export CICADA_PRINT_REOPEN=1; fi
			run_suite "$print_test@$print_layout-$print_phase" node --test "workstation/browser/$print_test.test.cjs" | tee "build/studio-print-$print_layout-$print_phase-report.txt"
			stop_studio
		done
		unset CICADA_SAMPLER_ARRANGEMENT_SOURCE CICADA_PRODUCER_ARRANGEMENT_SOURCE CICADA_PRINT_REOPEN
	done
}

seg_editor() { # 22 s
	local editor_mode editor_score
	for editor_mode in buffer project; do
		if [[ "$editor_mode" == project ]]; then
			node workstation/browser/code-editor.test.cjs --write-project "$browser_root/editor-project"
			editor_score="$browser_root/editor-project/main.cicada"
			export CICADA_EDITOR_PROJECT=1
			export CICADA_EDITOR_PROJECT_ROOT="$browser_root/editor-project"
		else
			node workstation/browser/code-editor.test.cjs --write-fixture "$browser_root/editor.cicada"
			editor_score="$browser_root/editor.cicada"
			export CICADA_EDITOR_PROJECT=0
		fi
		start_studio "$editor_score" "build/studio-editor-$editor_mode-server.log"
		run_suite "code-editor@$editor_mode" node --test workstation/browser/code-editor.test.cjs | tee "build/studio-editor-$editor_mode-report.txt"
		stop_studio
	done
}

# --- run ------------------------------------------------------------------

run_shard() {
	local shard="$1" seg run missing=0
	segments="$(shard_segments "$shard")"

	if [[ "${CICADA_STUDIO_DRY_RUN:-}" == 1 ]]; then
		# No Studio, browser, network or toolchain: stub what segments call.
		browser_root="${CICADA_STUDIO_DRY_ROOT:?set CICADA_STUDIO_DRY_ROOT}"
		base_node_options=
		studio_port=0
		export CICADA_STUDIO_URL=http://127.0.0.1:0
		node() { return 0; }
		go() { return 0; }
		python3() { return 0; }
		start_studio() { studio_pid=0; }
		stop_studio() { return 0; }
		start_synthetic_studio() { studio_pid=0; }
		stop_synthetic_studio() { return 0; }
		continuity_fixture() { mkdir -p "$browser_root/$1"; }
	else
		browser_root="$RUNNER_TEMP/cicada-studio-browser-$GITHUB_RUN_ID-$GITHUB_RUN_ATTEMPT-$shard"
		mkdir -p "$browser_root" build
		npm install --prefix "$browser_root" --no-save --ignore-scripts playwright@1.62.1
		export CICADA_PLAYWRIGHT_MODULE="$browser_root/node_modules/playwright"
		local tool_root="$RUNNER_TOOL_CACHE/chrome-for-testing/$CHROME_VERSION/linux64"
		export CICADA_CHROME_PATH="$tool_root/chrome-headless-shell-linux64/chrome-headless-shell"
		if [[ ! -x "$CICADA_CHROME_PATH" ]]; then
			mkdir -p "$tool_root"
			curl --fail --location --retry 3 --output "$browser_root/chrome.zip" "https://storage.googleapis.com/chrome-for-testing-public/$CHROME_VERSION/linux64/chrome-headless-shell-linux64.zip"
			unzip -q "$browser_root/chrome.zip" -d "$browser_root"
			mv "$browser_root/chrome-headless-shell-linux64" "$tool_root/"
		fi
		# A page that stops responding gets native stacks in its report. On a
		# GitHub-hosted runner gdb is installed only then, inside that failure
		# (page-stall.cjs); a self-hosted machine never installs packages.
		if [[ "${RUNNER_ENVIRONMENT:-}" == github-hosted ]]; then export CICADA_PAGE_STALL_NATIVE=install; fi
		base_node_options="${NODE_OPTIONS:-}"
		export NODE_OPTIONS="${NODE_OPTIONS:+$NODE_OPTIONS }--require=$PWD/workstation/browser/owner-session.cjs"
		studio_port="$(node -e 'const server=require("node:net").createServer(); server.listen(0,"127.0.0.1",()=>{process.stdout.write(String(server.address().port));server.close()})')"
		export CICADA_STUDIO_URL="http://127.0.0.1:$studio_port"
		trap 'kill "${studio_pid:-}" "${synthetic_pids[@]}" 2>/dev/null || true; wait "${studio_pid:-}" "${synthetic_pids[@]}" 2>/dev/null || true' EXIT
	fi
	mkdir -p "$browser_root" build
	song_root="$browser_root/song"
	ran_file="$browser_root/ran-suites.txt"
	: > "$ran_file"

	for seg in $segments; do
		echo "::group::segment $seg"
		"seg_$seg"
		echo "::endgroup::"
	done

	# A suite whose command was deleted or skipped leaves no record: fail.
	for seg in $segments; do
		for run in $(segment_runs "$seg"); do
			if ! grep -qxF -- "$run" "$ran_file"; then
				echo "FAIL: shard $shard did not run $run (segment $seg)" >&2
				missing=1
			fi
		done
	done
	if [[ "$missing" == 1 ]]; then return 1; fi
	echo "shard $shard ran all $(wc -l < "$ran_file") recorded suite runs"
}

case "${1:-}" in
run) run_shard "${2:?shard number}" ;;
list) shard_segments "${2:?shard number}" ;;
check) check_shards ;;
run-suite) # run-suite NAME COMMAND...: one suite under the time limit (used by the test)
	ran_file="${CICADA_STUDIO_RAN_FILE:-/dev/null}"
	shift
	run_suite "$@"
	;;
*)
	echo "usage: $0 run SHARD | list SHARD | check" >&2
	exit 2
	;;
esac
