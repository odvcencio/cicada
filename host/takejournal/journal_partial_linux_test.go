package takejournal

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
)

func TestJournalPartialFinalizeRetryAndReopen(t *testing.T) {
	if os.Getenv("CICADA_PARTIAL_FINALIZE_CHILD") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestJournalPartialFinalizeRetryAndReopen$")
		cmd.Env = append(os.Environ(), "CICADA_PARTIAL_FINALIZE_CHILD=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("partial append regression: %v\n%s", err, output)
		}
		return
	}
	s, path := testStore(t)
	id, err := s.Begin("vox", "main", Revision(nil), 48000, 1)
	if err != nil {
		t.Fatal(err)
	}
	writeBlock(t, s, id, 0, []float32{.5, -.25}, 0)
	if err := s.Flush(id); err != nil {
		t.Fatal(err)
	}
	info, err := s.root.Stat(s.logPath(id))
	if err != nil {
		t.Fatal(err)
	}
	var original syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &original); err != nil {
		t.Fatal(err)
	}
	signal.Ignore(syscall.SIGXFSZ)
	limited := original
	limited.Cur = uint64(info.Size() + 8)
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &limited); err != nil {
		t.Fatal(err)
	}
	finalizeErr := s.Finalize(id, false)
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &original); err != nil {
		t.Fatal(err)
	}
	if finalizeErr == nil {
		t.Fatal("partial completion append was acknowledged")
	}
	// Retrying may succeed after rollback, or reject until recovery; neither may
	// turn the torn final record into interior corruption.
	_ = s.Finalize(id, false)
	if err := s.Recover(); err != nil {
		t.Fatal(err)
	}
	s.Close()
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.Recover(); err != nil {
		t.Fatal(err)
	}
	take, err := reopened.Get(id)
	if err != nil || take.Frames != 2 {
		t.Fatalf("retained PCM lost: %+v %v", take, err)
	}
	wav, err := os.ReadFile(filepath.Join(filepath.Dir(path), take.Asset.Path))
	if err != nil {
		t.Fatal(err)
	}
	if len(wav) != 52 {
		t.Fatalf("WAV bytes=%d", len(wav))
	}
	for i, want := range []float32{.5, -.25} {
		got := math.Float32frombits(binary.LittleEndian.Uint32(wav[44+i*4:]))
		if got != want {
			t.Fatalf("recovered PCM frame %d=%g, want %g", i, got, want)
		}
	}
	journal, err := os.ReadFile(filepath.Join(filepath.Dir(path), reopened.logPath(id)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(journal, []byte("\n")) {
		t.Fatal("journal tail remains torn")
	}
}
