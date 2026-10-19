package main

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPersistentWorkspaceRolesKeepOneOwnerAndHonorViewer(t *testing.T) {
	dir := t.TempDir()
	a, _, err := openNoteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := openNoteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	actors := []string{strings.Repeat("a", 32), strings.Repeat("b", 32)}
	var wg sync.WaitGroup
	for i, store := range []*noteStore{a, b} {
		wg.Add(1)
		go func(i int, store *noteStore) {
			defer wg.Done()
			if _, err := store.membership(actors[i], true, true); err != nil {
				t.Error(err)
			}
		}(i, store)
	}
	wg.Wait()
	access, err := a.membership("", false, false)
	if err != nil || len(access.Roles) != 2 || access.Owner == "" {
		t.Fatalf("concurrent windows lost membership: %+v %v", access, err)
	}
	viewer := actors[0]
	if viewer == access.Owner {
		viewer = actors[1]
	}
	reopened, _, err := openNoteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	access, err = reopened.membership(viewer, false, true)
	if err != nil || access.Roles[viewer] != "viewer" {
		t.Fatal("restart promoted viewer")
	}
	if _, err = reopened.grantMember(viewer, "editor", viewer); err == nil {
		t.Fatal("viewer granted itself edit access")
	}
	if _, err = reopened.grantMember(viewer, "editor", access.Owner); err != nil {
		t.Fatal(err)
	}
	if access, err = b.membership(viewer, false, false); err != nil || access.Roles[viewer] != "editor" {
		t.Fatal("second window missed granted role")
	}
	if _, err = reopened.grantMember(viewer, "viewer", access.Owner); err != nil {
		t.Fatal(err)
	}
	if access, err = b.membership(viewer, false, true); err != nil || access.Roles[viewer] != "viewer" {
		t.Fatal("second window retained revoked edit access")
	}
	unknown := strings.Repeat("c", 32)
	if access, err = b.membership(unknown, false, true); err != nil || access.Roles[unknown] != "" {
		t.Fatal("unknown actor was silently registered")
	}
}

func TestNoteCheckpointIOCannotLockOutExistingPerformer(t *testing.T) {
	store, _, err := openNoteStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	actor := strings.Repeat("d", 32)
	if _, err = store.membership(actor, true, true); err != nil {
		t.Fatal(err)
	}
	locked, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() { done <- store.locked(func(*os.Root) error { close(locked); <-release; return nil }) }()
	<-locked
	defer func() {
		close(release)
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	read := make(chan error, 1)
	go func() {
		access, err := store.membership(actor, false, true)
		if err == nil && access.Roles[actor] != "owner" {
			err = fmt.Errorf("performer lost access")
		}
		read <- err
	}()
	select {
	case err := <-read:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("note recovery file lock blocked performer authorization")
	}
}
