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
# between shards. The "# suites:" comment under a segment names the suite
# files (workstation/browser/NAME.test.cjs) it runs; "check" compares those
# names with the files on disk.
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
	2) echo "pattern_song precision first_song gestures portable_song sound_context continuity modulation_source collaboration columns midi_pickup" ;;
	3) echo "owner_session pattern_grid tempo_editor recording_sources autoscroll arrangement_sources solo_clock mixer_strips navigation sampler_waveform export_sources library_recording midi_binding" ;;
	4) echo "shutdown_proof grid_sources shell direct_variation placement_variation editor performance_time startup tempo_clock export_stems chorus observation_clock" ;;
	*) echo "unknown shard: $1" >&2; return 1 ;;
	esac
}

# Suite files in workstation/browser that no segment runs. They did not run in
# the single step either; this list keeps the completeness check honest about
# them. Remove a name here when you add its suite to a segment.
not_run_by_ci=(
	amp-automation
	dock-fit
	live-write
	live-write-uncertain
	meter-performance
	performance
	recording-levels
	recording-setup
	shared-capture
	vocal-levels
	vocals
)

self="${BASH_SOURCE[0]}"

# --- check ----------------------------------------------------------------

check_shards() {
	local root problems=0 seg shard name file
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

	# Every suite named by a segment sits in exactly one shard. A suite may
	# run in several segments (several configurations) if they share a shard.
	for seg in "${defined[@]}"; do
		body="$(sed -n "/^seg_${seg}() {/,/^}/p" "$self")"
		for name in $(sed -n 's/^[[:space:]]*# suites: *//p' <<<"$body"); do
			declared[$name]=1
			shard="${seg_shard[$seg]:-}"
			if [[ -z "$shard" ]]; then continue; fi
			if [[ -n "${suite_shard[$name]:-}" && "${suite_shard[$name]}" != "$shard" ]]; then
				complain "suite $name runs in shard ${suite_shard[$name]} (${suite_segments[$name]}) and shard $shard ($seg)"
			fi
			suite_shard[$name]="$shard"
			suite_segments[$name]="${suite_segments[$name]:-} $seg"
		done
		# A suite the body runs with node --test must be declared.
		for name in $(grep -o -- '--test "\{0,1\}workstation/browser/[a-z0-9-]*\.test\.cjs' <<<"$body" | sed 's|.*/\([a-z0-9-]*\)\.test\.cjs|\1|'); do
			if ! sed -n 's/^[[:space:]]*# suites: *//p' <<<"$body" | tr ' ' '\n' | grep -qx "$name"; then
				complain "segment $seg runs $name but its '# suites:' comment does not name it"
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
}

# continuity_fixture NAME writes the continuity fixture score used by several suites.
continuity_fixture() {
	mkdir -p "$browser_root/$1"
	node workstation/browser/continuity.test.cjs --write-fixture "$browser_root/$1/continuity.cicada"
}

# --- segments ---------------------------------------------------------------
# Times are seconds in CI run 42717367458, including Studio start-up.

seg_owner_session() { # 1 s
	# suites: owner-session
	# Runs before NODE_OPTIONS loads the owner session helper for the others.
	NODE_OPTIONS="$base_node_options" node --test workstation/browser/owner-session.test.cjs
}

seg_shutdown_proof() { # 10 s
	# suites:
	python3 scripts/check-studio-shutdown.py --cicada build/cicada --output build/studio-shutdown-proof
}

seg_continuity() { # 8 s
	# suites: continuity
	continuity_fixture continuity
	start_studio "$browser_root/continuity/continuity.cicada" build/studio-continuity-server.log /
	make test-studio-continuity | tee build/studio-continuity-report.txt
	stop_studio
}

seg_modulation_source() { # 5 s
	# suites: modulation-source
	mkdir -p "$browser_root/modulation"
	export CICADA_MODULATION_SCORE="$browser_root/modulation/main.cicada"
	export CICADA_MODULATION_PROOF_DIR="$PWD/build/modulation-source-proof"
	node workstation/browser/modulation-source.test.cjs --write-fixture "$CICADA_MODULATION_SCORE"
	start_studio "$CICADA_MODULATION_SCORE" build/studio-modulation-server.log /
	make test-studio-modulation-source | tee build/studio-modulation-report.txt
	stop_studio
}

seg_chorus() { # 4 s
	# suites: chorus
	mkdir -p "$browser_root/chorus"
	export CICADA_CHORUS_SCORE="$browser_root/chorus/main.cicada"
	export CICADA_CHORUS_PROOF_DIR="$PWD/build/chorus-proof"
	node workstation/browser/chorus.test.cjs --write-fixture "$CICADA_CHORUS_SCORE"
	start_studio "$CICADA_CHORUS_SCORE" build/studio-chorus-server.log /
	make test-studio-chorus | tee build/studio-chorus-report.txt
	stop_studio
}

seg_pattern_machine() { # 5 s
	# suites: pattern-machine
	mkdir -p "$browser_root/pattern-machine"
	export CICADA_MACHINE_SCORE="$browser_root/pattern-machine/main.cicada"
	export CICADA_MACHINE_PROOF="$PWD/build/pattern-machine-proof"
	mkdir -p "$CICADA_MACHINE_PROOF"
	node workstation/browser/pattern-machine.test.cjs --write-fixture "$CICADA_MACHINE_SCORE"
	start_studio "$CICADA_MACHINE_SCORE" build/studio-pattern-machine-server.log /
	make test-studio-pattern-machine | tee build/studio-pattern-machine-report.txt
	stop_studio
}

seg_shell() { # 44 s
	# suites: shell
	continuity_fixture shell
	start_studio "$browser_root/shell/continuity.cicada" build/studio-shell-server.log /
	make test-studio-shell | tee build/studio-shell-report.txt
	stop_studio
}

seg_mixer_strips() { # 15 s
	# suites: mixer-strips
	continuity_fixture mixer-strips
	start_studio "$browser_root/mixer-strips/continuity.cicada" build/studio-mixer-strips-server.log /
	make test-studio-mixer-strips | tee build/studio-mixer-strips-report.txt
	stop_studio
}

seg_columns() { # 4 s
	# suites: columns
	continuity_fixture columns
	export CICADA_COLUMN_PROOF_DIR="$PWD/build/studio-column-proof"
	start_studio "$browser_root/columns/continuity.cicada" build/studio-column-server.log /
	make test-studio-columns | tee build/studio-column-report.txt
	stop_studio
}

seg_first_song() { # 22 s
	# suites: first-song
	mkdir -p "$song_root"
	local song_cli="$PWD/build/cicada" phase
	(cd "$song_root" && "$song_cli" new first-light)
	export CICADA_SONG_PROOF_DIR="$PWD/build/studio-song-proof"
	for phase in compose reopen; do
		start_studio "$song_root/first-light/main.cicada" "build/studio-song-$phase-server.log" /
		export CICADA_SONG_REOPEN=0
		if [[ "$phase" == reopen ]]; then export CICADA_SONG_REOPEN=1; fi
		make test-studio-first-song | tee "build/studio-song-$phase-report.txt"
		stop_studio
	done
	unset CICADA_SONG_REOPEN
}

seg_portable_song() { # 18 s
	# suites: portable-song
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
		make test-studio-portable-song | tee "build/studio-portable-$phase-report.txt"
		stop_studio
	done
	unset CICADA_PORTABLE_REOPEN
}

seg_code_recovery() { # 9 s
	# suites: code-recovery
	local phase
	cp -R examples/studio-project "$browser_root/code-recovery"
	export CICADA_CODE_RECOVERY_PROOF="$PWD/build/studio-code-recovery-proof"
	for phase in capture recover verify; do
		start_studio "$browser_root/code-recovery/main.cicada" "build/studio-code-recovery-$phase-server.log" /
		export CICADA_CODE_RECOVERY_PHASE="$phase"
		make test-studio-code-recovery | tee "build/studio-code-recovery-$phase-report.txt"
		stop_studio
	done
	unset CICADA_CODE_RECOVERY_PHASE
}

seg_export_sources() { # 5 s
	# suites: export-sources
	mkdir -p "$song_root"
	cp -R examples/studio-project "$song_root/export-sources"
	export CICADA_EXPORT_PROOF_DIR="$PWD/build/studio-export-proof"
	start_studio "$song_root/export-sources/main.cicada" build/studio-export-server.log /
	make test-studio-export-sources | tee build/studio-export-report.txt
	stop_studio
}

seg_export_stems() { # 5 s
	# suites: export-stems
	mkdir -p "$song_root"
	cp -R examples/studio-project "$song_root/export-stems"
	export CICADA_STEMS_PROOF_DIR="$PWD/build/studio-stems-proof"
	start_studio "$song_root/export-stems/main.cicada" build/studio-stems-server.log /
	make test-studio-export-stems | tee build/studio-stems-report.txt
	stop_studio
}

seg_collaboration() { # 5 s
	# suites: collaboration
	continuity_fixture collaboration
	start_studio "$browser_root/collaboration/continuity.cicada" build/studio-collaboration-server.log /
	make test-studio-collaboration | tee build/studio-collaboration-report.txt
	stop_studio
}

seg_placements() { # 24 s
	# suites: placements
	node workstation/browser/placements.test.cjs --write-fixture "$browser_root/placements.cicada"
	export CICADA_PLACEMENT_PROOF_DIR="$PWD/build/studio-placement-proof"
	export CICADA_PLACEMENT_BUILD="make build-workstation-release"
	start_studio "$browser_root/placements.cicada" build/studio-placement-server.log
	make test-studio-placements | tee build/studio-placement-report.txt
	stop_studio
}

seg_navigation() { # 12 s
	# suites: arrangement-navigation
	node workstation/browser/placements.test.cjs --write-fixture "$browser_root/navigation.cicada"
	start_studio "$browser_root/navigation.cicada" build/studio-navigation-server.log
	CICADA_ARRANGEMENT_PROOF_DIR="$PWD/build/studio-arrangement-proof" node --test workstation/browser/arrangement-navigation.test.cjs | tee build/studio-navigation-report.txt
	stop_studio
}

seg_gestures() { # 21 s
	# suites: placement-gestures
	node workstation/browser/placements.test.cjs --write-fixture "$browser_root/gestures.cicada"
	start_studio "$browser_root/gestures.cicada" build/studio-gestures-server.log
	CICADA_GESTURE_SCORE="$browser_root/gestures.cicada" CICADA_GESTURE_PROOF_DIR="$PWD/build/studio-gestures-proof" node --test workstation/browser/placement-gestures.test.cjs | tee build/studio-gestures-report.txt
	stop_studio
}

seg_precision() { # 30 s
	# suites: placement-precision
	node workstation/browser/placement-precision.test.cjs --write-fixture "$browser_root/precision.cicada"
	start_studio "$browser_root/precision.cicada" build/studio-precision-server.log
	CICADA_PRECISION_PROOF_DIR="$PWD/build/studio-precision-proof" node --test workstation/browser/placement-precision.test.cjs | tee build/studio-precision-report.txt
	stop_studio
}

seg_startup() { # 13 s
	# suites: startup
	node workstation/browser/placements.test.cjs --write-fixture "$browser_root/startup.cicada"
	start_studio "$browser_root/startup.cicada" build/studio-startup-server.log
	CICADA_STARTUP_PROOF_DIR="$PWD/build/studio-startup-proof" node --test workstation/browser/startup.test.cjs | tee build/studio-startup-report.txt
	stop_studio
}

seg_sampler_waveform() { # 6 s
	# suites: sampler-waveform
	node workstation/browser/sampler-waveform.test.cjs --write-fixture "$browser_root/sampler-waveform.cicada"
	start_studio "$browser_root/sampler-waveform.cicada" build/studio-sampler-waveform-server.log
	CICADA_SAMPLER_PROOF_DIR="$PWD/build/studio-sampler-waveform-proof" node --test workstation/browser/sampler-waveform.test.cjs | tee build/studio-sampler-waveform-report.txt
	stop_studio
}

seg_autoscroll() { # 27 s
	# suites: placement-autoscroll
	node workstation/browser/placement-autoscroll.test.cjs --write-fixture "$browser_root/autoscroll.cicada"
	start_studio "$browser_root/autoscroll.cicada" build/studio-autoscroll-server.log
	CICADA_AUTOSCROLL_PROOF_DIR="$PWD/build/studio-autoscroll-proof" node --test workstation/browser/placement-autoscroll.test.cjs | tee build/studio-autoscroll-report.txt
	stop_studio
}

seg_placement_commands() { # 13 s
	# suites: placement-commands
	node workstation/browser/placement-commands.test.cjs --write-fixture "$browser_root/placement-commands.cicada"
	start_studio "$browser_root/placement-commands.cicada" build/studio-placement-commands-server.log
	node --test workstation/browser/placement-commands.test.cjs | tee build/studio-placement-commands-report.txt
	stop_studio
}

seg_arrangement_sources() { # 22 s
	# suites: arrangement-sources
	local phase
	node workstation/browser/arrangement-sources.test.cjs --write-fixture "$browser_root/arrangement-sources" > build/studio-arrangement-sources-fixture.json
	export CICADA_ARRANGEMENT_FIXTURE="$PWD/build/studio-arrangement-sources-fixture.json"
	export CICADA_ARRANGEMENT_PROOF_DIR="$PWD/build/studio-arrangement-sources-proof"
	for phase in edit reopen; do
		start_studio "$browser_root/arrangement-sources/main.cicada" "build/studio-arrangement-sources-$phase-server.log"
		export CICADA_ARRANGEMENT_REOPEN=0
		if [[ "$phase" == reopen ]]; then export CICADA_ARRANGEMENT_REOPEN=1; fi
		node --test workstation/browser/arrangement-sources.test.cjs | tee "build/studio-arrangement-sources-$phase-report.txt"
		stop_studio
	done
	unset CICADA_ARRANGEMENT_REOPEN
}

seg_tempo_editor() { # 43 s
	# suites: tempo-editor
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
			node --test workstation/browser/tempo-editor.test.cjs | tee "build/studio-tempo-$tempo_layout-$tempo_phase-report.txt"
			stop_studio
		done
	done
	unset CICADA_TEMPO_REOPEN
}

seg_solo_clock() { # 19 s
	# suites: solo-clock
	node workstation/browser/solo-clock.test.cjs --write-fixture "$browser_root/solo-clock.cicada"
	start_studio "$browser_root/solo-clock.cicada" build/studio-solo-clock-server.log
	CICADA_SOLO_SCORE="$browser_root/solo-clock.cicada" CICADA_SOLO_PROOF_DIR="$PWD/build/studio-solo-clock-proof" node --test workstation/browser/solo-clock.test.cjs | tee build/studio-solo-clock-report.txt
	stop_studio
}

seg_portable_midi() { # 5 s
	# suites: portable-midi
	node workstation/browser/portable-midi.test.cjs --write-fixture "$browser_root/portable-midi"
	start_studio "$browser_root/portable-midi/main.cicada" build/studio-portable-midi-server.log
	CICADA_PORTABLE_MIDI_SCORE="$browser_root/portable-midi/main.cicada" CICADA_PORTABLE_MIDI_PROOF_DIR="$PWD/build/studio-portable-midi-proof" node --test workstation/browser/portable-midi.test.cjs | tee build/studio-portable-midi-report.txt
	stop_studio
}

seg_midi_pickup() { # 2 s
	# suites: midi-pickup
	node workstation/browser/midi-pickup.test.cjs --write-fixture "$browser_root/midi-pickup"
	start_studio "$browser_root/midi-pickup/main.cicada" build/studio-midi-pickup-server.log
	node --test workstation/browser/midi-pickup.test.cjs | tee build/studio-midi-pickup-report.txt
	stop_studio
}

seg_macro_controls() { # 3 s
	# suites: macro-controls
	node workstation/browser/macro-controls.test.cjs --write-fixture "$browser_root/macro-controls"
	start_studio "$browser_root/macro-controls/main.cicada" build/studio-macro-controls-server.log
	CICADA_MACRO_PROOF_DIR="$PWD/build/studio-macro-controls-proof" node --test workstation/browser/macro-controls.test.cjs | tee build/studio-macro-controls-report.txt
	stop_studio
}

seg_midi_binding() { # 3 s
	# suites: midi-binding
	# The fixture generator is the portable-midi one, as before.
	node workstation/browser/portable-midi.test.cjs --write-fixture "$browser_root/midi-binding"
	start_studio "$browser_root/midi-binding/main.cicada" build/studio-midi-binding-server.log
	CICADA_PORTABLE_MIDI_SCORE="$browser_root/midi-binding/main.cicada" CICADA_PORTABLE_MIDI_PROOF_DIR="$PWD/build/studio-midi-binding-proof" node --test workstation/browser/midi-binding.test.cjs | tee build/studio-midi-binding-report.txt
	stop_studio
}

seg_preset_save() { # 3 s
	# suites: preset-save
	node workstation/browser/preset-save.test.cjs --write-fixture "$browser_root/preset-save"
	start_studio "$browser_root/preset-save/main.cicada" build/studio-preset-save-server.log
	CICADA_PRESET_SCORE="$browser_root/preset-save/main.cicada" CICADA_PRESET_PROOF_DIR="$PWD/build/studio-preset-save-proof" node --test workstation/browser/preset-save.test.cjs | tee build/studio-preset-save-report.txt
	stop_studio
}

seg_observation_clock() { # 4 s
	# suites: observation-clock
	local observation_score
	go -C workstation run ./browser/testdata/print-sources --primary-arrangement > build/studio-observation-clock-fixture.json
	export CICADA_OBSERVATION_FIXTURE="$PWD/build/studio-observation-clock-fixture.json"
	observation_score="$(node -e 'process.stdout.write(require(process.argv[1]).score)' "$CICADA_OBSERVATION_FIXTURE")"
	start_studio "$observation_score" build/studio-observation-clock-server.log
	CICADA_OBSERVATION_PROOF_DIR="$PWD/build/studio-observation-clock-proof" node --test workstation/browser/observation-clock.test.cjs | tee build/studio-observation-clock-report.txt
	stop_studio
}

seg_tempo_clock() { # 7 s
	# suites: tempo-clock
	node workstation/browser/tempo-clock.test.cjs --write-fixture "$browser_root/tempo-clock.cicada"
	start_studio "$browser_root/tempo-clock.cicada" build/studio-tempo-clock-server.log
	CICADA_TEMPO_CLOCK_PROOF_DIR="$PWD/build/studio-tempo-clock-proof" node --test workstation/browser/tempo-clock.test.cjs | tee build/studio-tempo-clock-report.txt
	stop_studio
}

seg_library_recording() { # 5 s
	# suites: library-recording
	node workstation/browser/library-recording.test.cjs --write-fixture "$browser_root/library-recording.cicada"
	start_studio "$browser_root/library-recording.cicada" build/studio-library-recording-server.log
	CICADA_LIBRARY_RECORDING_PROOF_DIR="$PWD/build/studio-library-recording-proof" node --test workstation/browser/library-recording.test.cjs | tee build/studio-library-recording-report.txt
	stop_studio
}

seg_pattern_song() { # 132 s
	# suites: pattern-song
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
			CICADA_PATTERN_SONG_PROOF_DIR="$PWD/build/studio-pattern-song-$song_layout-proof" node --test workstation/browser/pattern-song.test.cjs | tee "build/studio-pattern-song-$song_layout-$song_phase-report.txt"
			stop_studio
		done
	done
}

seg_pattern_grid() { # 65 s
	# suites: pattern-grid
	local grid_phase
	node workstation/browser/pattern-grid.test.cjs --write-fixture "$browser_root/pattern-grid.cicada"
	for grid_phase in journey reopen; do
		start_studio "$browser_root/pattern-grid.cicada" "build/studio-pattern-grid-$grid_phase-server.log"
		CICADA_PATTERN_GRID_REOPEN=0
		if [[ "$grid_phase" == reopen ]]; then CICADA_PATTERN_GRID_REOPEN=1; fi
		export CICADA_PATTERN_GRID_REOPEN
		CICADA_PATTERN_GRID_PROOF_DIR="$PWD/build/studio-pattern-grid-proof" node --test workstation/browser/pattern-grid.test.cjs | tee "build/studio-pattern-grid-$grid_phase-report.txt"
		stop_studio
	done
}

seg_placement_variation() { # 27 s
	# suites: placement-variation
	local variation_phase
	go -C workstation run ./browser/testdata/placement-variation "$browser_root/placement-variation" > build/studio-placement-variation-fixture.json
	for variation_phase in journey reopen; do
		start_studio "$browser_root/placement-variation/main.cicada" "build/studio-placement-variation-$variation_phase-server.log"
		CICADA_VARIATION_REOPEN=0
		if [[ "$variation_phase" == reopen ]]; then CICADA_VARIATION_REOPEN=1; fi
		export CICADA_VARIATION_REOPEN
		CICADA_VARIATION_PROOF_DIR="$PWD/build/studio-placement-variation-proof" node --test workstation/browser/placement-variation.test.cjs | tee "build/studio-placement-variation-$variation_phase-report.txt"
		stop_studio
	done
}

seg_performance_time() { # 19 s
	# suites: performance-time
	local time_phase
	node workstation/browser/performance-time.test.cjs --write-fixture "$browser_root/performance-time" > build/studio-performance-time-fixture.json
	for time_phase in journey reopen; do
		start_studio "$browser_root/performance-time/main.cicada" "build/studio-performance-time-$time_phase-server.log"
		CICADA_TIME_REOPEN=0
		if [[ "$time_phase" == reopen ]]; then CICADA_TIME_REOPEN=1; fi
		export CICADA_TIME_REOPEN
		CICADA_TIME_PROOF_DIR="$PWD/build/studio-performance-time-proof" node --test workstation/browser/performance-time.test.cjs | tee "build/studio-performance-time-$time_phase-report.txt"
		if [[ "$time_phase" == journey ]]; then stop_studio; fi
	done
	python3 workstation/browser/performance-time-audio.py --cicada build/cicada --proof build/studio-performance-time-proof
	stop_studio
}

seg_direct_variation() { # 40 s
	# suites: direct-variation
	local direct_phase
	go -C workstation run ./browser/testdata/direct-variation "$browser_root/direct-variation" > build/studio-direct-variation-fixture.json
	for direct_phase in journey reopen; do
		start_studio "$browser_root/direct-variation/main.cicada" "build/studio-direct-variation-$direct_phase-server.log"
		CICADA_DIRECT_REOPEN=0
		if [[ "$direct_phase" == reopen ]]; then CICADA_DIRECT_REOPEN=1; fi
		export CICADA_DIRECT_REOPEN
		CICADA_DIRECT_PROOF_DIR="$PWD/build/studio-direct-variation-proof" node --test workstation/browser/direct-variation.test.cjs | tee "build/studio-direct-variation-$direct_phase-report.txt"
		if [[ "$direct_phase" == journey ]]; then stop_studio; fi
	done
	python3 workstation/browser/direct-variation-audio.py --cicada build/cicada --proof build/studio-direct-variation-proof --fixture build/studio-direct-variation-fixture.json
	stop_studio
}

seg_sound_context() { # 13 s
	# suites: sound-context
	local context_phase
	node workstation/browser/sound-context.test.cjs --write-fixture "$browser_root/sound-context" > build/studio-sound-context-fixture.json
	for context_phase in journey reopen; do
		start_studio "$browser_root/sound-context/main.cicada" "build/studio-sound-context-$context_phase-server.log"
		CICADA_CONTEXT_REOPEN=0
		if [[ "$context_phase" == reopen ]]; then CICADA_CONTEXT_REOPEN=1; fi
		export CICADA_CONTEXT_REOPEN
		CICADA_CONTEXT_PROOF_DIR="$PWD/build/studio-sound-context-proof" node --test workstation/browser/sound-context.test.cjs | tee "build/studio-sound-context-$context_phase-report.txt"
		stop_studio
	done
}

seg_performance_pitch() { # 20 s
	# suites: performance-pitch
	local pitch_phase
	node workstation/browser/performance-pitch.test.cjs --write-fixture "$browser_root/performance-pitch" > build/studio-performance-pitch-fixture.json
	for pitch_phase in journey reopen; do
		start_studio "$browser_root/performance-pitch/main.cicada" "build/studio-performance-pitch-$pitch_phase-server.log"
		CICADA_PITCH_REOPEN=0
		if [[ "$pitch_phase" == reopen ]]; then CICADA_PITCH_REOPEN=1; fi
		export CICADA_PITCH_REOPEN
		CICADA_PITCH_PROOF_DIR="$PWD/build/studio-performance-pitch-proof" node --test workstation/browser/performance-pitch.test.cjs | tee "build/studio-performance-pitch-$pitch_phase-report.txt"
		if [[ "$pitch_phase" == journey ]]; then stop_studio; fi
	done
	python3 workstation/browser/performance-pitch-audio.py --cicada build/cicada --proof build/studio-performance-pitch-proof
	stop_studio
}

seg_performance_sources() { # 17 s
	# suites: performance-sources
	local sources_phase
	node workstation/browser/performance-sources.test.cjs --write-fixture "$browser_root/performance-sources" > build/studio-performance-sources-fixture.json
	export CICADA_SOURCES_FIXTURE="$PWD/build/studio-performance-sources-fixture.json"
	for sources_phase in journey reopen; do
		start_studio "$browser_root/performance-sources/main.cicada" "build/studio-performance-sources-$sources_phase-server.log"
		CICADA_SOURCES_REOPEN=0
		if [[ "$sources_phase" == reopen ]]; then CICADA_SOURCES_REOPEN=1; fi
		export CICADA_SOURCES_REOPEN
		CICADA_SOURCES_PROOF_DIR="$PWD/build/studio-performance-sources-proof" node --test workstation/browser/performance-sources.test.cjs | tee "build/studio-performance-sources-$sources_phase-report.txt"
		if [[ "$sources_phase" == journey ]]; then stop_studio; fi
	done
	python3 workstation/browser/performance-sources-audio.py --cicada build/cicada --proof build/studio-performance-sources-proof
	stop_studio
}

seg_recording_sources() { # 36 s
	# suites: recording-sources
	local record_sources_phase
	node workstation/browser/recording-sources.test.cjs --write-fixture "$browser_root/recording-sources" > build/studio-recording-sources-fixture.json
	export CICADA_RECORD_SOURCES_FIXTURE="$PWD/build/studio-recording-sources-fixture.json"
	for record_sources_phase in journey reopen; do
		start_studio "$browser_root/recording-sources/main.cicada" "build/studio-recording-sources-$record_sources_phase-server.log"
		CICADA_RECORD_SOURCES_REOPEN=0
		if [[ "$record_sources_phase" == reopen ]]; then CICADA_RECORD_SOURCES_REOPEN=1; fi
		export CICADA_RECORD_SOURCES_REOPEN
		CICADA_RECORD_SOURCES_PROOF_DIR="$PWD/build/studio-recording-sources-proof" node --test workstation/browser/recording-sources.test.cjs | tee "build/studio-recording-sources-$record_sources_phase-report.txt"
		if [[ "$record_sources_phase" == journey ]]; then stop_studio; fi
	done
	python3 workstation/browser/recording-sources-audio.py --cicada build/cicada --proof build/studio-recording-sources-proof
	stop_studio
}

seg_grid_sources() { # 63 s
	# suites: grid-sources
	local grid_sources_phase
	node workstation/browser/grid-sources.test.cjs --write-fixture "$browser_root/grid-sources" > build/studio-grid-sources-fixture.json
	export CICADA_GRID_SOURCES_FIXTURE="$PWD/build/studio-grid-sources-fixture.json"
	for grid_sources_phase in journey reopen; do
		start_studio "$browser_root/grid-sources/main.cicada" "build/studio-grid-sources-$grid_sources_phase-server.log"
		CICADA_GRID_SOURCES_REOPEN=0
		if [[ "$grid_sources_phase" == reopen ]]; then CICADA_GRID_SOURCES_REOPEN=1; fi
		export CICADA_GRID_SOURCES_REOPEN
		CICADA_GRID_SOURCES_PROOF_DIR="$PWD/build/studio-grid-sources-proof" node --test workstation/browser/grid-sources.test.cjs | tee "build/studio-grid-sources-$grid_sources_phase-report.txt"
		if [[ "$grid_sources_phase" == journey ]]; then stop_studio; fi
	done
	python3 workstation/browser/grid-sources-audio.py --cicada build/cicada --proof build/studio-grid-sources-proof
	stop_studio
}

seg_print_sources() { # 162 s
	# suites: sampler producer-workflow print-roundtrip automation-recall tempo-arrangement
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
			node --test "workstation/browser/$print_test.test.cjs" | tee "build/studio-print-$print_layout-$print_phase-report.txt"
			stop_studio
		done
		unset CICADA_SAMPLER_ARRANGEMENT_SOURCE CICADA_PRODUCER_ARRANGEMENT_SOURCE CICADA_PRINT_REOPEN
	done
}

seg_editor() { # 22 s
	# suites: code-editor
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
		node --test workstation/browser/code-editor.test.cjs | tee "build/studio-editor-$editor_mode-report.txt"
		stop_studio
	done
}

# --- run ------------------------------------------------------------------

run_shard() {
	local shard="$1" seg
	segments="$(shard_segments "$shard")"

	browser_root="$RUNNER_TEMP/cicada-studio-browser-$GITHUB_RUN_ID-$GITHUB_RUN_ATTEMPT-$shard"
	mkdir -p "$browser_root" build
	song_root="$browser_root/song"
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
	base_node_options="${NODE_OPTIONS:-}"
	export NODE_OPTIONS="${NODE_OPTIONS:+$NODE_OPTIONS }--require=$PWD/workstation/browser/owner-session.cjs"

	studio_port="$(node -e 'const server=require("node:net").createServer(); server.listen(0,"127.0.0.1",()=>{process.stdout.write(String(server.address().port));server.close()})')"
	export CICADA_STUDIO_URL="http://127.0.0.1:$studio_port"
	trap 'kill "${studio_pid:-}" 2>/dev/null || true; wait "${studio_pid:-}" 2>/dev/null || true' EXIT

	for seg in $segments; do
		echo "::group::segment $seg"
		"seg_$seg"
		echo "::endgroup::"
	done
}

case "${1:-}" in
run) run_shard "${2:?shard number}" ;;
list) shard_segments "${2:?shard number}" ;;
check) check_shards ;;
*)
	echo "usage: $0 run SHARD | list SHARD | check" >&2
	exit 2
	;;
esac
