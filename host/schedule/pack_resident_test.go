package schedule_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/instrumentpack"
	"m31labs.dev/cicada/host/schedule"
	"m31labs.dev/cicada/internal/testpack"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// packProject declares one sampler and one track for each pack, all playing the
// same one-note pattern.
func packProject(t *testing.T, packs ...testpack.Pack) *project.Project {
	t.Helper()
	var source strings.Builder
	source.WriteString("cicada 2\ntempo 120\n")
	for i, pack := range packs {
		source.WriteString(pack.Sampler(fmt.Sprintf("s%d", i), 2))
		fmt.Fprintf(&source, "track t%d s%d {}\n", i, i)
	}
	source.WriteString("pattern hit notes steps=4 { c4 . . . }\nscene main {")
	for i := range packs {
		fmt.Fprintf(&source, " t%d = hit", i)
	}
	source.WriteString(" }\nsong { main }\n")
	score, ds := notation.Parse([]byte(source.String()))
	p, more := project.FromScore(score)
	if p == nil {
		t.Fatalf("fixture: %+v %+v\n%s", ds, more, source.String())
	}
	return p
}

// threeMonoAssets decodes to 72,000,000 bytes of float32 PCM, above the 64 MiB
// resident budget (67,108,864 bytes) and below the 256 MiB limit of one pack.
func threeMonoAssets() testpack.Options {
	return testpack.Options{ID: "big", Assets: 3, Frames: 6_000_000, Zones: []testpack.Zone{{KeyLow: 48, KeyHigh: 59}, {KeyLow: 60, KeyHigh: 71}, {KeyLow: 72, KeyHigh: 83}}}
}

func TestNativeCompileHoldsAPackAboveTheResidentBudgetAndTheBrowserPathRefusesIt(t *testing.T) {
	dir := t.TempDir()
	pack := testpack.Write(t, dir, threeMonoAssets())
	if pack.PCMBytes != 72_000_000 || pack.PCMBytes <= 64<<20 {
		t.Fatalf("fixture holds %d bytes, want 72000000 (above 64 MiB)", pack.PCMBytes)
	}
	p := packProject(t, pack)

	cfg, err := schedule.Compile(p, dir, 48000, 128)
	if err != nil {
		t.Fatalf("native compile refused a pack of %d bytes: %v", pack.PCMBytes, err)
	}
	if _, ok := cfg.Track[0].Prepared.(*engine.PackFactory); !ok || cfg.Track[0].Kind != engine.VoicePrepared {
		t.Fatalf("track 0 is not backed by the pack: %+v", cfg.Track[0])
	}
	if _, err = schedule.CompileResident(p, dir, 48000, 128); err == nil || !strings.Contains(err.Error(), "64 MiB") {
		t.Fatalf("the browser image path must keep the 64 MiB rule: %v", err)
	}
}

func TestNativeCompileRefusesPacksAboveTheNamedCapWithTheLimitAndTheSize(t *testing.T) {
	dir := t.TempDir()
	pack := testpack.Write(t, dir, threeMonoAssets())
	p := packProject(t, pack)
	const limit = 48 << 20
	_, err := schedule.CompileWithOptions(p, dir, 48000, 128, schedule.Options{MaxResidentPCM: limit})
	var over *instrumentpack.ResidentPCMError
	if !errors.As(err, &over) || over.Limit != limit || over.Bytes != pack.PCMBytes || over.Pack != pack.Manifest {
		t.Fatalf("want a *ResidentPCMError for %s, %d of %d bytes: %#v", pack.Manifest, pack.PCMBytes, limit, err)
	}
	for _, want := range []string{"--max-resident-pcm", "50331648 bytes", "72000000 bytes", pack.Manifest} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal lacks %q: %v", want, err)
		}
	}
	// The cap replaces the 64 MiB rule for packs: raising it above the pack admits it.
	if _, err = schedule.CompileWithOptions(p, dir, 48000, 128, schedule.Options{MaxResidentPCM: 128 << 20}); err != nil {
		t.Fatalf("a 128 MiB cap refused a 72 MB pack: %v", err)
	}
}

func TestNativeCapAllowsExactlyItsSizeAndTalliesEveryPack(t *testing.T) {
	dir := t.TempDir()
	first := testpack.Write(t, dir, testpack.Options{ID: "first", Frames: 1000})
	second := testpack.Write(t, dir, testpack.Options{ID: "second", Frames: 1000})
	if first.PCMBytes != 4000 || second.PCMBytes != 4000 {
		t.Fatalf("fixture sizes %d and %d, want 4000 each", first.PCMBytes, second.PCMBytes)
	}
	one, both := packProject(t, first), packProject(t, first, second)

	for _, tc := range []struct {
		name    string
		p       *project.Project
		limit   int64
		refused bool
	}{
		{"one pack at the cap", one, 4000, false},
		{"one pack a byte over", one, 3999, true},
		{"two packs at the cap", both, 8000, false},
		{"two packs a byte over", both, 7999, true},
		{"the second pack crosses a cap the first fits", both, 6000, true},
	} {
		_, err := schedule.CompileWithOptions(tc.p, dir, 48000, 128, schedule.Options{MaxResidentPCM: tc.limit})
		if tc.refused != (err != nil) {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
	_, err := schedule.CompileWithOptions(both, dir, 48000, 128, schedule.Options{MaxResidentPCM: 6000})
	var over *instrumentpack.ResidentPCMError
	if !errors.As(err, &over) || over.Pack != second.Manifest || over.Bytes != 8000 || over.Limit != 6000 {
		t.Fatalf("the refusal must name the pack that crossed the cap and the total measured: %#v", err)
	}
}

func TestNativeCompileDefaultsToTheOneGiBCapAndRefusesNegativeCaps(t *testing.T) {
	dir := t.TempDir()
	p := packProject(t, testpack.Write(t, dir, testpack.Options{}))
	if _, err := schedule.CompileWithOptions(p, dir, 48000, 128, schedule.Options{}); err != nil {
		t.Fatalf("the default cap refused a 4 KiB pack: %v", err)
	}
	if _, err := schedule.CompileWithOptions(p, dir, 48000, 128, schedule.Options{MaxResidentPCM: -1}); err == nil {
		t.Fatal("a negative cap was accepted")
	}
}
