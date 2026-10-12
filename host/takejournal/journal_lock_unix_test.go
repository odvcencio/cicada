//go:build !windows

package takejournal

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// A process that forks between Open and Close (parallel tests, helper tools)
// briefly holds a copy of the journal's lock descriptor until it execs. A
// flock belongs to the open file description, so closing our descriptor alone
// would leave the journal locked for that long. Close must release the lock
// itself, so the same project reopens at once.
func TestCloseReleasesLockHeldByInheritedDescriptor(t *testing.T) {
	dir := t.TempDir()
	score := filepath.Join(dir, "main.cicada")
	if err := os.WriteFile(score, []byte("cicada 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := Open(score)
	if err != nil {
		t.Fatal(err)
	}
	inherited, err := syscall.Dup(int(store.lock.Fd())) // What a forked child holds before exec.
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(inherited)
	if second, err := Open(score); err == nil {
		second.Close()
		t.Fatal("a second journal opened while the first held the lock")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(score)
	if err != nil {
		t.Fatalf("journal stayed locked after Close because another descriptor shares the lock: %v", err)
	}
	reopened.Close()
}

// The lock is per journal path: sessions on different projects never contend.
func TestJournalLockIsPerProject(t *testing.T) {
	var stores []*Store
	for i := 0; i < 3; i++ {
		score := filepath.Join(t.TempDir(), "main.cicada")
		if err := os.WriteFile(score, []byte("cicada 2\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		store, err := Open(score)
		if err != nil {
			t.Fatalf("project %d: %v", i, err)
		}
		stores = append(stores, store)
	}
	for _, store := range stores {
		store.Close()
	}
}
