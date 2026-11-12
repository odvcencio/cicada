package main

import (
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"m31labs.dev/cicada/render"
)

// TestCheckedExamplesRender renders every example score that cicada check
// accepts, and fails when render refuses one: what check accepts, render must
// play. An agent validates, edits and validates again before it renders, so a
// score that only the render refuses costs it the whole loop.
//
// Each score renders its first bar at 48 kHz. That runs every refusal render
// makes before it writes audio: the compile, the asset, pack and sampler
// checks, the voices and effects it builds. The nightly WAV parity sweep
// (TestExampleWAVParity with CICADA_WAV_PARITY_ALL=1) renders every song whole
// and fails the same way, which also covers what can refuse while a song
// plays: scene and chain changes and engine faults. Scores that check refuses,
// such as the sampled keys whose packs are downloaded on demand, are skipped
// and counted.
func TestCheckedExamplesRender(t *testing.T) {
	root := repositoryRoot()
	scores := exampleScores(t, root)
	var mu sync.Mutex
	rendered, refused := 0, 0
	t.Run("examples", func(t *testing.T) {
		for _, score := range scores {
			t.Run(strings.ReplaceAll(score, "/", "_"), func(t *testing.T) {
				t.Parallel()
				path := filepath.Join(root, filepath.FromSlash(score))
				if report := checkScoreJSON(path, checkOptions{profile: profileDesktop}); !report.Valid {
					mu.Lock()
					refused++
					mu.Unlock()
					t.Skipf("check refuses %s: %+v", score, report.Diagnostics)
				}
				inspection, err := inspectScore(path)
				if err != nil {
					t.Fatal(err)
				}
				opts := render.Options{SampleRate: 48_000, Bits: 24, Bars: 1, AssetRoot: inspection.root(path), AssetDir: inspection.root(path)}
				if _, err := render.WAV(inspection.score, opts, io.Discard); err != nil {
					t.Errorf("cicada check accepts %s, and render refuses it: %v", score, err)
				}
				mu.Lock()
				rendered++
				mu.Unlock()
			})
		}
	})
	t.Logf("%d example scores: %d accepted by check and rendered, %d refused by check", len(scores), rendered, refused)
	// The sampled keys need packs that are downloaded on demand; every other
	// example must pass check, so a check that wrongly refuses scores cannot
	// make this test pass by rendering nothing.
	if rendered < len(scores)-30 {
		t.Fatalf("check accepted only %d of %d examples", rendered, len(scores))
	}
}
