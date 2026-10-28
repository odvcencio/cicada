package main

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/instrumentpack"
	"m31labs.dev/cicada/internal/testpack"
	"m31labs.dev/cicada/kernel/engine"
)

// allocatedBytes is the heap that f allocated, whether or not it is still alive.
func allocatedBytes(f func()) uint64 {
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	f()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// packScore writes a project with one pack of the given length and a score that
// plays it, and returns the score's path and the pack.
func packScore(t *testing.T, frames int) (string, testpack.Pack) {
	t.Helper()
	dir := t.TempDir()
	pack := testpack.Write(t, dir, testpack.Options{Frames: frames})
	path := filepath.Join(dir, "main.cicada")
	if err := os.WriteFile(path, []byte(samplerScore(pack, "pattern melody notes { c4 . . . }\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(instrumentpack.ReleaseShared)
	return path, pack
}

// Studio checks that the score compiles on every state request and on every
// edit. The check reads the pack manifest and counts its size, but decodes none
// of the pack's audio: 435 MiB of drum PCM took three seconds a request.
func TestStudioSourceCheckDecodesNoSamplerPackAudio(t *testing.T) {
	// A source check allocates for the score, the same for a pack of any length.
	// Measure it on a tiny pack, then on one of 64 MB of PCM.
	check := func(frames, channels int) (allocated uint64, pcm int64, decoded uint64) {
		dir := t.TempDir()
		pack := testpack.Write(t, dir, testpack.Options{Frames: frames, Channels: channels})
		path := filepath.Join(dir, "main.cicada")
		source := samplerScore(pack, "pattern melody notes { c4 . . . }\n")
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(instrumentpack.ReleaseShared)
		before := instrumentpack.Decodes()
		allocated = allocatedBytes(func() {
			for range 3 {
				if _, err := compileStudioSource(path, []byte(source)); err != nil {
					t.Fatal(err)
				}
			}
		})
		return allocated, pack.PCMBytes, instrumentpack.Decodes() - before
	}
	baseline, _, _ := check(1000, 1)
	total, pcm, decoded := check(8_000_000, 2)
	if decoded != 0 {
		t.Errorf("three source checks decoded %d packs", decoded)
	}
	// Decoding the pack even once would add its 64 MB to the baseline.
	if total > baseline+uint64(pcm)/2 {
		t.Errorf("three source checks of a pack of %d bytes of PCM allocated %d bytes; of a tiny pack, %d", pcm, total, baseline)
	}
}

func TestStudioStartupAndStateRequestsDecodeNoSamplerPackAudio(t *testing.T) {
	path, _ := packScore(t, 4000)
	before := instrumentpack.Decodes()
	s, err := newStudio(path)
	if err != nil {
		t.Fatal(err)
	}
	handler := s.routes()
	for range 10 {
		if state := studioCall(t, handler, "/api/state", nil); state.Code != http.StatusOK {
			t.Fatalf("state: %d %s", state.Code, state.Body.String())
		}
	}
	if got := instrumentpack.Decodes() - before; got != 0 {
		t.Errorf("starting Studio and ten state requests decoded %d packs", got)
	}
}

// An edit renders the score to prove that it still sounds right, so the first
// one decodes the pack. The packs stay in memory, and the next edit reuses them.
func TestStudioEditsDecodeAPackOnce(t *testing.T) {
	path, _ := packScore(t, 4000)
	s, err := newStudio(path)
	if err != nil {
		t.Fatal(err)
	}
	handler := s.routes()
	edit := func(comment string) {
		t.Helper()
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		reply := studioCall(t, handler, "/api/source", studioEdit{Revision: studioRevision(source), SourceRevision: studioProjectReceipt(t, path), Source: string(source) + comment})
		if reply.Code != http.StatusOK {
			t.Fatalf("edit: %d %s", reply.Code, reply.Body.String())
		}
	}
	before := instrumentpack.Decodes()
	edit("// first edit\n")
	if got := instrumentpack.Decodes() - before; got > 1 {
		t.Errorf("the first edit decoded the pack %d times", got)
	}
	before = instrumentpack.Decodes()
	edit("// second edit\n")
	edit("// third edit\n")
	if got := instrumentpack.Decodes() - before; got != 0 {
		t.Errorf("two more edits decoded the pack %d times", got)
	}
}

// The cap still holds in Studio: a score whose packs declare more than the cap
// is refused at startup and by an edit, from the manifests.
func TestStudioRefusesPacksOverTheCapWithoutDecodingThem(t *testing.T) {
	t.Cleanup(func() { instrumentpack.SetDefaultMaxResidentPCM(0) })
	path, pack := packScore(t, 1000) // 4000 bytes
	if err := instrumentpack.SetDefaultMaxResidentPCM(3999); err != nil {
		t.Fatal(err)
	}
	before := instrumentpack.Decodes()
	if _, err := newStudio(path); err == nil || !strings.Contains(err.Error(), "--max-resident-pcm") || !strings.Contains(err.Error(), pack.Manifest) {
		t.Fatalf("Studio opened a score whose pack is over the cap: %v", err)
	}
	if got := instrumentpack.Decodes() - before; got != 0 {
		t.Errorf("the refusal decoded %d packs", got)
	}
	if err := instrumentpack.SetDefaultMaxResidentPCM(4000); err != nil {
		t.Fatal(err)
	}
	if _, err := newStudio(path); err != nil {
		t.Fatalf("a cap equal to the pack refused it: %v", err)
	}
}

// The engine decodes a pack once. The plan that plays the score, a second plan
// built from an edit, and the sampler placement all use the same copy.
func TestStudioPlansShareOneDecodedCopyOfAPack(t *testing.T) {
	path, _ := packScore(t, 4000)
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	before := instrumentpack.Decodes()
	first, err := buildLivePlan(path, source, liveSampleRate)
	if err != nil {
		t.Fatal(err)
	}
	second, err := buildLivePlan(path, append(source, []byte("// an edit\n")...), liveSampleRate)
	if err != nil {
		t.Fatal(err)
	}
	if got := instrumentpack.Decodes() - before; got != 1 {
		t.Errorf("two plans of one pack decoded it %d times, want 1", got)
	}
	address := func(plan *livePlan) *float32 {
		for _, track := range plan.cfg.Track {
			if pack, ok := track.Prepared.(*engine.PackFactory); ok {
				return &pack.Zones[0].Region.Left[0]
			}
		}
		t.Fatal("no track is backed by the pack")
		return nil
	}
	if address(first) != address(second) {
		t.Error("two plans hold separate copies of the PCM")
	}
}
