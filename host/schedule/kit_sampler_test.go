package schedule_test

import (
	"strings"
	"testing"

	"m31labs.dev/cicada/host/schedule"
	"m31labs.dev/cicada/internal/testpack"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// kitOnlyProject binds two lanes of a kit to one pack sampler that no track
// plays directly.
func kitOnlyProject(t *testing.T, pack testpack.Pack) *project.Project {
	t.Helper()
	source := "cicada 2\ntempo 120\n" + pack.Sampler("shells", 4) +
		"kit rec { sd = shells.d2\n rs = shells.c#2 }\ntrack drums rec {}\n" +
		"pattern p drums steps=4 { sd: x... rs: ..x. }\nscene main { drums = p }\nsong { main }\n"
	score, ds := notation.Parse([]byte(source))
	p, more := project.FromScore(score)
	if p == nil {
		t.Fatalf("fixture: %+v %+v\n%s", ds, more, source)
	}
	return p
}

func TestKitOnlySamplerLoadsOnceAndLanesShareThePack(t *testing.T) {
	dir := t.TempDir()
	pack := testpack.Write(t, dir, testpack.Options{Frames: 1000, Zones: []testpack.Zone{{KeyLow: 37, KeyHigh: 38}}})
	p := kitOnlyProject(t, pack)
	cfg, err := schedule.CompileWithOptions(p, dir, 48000, 128, schedule.Options{MaxResidentPCM: pack.PCMBytes})
	if err != nil {
		t.Fatalf("a cap equal to one pack's PCM refused a kit that uses it twice: %v", err)
	}
	kit := cfg.Track[0].Kit
	sd, rs := kit[drum.SD], kit[drum.RS]
	if sd.Kind != engine.KitLaneSampler || sd.Pack == nil || sd.Pack != rs.Pack {
		t.Fatalf("lanes do not share one loaded pack: %+v %+v", sd, rs)
	}
	if _, err = schedule.CompileWithOptions(p, dir, 48000, 128, schedule.Options{MaxResidentPCM: pack.PCMBytes - 1}); err == nil {
		t.Fatal("a cap under the pack's PCM admitted a kit-only sampler")
	}
}

func TestResidentCompileCountsAKitOnlySampler(t *testing.T) {
	dir := t.TempDir()
	pack := testpack.Write(t, dir, threeMonoAssets())
	p := kitOnlyProject(t, pack)
	if _, err := schedule.CompileResident(p, dir, 48000, 128); err == nil || !strings.Contains(err.Error(), "64 MiB") {
		t.Fatalf("the browser path must count a kit-only pack against its budget: %v", err)
	}
}

// Several kit tracks on one pack each reserve the sampler's whole voice pool,
// so the compiled capacity must cover them or engine.New refuses the project.
func TestKitSamplerTracksFitTheCompiledVoiceCapacity(t *testing.T) {
	dir := t.TempDir()
	pack := testpack.Write(t, dir, testpack.Options{Frames: 1000, Zones: []testpack.Zone{{KeyLow: 36, KeyHigh: 38}}})
	source := "cicada 2\ntempo 120\n" + pack.Sampler("shells", 12) +
		"kit a { bd = shells.c2 }\nkit b { sd = shells.d2 }\nkit c { rs = shells.c#2 }\n" +
		"track t1 a {}\ntrack t2 b {}\ntrack t3 c {}\n" +
		"pattern p drums steps=4 { bd: x... sd: ..x. rs: .x.. }\nscene main { t1 = p t2 = p t3 = p }\nsong { main }\n"
	score, ds := notation.Parse([]byte(source))
	p, more := project.FromScore(score)
	if p == nil {
		t.Fatalf("fixture: %+v %+v\n%s", ds, more, source)
	}
	cfg, err := schedule.CompileWithOptions(p, dir, 48000, 128, schedule.Options{MaxResidentPCM: pack.PCMBytes})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxVoices < 36 {
		t.Fatalf("capacity %d does not cover three 12-voice kit pools", cfg.MaxVoices)
	}
	if _, err := engine.New(cfg); err != nil {
		t.Fatalf("engine refused the compiled kit tracks: %v", err)
	}
}
