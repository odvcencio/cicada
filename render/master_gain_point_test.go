package render

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/internal/testwav"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// convolutionCacheScore is masterCacheScore whose master chain puts a
// convolution ahead of the last limiter, loaded from a folder that holds its
// impulse, on the direct or the scheduled renderer.
func convolutionCacheScore(t *testing.T, scheduled bool) *notation.Score {
	t.Helper()
	dir := t.TempDir()
	wav := testwav.Bytes(48000, 1, 16, 32, 1)
	if err := os.WriteFile(filepath.Join(dir, "impulse.wav"), wav, 0o600); err != nil {
		t.Fatal(err)
	}
	master := fmt.Sprintf(`asset room-ir "impulse.wav" { sha256 = "%x" format = wav frames = 32 rate = 48000Hz channels = 1 }
fx room convolution { asset = room-ir mix = 0.5 partition = 512frames }
fx ceiling limiter { ceiling = -1dBTP }
master { insert = room -> ceiling }`, sha256.Sum256(wav))
	source := "cicada 2\n" + strings.Replace(masterCacheScore, "MASTER_FX", master, 1)
	if scheduled {
		for i := 0; i+1 < len(scheduledCacheReplacements); i += 2 {
			source = strings.Replace(source, scheduledCacheReplacements[i], scheduledCacheReplacements[i+1], 1)
		}
	}
	path := filepath.Join(dir, "score.cicada")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	score, diagnostics, err := project.LoadScore(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range diagnostics {
		if d.Severity == "error" {
			t.Fatal(d)
		}
	}
	if got := UsesScheduledEngine(score, Options{}); got != scheduled {
		t.Fatalf("UsesScheduledEngine = %v, want %v", got, scheduled)
	}
	return score
}
