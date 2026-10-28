package instrumentpack_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"m31labs.dev/cicada/host/instrumentpack"
	"m31labs.dev/cicada/internal/testpack"
)

// freshCache empties the process-wide cache before a test and after it, so a
// test counts only the decodes it causes.
func freshCache(t *testing.T) {
	t.Helper()
	instrumentpack.ReleaseShared()
	t.Cleanup(instrumentpack.ReleaseShared)
}

// loadCapped loads the pack the way one native compile does: under its own tally.
func loadCapped(t *testing.T, limit int64, dir string, pack testpack.Pack, rate int) (*instrumentpack.Prepared, error) {
	t.Helper()
	tally, err := instrumentpack.NewResidentCap(limit)
	if err != nil {
		t.Fatal(err)
	}
	return tally.Load(dir, pack.Manifest, pack.SHA256, rate)
}

// pcm is the address of the first sample of the pack's first zone.
func pcm(p *instrumentpack.Prepared) *float32 { return &p.Zones[0].Region.Left[0] }

func TestOpenReadsTheManifestAndDecodesNothing(t *testing.T) {
	freshCache(t)
	dir := t.TempDir()
	pack := testpack.Write(t, dir, testpack.Options{ID: "declared", Assets: 3, Frames: 5000, Channels: 2,
		Zones: []testpack.Zone{{KeyLow: 48, KeyHigh: 59}, {KeyLow: 60, KeyHigh: 72}}})
	// The third asset is declared but no zone plays it.
	before := instrumentpack.Decodes()
	opened, err := instrumentpack.Open(dir, pack.Manifest, pack.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	if got := instrumentpack.Decodes() - before; got != 0 {
		t.Fatalf("Open decoded %d packs", got)
	}
	// Two played assets of 5000 frames and two channels, at four bytes a sample.
	if want := int64(2 * 5000 * 2 * 4); opened.ResidentPCMBytes() != want || pack.PCMBytes != want {
		t.Fatalf("declared %d bytes, testpack says %d, want %d", opened.ResidentPCMBytes(), pack.PCMBytes, want)
	}
	// The manifest's figure is what decoding leaves in memory.
	decoded, err := opened.Decode(48000)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.ResidentPCMBytes() != opened.ResidentPCMBytes() {
		t.Fatalf("decoded %d bytes, manifest declared %d", decoded.ResidentPCMBytes(), opened.ResidentPCMBytes())
	}
	if _, err = instrumentpack.Open(dir, pack.Manifest, strings.Repeat("0", 64)); err == nil || !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("a wrong pin must be refused by Open: %v", err)
	}
}

// The cap counts the manifest's figure before the audio is read, so a pack over
// the cap never allocates its PCM.
func TestResidentCapRefusesAPackBeforeReadingItsAudio(t *testing.T) {
	freshCache(t)
	dir := t.TempDir()
	pack := testpack.Write(t, dir, testpack.Options{ID: "big", Frames: 1000}) // 4000 bytes
	audio := filepath.Join(dir, filepath.Dir(pack.Manifest), "samples", "take0.wav.gz")
	if err := os.Remove(audio); err != nil {
		t.Fatal(err)
	}
	before := instrumentpack.Decodes()
	_, err := loadCapped(t, 3999, dir, pack, 48000)
	var over *instrumentpack.ResidentPCMError
	if !errors.As(err, &over) || over.Bytes != 4000 || over.Limit != 3999 || over.Pack != pack.Manifest {
		t.Fatalf("a pack a byte over the cap must be refused from its manifest, before its audio is opened: %v", err)
	}
	if got := instrumentpack.Decodes() - before; got != 0 {
		t.Fatalf("the refused pack was decoded %d times", got)
	}
	// Under the cap the load goes on to read the audio, and reports the missing file.
	if _, err = loadCapped(t, 4000, dir, pack, 48000); err == nil || errors.As(err, &over) || !strings.Contains(err.Error(), "take0.wav.gz") {
		t.Fatalf("a pack that fits must reach its audio: %v", err)
	}
}

// A host with several packs opens each one before it loads any. Open counts the
// pack against the cap from its manifest and decodes nothing, so the cap sees
// the whole project while no audio is in memory.
func TestResidentCapOpenCountsEveryPackAndDecodesNone(t *testing.T) {
	freshCache(t)
	dir := t.TempDir()
	first := testpack.Write(t, dir, testpack.Options{ID: "first", Frames: 1000}) // 4000 bytes each
	second := testpack.Write(t, dir, testpack.Options{ID: "second", Frames: 1000})
	before := instrumentpack.Decodes()
	tally, err := instrumentpack.NewResidentCap(7999)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tally.Open(dir, first.Manifest, first.SHA256); err != nil {
		t.Fatalf("the first pack fits: %v", err)
	}
	_, err = tally.Open(dir, second.Manifest, second.SHA256)
	var over *instrumentpack.ResidentPCMError
	if !errors.As(err, &over) || over.Bytes != 8000 || over.Limit != 7999 || over.Pack != second.Manifest {
		t.Fatalf("the second pack takes the total to 8000 bytes and must be refused from its manifest: %v", err)
	}
	if got := instrumentpack.Decodes() - before; got != 0 {
		t.Fatalf("counting two packs decoded %d of them", got)
	}
	// At the cap both are counted, and the audio is read only when the caller asks.
	tally, _ = instrumentpack.NewResidentCap(8000)
	var opened []*instrumentpack.Pack
	for _, pack := range []testpack.Pack{first, second} {
		o, err := tally.Open(dir, pack.Manifest, pack.SHA256)
		if err != nil {
			t.Fatal(err)
		}
		opened = append(opened, o)
	}
	if got := instrumentpack.Decodes() - before; got != 0 {
		t.Fatalf("opening two packs decoded %d of them", got)
	}
	for _, o := range opened {
		if _, err = o.LoadShared(48000, tally.Limit()); err != nil {
			t.Fatal(err)
		}
	}
	if got := instrumentpack.Decodes() - before; got != 2 {
		t.Fatalf("loading two packs decoded %d, want 2", got)
	}
}

func TestResidentCapLoadSharesOneDecodedCopyBetweenCompiles(t *testing.T) {
	freshCache(t)
	dir := t.TempDir()
	pack := testpack.Write(t, dir, testpack.Options{Frames: 1000})
	before := instrumentpack.Decodes()
	first, err := loadCapped(t, 0, dir, pack, 48000)
	if err != nil {
		t.Fatal(err)
	}
	second, err := loadCapped(t, 0, dir, pack, 48000)
	if err != nil {
		t.Fatal(err)
	}
	if got := instrumentpack.Decodes() - before; got != 1 {
		t.Fatalf("two loads of one pack decoded it %d times, want 1", got)
	}
	if pcm(first) != pcm(second) {
		t.Fatal("the two loads hold separate copies of the PCM")
	}
}

// A render sets the sampler's voice count and seed on the Prepared it gets, and
// a Studio compile reads the same pack at the same time. Neither may see the other.
func TestSharedLoadsGiveEachCallerItsOwnManifestAndZoneTable(t *testing.T) {
	freshCache(t)
	dir := t.TempDir()
	pack := testpack.Write(t, dir, testpack.Options{Frames: 1000})
	first, err := loadCapped(t, 0, dir, pack, 48000)
	if err != nil {
		t.Fatal(err)
	}
	voices := first.Manifest.Config.Voices
	first.Manifest.Config.Voices = voices + 7
	first.Manifest.Config.Humanize.Seed = 99
	first.Zones[0].Gain = 5
	first.Zones[0].KeyHigh = 1
	second, err := loadCapped(t, 0, dir, pack, 48000)
	if err != nil {
		t.Fatal(err)
	}
	if second.Manifest.Config.Voices != voices || second.Manifest.Config.Humanize.Seed != 0 || second.Zones[0].Gain != 1 || second.Zones[0].KeyHigh != 72 {
		t.Fatalf("a change to one caller's pack reached another: %+v %+v", second.Manifest.Config, second.Zones[0])
	}
	if pcm(first) != pcm(second) {
		t.Fatal("the callers do not share the PCM")
	}
}

func TestSharedLoadTreatsChangedManifestBytesAsAnotherPack(t *testing.T) {
	freshCache(t)
	dir := t.TempDir()
	pack := testpack.Write(t, dir, testpack.Options{Frames: 1000})
	// An unpinned load reads whatever manifest is there.
	tally, _ := instrumentpack.NewResidentCap(0)
	first, err := tally.Load(dir, pack.Manifest, "", 48000)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(filepath.Join(dir, pack.Manifest), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.WriteString("\n"); err != nil {
		t.Fatal(err)
	}
	file.Close()
	before := instrumentpack.Decodes()
	tally, _ = instrumentpack.NewResidentCap(0)
	second, err := tally.Load(dir, pack.Manifest, "", 48000)
	if err != nil {
		t.Fatal(err)
	}
	if instrumentpack.Decodes()-before != 1 || pcm(first) == pcm(second) {
		t.Fatalf("a manifest with different bytes reused the earlier PCM (decodes %d)", instrumentpack.Decodes()-before)
	}
}

func TestSharedLoadsDecodeOnceWhenCalledTogether(t *testing.T) {
	freshCache(t)
	dir := t.TempDir()
	pack := testpack.Write(t, dir, testpack.Options{Frames: 200_000})
	before := instrumentpack.Decodes()
	const callers = 8
	loaded := make([]*instrumentpack.Prepared, callers)
	failures := make([]error, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			loaded[i], failures[i] = loadCapped(t, 0, dir, pack, 48000)
		}()
	}
	wg.Wait()
	for i := range callers {
		if failures[i] != nil {
			t.Fatal(failures[i])
		}
		if pcm(loaded[i]) != pcm(loaded[0]) {
			t.Fatalf("caller %d holds its own copy", i)
		}
	}
	if got := instrumentpack.Decodes() - before; got != 1 {
		t.Fatalf("%d simultaneous loads decoded the pack %d times, want 1", callers, got)
	}
}

// The cache keeps packs after their compile returns, within the cap, and lets
// the least recently used one go when a newer pack needs the room.
func TestSharedCacheDropsTheLeastRecentlyUsedPackOverTheCap(t *testing.T) {
	freshCache(t)
	dir := t.TempDir()
	packs := map[string]testpack.Pack{}
	for _, id := range []string{"a", "b", "c"} {
		packs[id] = testpack.Write(t, dir, testpack.Options{ID: id, Frames: 1000}) // 4000 bytes each
	}
	const limit = 8000 // two packs
	before := instrumentpack.Decodes()
	load := func(id string) *instrumentpack.Prepared {
		t.Helper()
		p, err := loadCapped(t, limit, dir, packs[id], 48000)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	decoded := func(want uint64, what string) {
		t.Helper()
		if got := instrumentpack.Decodes() - before; got != want {
			t.Fatalf("%s: %d decodes so far, want %d", what, got, want)
		}
	}
	load("a")
	b := load("b")
	decoded(2, "a then b")
	load("a") // a is now more recent than b
	decoded(2, "a again, from the cache")
	load("c") // 12000 bytes: b, the least recent, goes
	decoded(3, "c")
	load("a")
	decoded(3, "a survived c")
	again := load("b")
	decoded(4, "b was dropped for c and decodes again")
	if pcm(again) == pcm(b) {
		t.Fatal("b was served from the cache after it was dropped")
	}
	// A caller that still holds the dropped pack keeps its audio.
	if len(b.Zones[0].Region.Left) != 1000 {
		t.Fatalf("the dropped pack lost its PCM: %d frames", len(b.Zones[0].Region.Left))
	}
}

func TestSharedPackAdmitsEachRateOnce(t *testing.T) {
	freshCache(t)
	dir := t.TempDir()
	// Keys 34 to 86 around a root of 60 on a 48 kHz recording play at 48 kHz
	// (lowest ratio 0.22) but not at 96 kHz (0.11, below the voice's 0.125).
	pack := testpack.Write(t, dir, testpack.Options{Frames: 1000, Zones: []testpack.Zone{{KeyLow: 34, KeyHigh: 86}}})
	before := instrumentpack.Decodes()
	for _, rate := range []int{48000, 44100} {
		if _, err := loadCapped(t, 0, dir, pack, rate); err != nil {
			t.Fatalf("%d Hz: %v", rate, err)
		}
	}
	if _, err := loadCapped(t, 0, dir, pack, 96000); err == nil || !strings.Contains(err.Error(), "playback ratio") {
		t.Fatalf("96 kHz: want a playback ratio refusal, got %v", err)
	}
	// The refusal at one rate leaves the pack usable at the others.
	if _, err := loadCapped(t, 0, dir, pack, 48000); err != nil {
		t.Fatal(err)
	}
	if got := instrumentpack.Decodes() - before; got != 1 {
		t.Fatalf("one pack at three rates decoded %d times, want 1 (the PCM does not depend on the rate)", got)
	}
}

func TestSharedCacheDoesNotRememberAFailure(t *testing.T) {
	freshCache(t)
	dir := t.TempDir()
	pack := testpack.Write(t, dir, testpack.Options{Frames: 1000})
	audio := filepath.Join(dir, filepath.Dir(pack.Manifest), "samples", "take0.wav.gz")
	saved, err := os.ReadFile(audio)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(audio); err != nil {
		t.Fatal(err)
	}
	if _, err = loadCapped(t, 0, dir, pack, 48000); err == nil {
		t.Fatal("a pack with no audio loaded")
	}
	if err = os.WriteFile(audio, saved, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = loadCapped(t, 0, dir, pack, 48000); err != nil {
		t.Fatalf("the failure was remembered: %v", err)
	}
}

func TestCheckFilesFindsAMissingOrTruncatedAssetWithoutDecoding(t *testing.T) {
	freshCache(t)
	dir := t.TempDir()
	pack := testpack.Write(t, dir, testpack.Options{Frames: 1000})
	opened, err := instrumentpack.Open(dir, pack.Manifest, pack.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	before := instrumentpack.Decodes()
	if err = opened.CheckFiles(); err != nil {
		t.Fatalf("a complete pack: %v", err)
	}
	audio := filepath.Join(dir, filepath.Dir(pack.Manifest), "samples", "take0.wav.gz")
	saved, err := os.ReadFile(audio)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(audio, saved[:len(saved)-1], 0o600); err != nil {
		t.Fatal(err)
	}
	if err = opened.CheckFiles(); err == nil || !strings.Contains(err.Error(), "take0") {
		t.Fatalf("a truncated asset: %v", err)
	}
	if err = os.Remove(audio); err != nil {
		t.Fatal(err)
	}
	if err = opened.CheckFiles(); err == nil {
		t.Fatal("a missing asset passed")
	}
	if got := instrumentpack.Decodes() - before; got != 0 {
		t.Fatalf("CheckFiles decoded %d packs", got)
	}
}
