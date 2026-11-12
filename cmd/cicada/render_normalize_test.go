package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"m31labs.dev/cicada/render"
)

// Peak normalization renders twice; the second render replays the first one's
// mix and must write the bytes of a render that synthesizes both times.
func TestRenderPlainFileNormalizeReplaysTheFirstRender(t *testing.T) {
	defer func(create func(string) (*render.MasterCache, error)) { newNormalizeMasterCache = create }(newNormalizeMasterCache)
	inspection, err := inspectScore(filepath.Join(repositoryRoot(), "examples", "tuplet-grid.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	opts := render.Options{SampleRate: 48000, Bits: 24, TailSec: 1, Normalize: true}
	var want bytes.Buffer
	wantReport, err := render.WAV(inspection.score, opts, &want)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	var cache *render.MasterCache
	newNormalizeMasterCache = func(dir string) (*render.MasterCache, error) {
		created, err := render.NewMasterCache(dir)
		cache = created
		return created, err
	}
	check := func(name string) {
		t.Helper()
		path := filepath.Join(dir, name+".wav")
		report, err := renderPlainFile(inspection.score, path, opts)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want.Bytes()) || !reflect.DeepEqual(report, wantReport) {
			t.Fatalf("%s: the normalized render differs from one without a cache", name)
		}
		if files, _ := filepath.Glob(filepath.Join(dir, ".cicada-master-cache-*")); len(files) != 0 {
			t.Fatalf("%s: left cache files behind: %v", name, files)
		}
	}
	check("cached")
	if cache == nil || cache.Replays() != 1 {
		t.Fatalf("the second render did not replay the first: %v", cache)
	}
	newNormalizeMasterCache = func(string) (*render.MasterCache, error) { return nil, errors.New("no space left on device") }
	check("without a cache")
}
