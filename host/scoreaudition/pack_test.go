package scoreaudition

import (
	"errors"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/instrumentpack"
	"m31labs.dev/cicada/internal/testpack"
	"m31labs.dev/cicada/notation"
)

func packScore(t *testing.T, frames int) (*notation.Score, string, testpack.Pack) {
	t.Helper()
	dir := t.TempDir()
	pack := testpack.Write(t, dir, testpack.Options{Frames: frames})
	source := "cicada 2\ntempo 120\n" + pack.Sampler("sound", 2) + "track lead sound {}\npattern melody notes steps=16 { c4 . e4 . g4 . e4 . c4 . e4 . g4 . e4 . }\nscene main { lead = melody }\nsong { main*2 }\n"
	score, ds := notation.Parse([]byte(source))
	for _, d := range ds {
		if d.Severity == "error" {
			t.Fatalf("parse: %s", d.Error())
		}
	}
	t.Cleanup(instrumentpack.ReleaseShared)
	return score, dir, pack
}

// The analysis compiles the score to learn its track kinds and renders it for
// the stems. Both read the pack, and the process decodes it once.
func TestAnalyzeDecodesEachSamplerPackOnce(t *testing.T) {
	score, dir, _ := packScore(t, 4000)
	before := instrumentpack.Decodes()
	if _, err := Analyze(score, Options{AssetDir: dir, TempDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if got := instrumentpack.Decodes() - before; got != 1 {
		t.Fatalf("an analysis decoded its pack %d times, want 1", got)
	}
}

func TestAnalyzeAppliesTheResidentPCMCap(t *testing.T) {
	score, dir, pack := packScore(t, 4000) // 16,000 bytes
	if _, err := Analyze(score, Options{AssetDir: dir, TempDir: t.TempDir(), MaxResidentPCM: pack.PCMBytes}); err != nil {
		t.Fatalf("a cap equal to the pack refused it: %v", err)
	}
	_, err := Analyze(score, Options{AssetDir: dir, TempDir: t.TempDir(), MaxResidentPCM: pack.PCMBytes - 1})
	var over *instrumentpack.ResidentPCMError
	if !errors.As(err, &over) || over.Limit != pack.PCMBytes-1 || !strings.Contains(err.Error(), "--max-resident-pcm") {
		t.Fatalf("a cap a byte below the pack: %v", err)
	}
}
