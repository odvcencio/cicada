package schedule_test

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/instrumentpack"
	"m31labs.dev/cicada/host/schedule"
	"m31labs.dev/cicada/internal/testpack"
	"m31labs.dev/cicada/internal/testwav"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// allocated is the heap that f allocated, in bytes, whether or not it is still
// alive afterwards. A compile that decodes a pack allocates at least its PCM.
func allocated(f func()) uint64 {
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	f()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// pcmAddress is where the first sample of the first zone of the track's pack
// sits. Two scores that share a decoded pack have the same address.
func pcmAddress(t *testing.T, cfg engine.Config, track int) *float32 {
	t.Helper()
	pack, ok := cfg.Track[track].Prepared.(*engine.PackFactory)
	if !ok {
		t.Fatalf("track %d is not backed by a pack: %+v", track, cfg.Track[track])
	}
	return &pack.Zones[0].Region.Left[0]
}

// audioFile is the first audio file of a pack that testpack wrote.
func audioFile(dir string, pack testpack.Pack) string {
	return filepath.Join(dir, filepath.Dir(pack.Manifest), "samples", "take0.wav.gz")
}

// Play reloads, Studio requests and auditions compile the same project over and
// over. They share one decoded copy of its packs.
func TestNativeCompilesOfOnePackShareOneDecodedCopy(t *testing.T) {
	dir := t.TempDir()
	pack := testpack.Write(t, dir, testpack.Options{ID: "shared", Frames: 8_000_000}) // 32 MB of PCM
	p := packProject(t, pack)
	t.Cleanup(instrumentpack.ReleaseShared)

	var first, second, otherRate engine.Config
	var err error
	firstBytes := allocated(func() { first, err = schedule.Compile(p, dir, 48000, 128) })
	if err != nil {
		t.Fatal(err)
	}
	secondBytes := allocated(func() { second, err = schedule.Compile(p, dir, 48000, 128) })
	if err != nil {
		t.Fatal(err)
	}
	if firstBytes < uint64(pack.PCMBytes) {
		t.Fatalf("fixture: the first compile allocated %d bytes for %d bytes of PCM", firstBytes, pack.PCMBytes)
	}
	// Every compile allocates a megabyte or two of its own for the engine
	// configuration, so the test looks for the audio, not for zero.
	if secondBytes > uint64(pack.PCMBytes)/8 {
		t.Errorf("the second compile allocated %d bytes for a pack of %d bytes: it decoded the pack again", secondBytes, pack.PCMBytes)
	}
	if pcmAddress(t, first, 0) != pcmAddress(t, second, 0) {
		t.Error("two compiles of one project hold separate copies of the PCM")
	}
	// A device at 44.1 kHz plays the same recording; only the rate check differs.
	if otherRate, err = schedule.Compile(p, dir, 44100, 128); err != nil {
		t.Fatal(err)
	}
	if pcmAddress(t, first, 0) != pcmAddress(t, otherRate, 0) {
		t.Error("a compile at another output rate decoded the pack again")
	}
}

// Two samplers that name one pack play the same recording, so the process holds
// it once and the cap counts it once.
func TestNativeCompileCountsAPackOnceWhenSeveralSamplersNameIt(t *testing.T) {
	dir := t.TempDir()
	pack := testpack.Write(t, dir, testpack.Options{ID: "twice", Frames: 1000}) // 4000 bytes
	p := packProject(t, pack, pack)
	t.Cleanup(instrumentpack.ReleaseShared)

	cfg, err := schedule.CompileWithOptions(p, dir, 48000, 128, schedule.Options{MaxResidentPCM: 4000})
	if err != nil {
		t.Fatalf("a cap equal to the pack refused a project whose two samplers share it: %v", err)
	}
	if pcmAddress(t, cfg, 0) != pcmAddress(t, cfg, 1) {
		t.Error("two samplers on one pack hold separate copies of the PCM")
	}
	if _, err = schedule.CompileWithOptions(p, dir, 48000, 128, schedule.Options{MaxResidentPCM: 3999}); err == nil {
		t.Error("a cap a byte below the pack admitted it")
	}
}

// A pack over the cap is refused from its manifest. Its audio is never opened,
// so a project that is too big cannot spend the memory it will be refused for.
func TestNativeCompileRefusesAPackOverTheCapBeforeReadingItsAudio(t *testing.T) {
	dir := t.TempDir()
	pack := testpack.Write(t, dir, testpack.Options{ID: "unread", Frames: 1000}) // 4000 bytes
	p := packProject(t, pack)
	t.Cleanup(instrumentpack.ReleaseShared)
	if err := os.Remove(audioFile(dir, pack)); err != nil {
		t.Fatal(err)
	}
	_, err := schedule.CompileWithOptions(p, dir, 48000, 128, schedule.Options{MaxResidentPCM: 3999})
	var over *instrumentpack.ResidentPCMError
	if !errors.As(err, &over) || over.Bytes != 4000 || over.Limit != 3999 || over.Pack != pack.Manifest {
		t.Fatalf("a pack over the cap must be refused by its manifest, before its audio is opened: %v", err)
	}
	// At the cap the compile goes on to read the audio and reports the missing file.
	if _, err = schedule.CompileWithOptions(p, dir, 48000, 128, schedule.Options{MaxResidentPCM: 4000}); err == nil || errors.As(err, &over) || !strings.Contains(err.Error(), "take0") {
		t.Fatalf("a pack that fits the cap must reach its audio: %v", err)
	}
}

// The cap sees the whole project before any pack is decoded. A first pack that
// fits must not be decoded for a project that a second pack takes over the cap.
func TestNativeCompileRefusesAProjectOverTheCapBeforeDecodingAnyPack(t *testing.T) {
	dir := t.TempDir()
	first := testpack.Write(t, dir, testpack.Options{ID: "first", Frames: 1000}) // 4000 bytes each
	second := testpack.Write(t, dir, testpack.Options{ID: "second", Frames: 1000})
	p := packProject(t, first, second)
	t.Cleanup(instrumentpack.ReleaseShared)
	before := instrumentpack.Decodes()
	_, err := schedule.CompileWithOptions(p, dir, 48000, 128, schedule.Options{MaxResidentPCM: 7999})
	var over *instrumentpack.ResidentPCMError
	if !errors.As(err, &over) || over.Bytes != 8000 || over.Limit != 7999 || over.Pack != second.Manifest {
		t.Fatalf("the second pack takes the project over the cap: %v", err)
	}
	if got := instrumentpack.Decodes() - before; got != 0 {
		t.Fatalf("a project over the cap decoded %d of its packs before it was refused", got)
	}
	if _, err = schedule.CompileWithOptions(p, dir, 48000, 128, schedule.Options{MaxResidentPCM: 8000}); err != nil {
		t.Fatalf("a cap equal to both packs refused the project: %v", err)
	}
	if got := instrumentpack.Decodes() - before; got != 2 {
		t.Fatalf("a project of two packs decoded %d, want 2", got)
	}
}

// Validate is the compile that Studio runs on every request, so it holds no
// audio. It still applies each rule that needs no audio.
func TestValidateReadsNoAudio(t *testing.T) {
	dir := t.TempDir()
	pack := testpack.Write(t, dir, testpack.Options{ID: "validated", Frames: 8_000_000}) // 32 MB of PCM
	p := packProject(t, pack)
	t.Cleanup(instrumentpack.ReleaseShared)
	// Damage the audio without changing its size. The manifest's hash no longer
	// matches, which only a decode can see.
	damaged, err := os.ReadFile(audioFile(dir, pack))
	if err != nil {
		t.Fatal(err)
	}
	damaged[len(damaged)/2] ^= 0xff
	if err = os.WriteFile(audioFile(dir, pack), damaged, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = schedule.Compile(p, dir, 48000, 128); err == nil {
		t.Fatal("fixture: a compile must see the damaged audio")
	}
	before := instrumentpack.Decodes()
	bytes := allocated(func() { err = schedule.Validate(p, dir, 48000, 128, schedule.Options{}) })
	if err != nil {
		t.Fatalf("Validate read the audio of the pack: %v", err)
	}
	// A compile allocates a megabyte or two of its own, so the test looks for the audio.
	if bytes > uint64(pack.PCMBytes)/8 {
		t.Errorf("Validate allocated %d bytes for a pack of %d bytes of PCM", bytes, pack.PCMBytes)
	}
	if instrumentpack.Decodes() != before {
		t.Error("Validate decoded a pack")
	}
}

func TestValidateReadsNoAssetAudio(t *testing.T) {
	dir := t.TempDir()
	const frames = 4_000_000
	wav := testwav.Bytes(48000, 1, 16, frames, 1) // 16 MB of float32 PCM once loaded
	if err := os.WriteFile(filepath.Join(dir, "wave.wav"), wav, 0o600); err != nil {
		t.Fatal(err)
	}
	source := fmt.Sprintf("cicada 2\ntempo 120\nasset wave \"wave.wav\" { sha256 = \"%x\" format = wav frames = %d rate = 48000Hz channels = 1 }\nsampler hit { asset = wave root = c3 mode = oneshot voices = 2 }\ntrack lead hit {}\npattern melody { c3 . . . }\nscene main { lead = melody }\nsong { main }\n", sha256.Sum256(wav), frames)
	score, ds := notation.Parse([]byte(source))
	p, more := project.FromScore(score)
	if p == nil {
		t.Fatalf("fixture: %+v %+v", ds, more)
	}
	var err error
	compiled := allocated(func() { _, err = schedule.Compile(p, dir, 48000, 128) })
	if err != nil || compiled < 16_000_000 {
		t.Fatalf("fixture: Compile allocated %d bytes (%v), want at least the 16 MB of PCM", compiled, err)
	}
	if validated := allocated(func() { err = schedule.Validate(p, dir, 48000, 128, schedule.Options{}) }); err != nil || validated > 4_000_000 {
		t.Errorf("Validate allocated %d bytes (%v) for 16 MB of asset PCM", validated, err)
	}
}

func TestValidateAppliesTheRulesThatNeedNoAudio(t *testing.T) {
	dir := t.TempDir()
	pack := testpack.Write(t, dir, testpack.Options{ID: "rules", Frames: 1000}) // 4000 bytes
	p := packProject(t, pack)
	t.Cleanup(instrumentpack.ReleaseShared)
	validate := func(limit int64) error {
		return schedule.Validate(p, dir, 48000, 128, schedule.Options{MaxResidentPCM: limit})
	}
	if err := validate(4000); err != nil {
		t.Fatalf("a complete pack at its cap: %v", err)
	}

	// The cap, from the manifest's figure, with the numbers that a compile gives.
	err := validate(3999)
	var over *instrumentpack.ResidentPCMError
	_, compileErr := schedule.CompileWithOptions(p, dir, 48000, 128, schedule.Options{MaxResidentPCM: 3999})
	if !errors.As(err, &over) || over.Bytes != 4000 || over.Limit != 3999 || err.Error() != compileErr.Error() {
		t.Errorf("over the cap: Validate says %v, a compile says %v", err, compileErr)
	}
	if err = validate(-1); err == nil {
		t.Error("a negative cap was accepted")
	}

	// A missing or truncated audio file.
	saved, err := os.ReadFile(audioFile(dir, pack))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(audioFile(dir, pack), saved[:len(saved)-1], 0o600); err != nil {
		t.Fatal(err)
	}
	if err = validate(0); err == nil || !strings.Contains(err.Error(), "take0") {
		t.Errorf("a truncated audio file: %v", err)
	}
	if err = os.Remove(audioFile(dir, pack)); err != nil {
		t.Fatal(err)
	}
	if err = validate(0); err == nil || !strings.Contains(err.Error(), "take0") {
		t.Errorf("a missing audio file: %v", err)
	}

	// A manifest that differs from its pin.
	if err = os.WriteFile(audioFile(dir, pack), saved, 0o600); err != nil {
		t.Fatal(err)
	}
	if err = validate(0); err != nil {
		t.Fatalf("restored pack: %v", err)
	}
	manifest, err := os.OpenFile(filepath.Join(dir, pack.Manifest), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = manifest.WriteString("\n"); err != nil {
		t.Fatal(err)
	}
	manifest.Close()
	if err = validate(0); err == nil || !strings.Contains(err.Error(), "hash mismatch") {
		t.Errorf("a manifest that differs from its pin: %v", err)
	}
}
