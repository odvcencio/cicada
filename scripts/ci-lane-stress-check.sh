#!/usr/bin/env bash
# Checks the log of `make test-lane-stress`: the lanestress hook was built in
# (each test binary prints its seed), and every stress test ran and passed.
# go test exits 0 when -run matches nothing, for example after a rename, so
# each test must have a PASS line.
#
#   scripts/ci-lane-stress-check.sh build/lane-stress.log
set -euo pipefail

log="${1:?usage: $0 <make test-lane-stress log>}"
status=0
if ! grep -q 'renderlanes: lane stress seed' "$log"; then
	echo "$log: no lane stress seed line: the lanestress build tag was not applied" >&2
	status=1
fi
for name in \
	TestRunRendersEveryTrackOnce TestLaneCountChangesBetweenSegments TestForcedLanesRenderEachSegmentOnce \
	TestPanicInALaneReachesTheCaller TestPanicInTheCallersLane \
	TestOfflineParallelRenderIsBitIdentical TestOfflineParallelFaultMatchesSerial \
	TestDirectLanesAreBitIdentical TestDirectLanesFaultMatchesSerial TestAudioClipLanesAreBitIdentical \
	TestParallelRenderIdentity; do
	if ! grep -q -- "^--- PASS: $name " "$log"; then
		echo "$log: no PASS line for $name" >&2
		status=1
	fi
done
if grep -q -- '--- FAIL' "$log"; then
	echo "$log: a test failed; its lane stress seed is printed above it" >&2
	status=1
fi
exit "$status"
