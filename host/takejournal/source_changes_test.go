package takejournal

import (
	"bytes"
	"testing"
)

func TestPreparedProjectSourcesSurviveRestartAndCannotAliasBuffers(t *testing.T) {
	s, path := testStore(t)
	id, err := s.Begin("vox", "main", Revision([]byte("before")), 48000, 1)
	if err != nil {
		t.Fatal(err)
	}
	writeBlock(t, s, id, 0, []float32{.25, -.25}, 0)
	if err = s.Finalize(id, false); err != nil {
		t.Fatal(err)
	}
	if err = s.Publish(id); err != nil {
		t.Fatal(err)
	}
	files := []SourceChange{{Path: "parts/scene.cicada", Before: []byte("// old 🎹\r\n"), After: []byte("// selected 🎵\r\n")}}
	if err = s.PrepareProject(id, []byte("before"), []byte("after"), files); err != nil {
		t.Fatal(err)
	}
	files[0].After[0] = 'x'
	got, _ := s.Get(id)
	if got.Files[0].After[0] != '/' {
		t.Fatal("the journal retained a caller-owned slice")
	}
	got.Files[0].After[0] = 'y'
	s.Close()
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err = reopened.Recover(); err != nil {
		t.Fatal(err)
	}
	got, err = reopened.Get(id)
	if err != nil || got.Stage != Prepared || len(got.Files) != 1 || string(got.Files[0].After) != "// selected 🎵\r\n" || !bytes.Equal(got.Candidate, []byte("after")) {
		t.Fatalf("restart lost prepared source bytes: %+v %v", got, err)
	}
	for _, invalid := range []string{"../escape", "/absolute", "parts/../scene.cicada", "parts\\scene.cicada"} {
		if err := reopened.PrepareProject(id, []byte("before"), []byte("after"), []SourceChange{{Path: invalid}}); err == nil {
			t.Fatalf("accepted %q", invalid)
		}
	}
	if err := reopened.Prepare(id, []byte("before"), []byte("new candidate")); err != nil {
		t.Fatal(err)
	}
	got, _ = reopened.Get(id)
	if len(got.Files) != 0 {
		t.Fatal("a later single-file preparation retained earlier auxiliary changes")
	}
}
