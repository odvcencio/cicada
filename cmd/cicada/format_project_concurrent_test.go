package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

type concurrentFormatWriter struct{ after func() }

func (w *concurrentFormatWriter) Write(p []byte) (int, error) {
	if w.after != nil {
		fn := w.after
		w.after = nil
		fn()
	}
	return len(p), nil
}
func TestProjectFormatKeepsConcurrentExternalEdit(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "a.cicada")
	second := filepath.Join(dir, "b.cicada")
	source := []byte("title \"A\"\ntrack bass acid {}\npattern pulse {1 . 5 .}\nscene main {bass=pulse}\nsong {main}\n")
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, source, 0600); err != nil {
			t.Fatal(err)
		}
	}
	external := bytes.Replace(source, []byte("A"), []byte("External saved draft"), 1)
	writer := &concurrentFormatWriter{after: func() {
		if err := os.WriteFile(second, external, 0600); err != nil {
			t.Fatal(err)
		}
	}}
	var diagnostics bytes.Buffer
	err := formatProject(dir, false, writer, &diagnostics)
	got, readErr := os.ReadFile(second)
	if readErr != nil || !bytes.Equal(got, external) {
		t.Fatalf("concurrent external score was lost: %q, %v; format result %v", got, readErr, err)
	}
	if err == nil {
		t.Fatal("format did not report the conflict")
	}
}

func TestProjectFormatRetainsLateExternalWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "late.cicada")
	before := []byte("title \"Before\"\ntrack bass acid {}\npattern pulse {1 . 5 .}\nscene main {bass=pulse}\nsong {main}\n")
	if err := os.WriteFile(path, before, 0640); err != nil {
		t.Fatal(err)
	}
	writer, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	var output, diagnostics bytes.Buffer
	if err := formatProject(dir, false, &output, &diagnostics); err != nil {
		t.Fatal(err)
	}
	formatted, err := os.ReadFile(path)
	if err != nil || bytes.Equal(formatted, before) {
		t.Fatalf("format did not save: %q, %v", formatted, err)
	}
	external := bytes.Replace(before, []byte("Before"), []byte("Late external draft"), 1)
	if err := writer.Truncate(0); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.WriteAt(external, 0); err != nil {
		t.Fatal(err)
	}
	if err := writer.Sync(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range entries {
		if source, err := os.ReadFile(filepath.Join(dir, entry.Name())); err == nil && bytes.Equal(source, external) {
			found = true
		}
	}
	if !found {
		t.Fatal("late external draft has no recovery file")
	}
	if err := studioRecoveryConflict(path); err == nil {
		t.Fatal("Studio did not detect the formatter's external-write conflict")
	}
	for _, check := range []bool{false, true} {
		if err := formatProject(dir, check, &output, &diagnostics); err == nil {
			t.Fatalf("format check=%v ignored an unresolved recovery on an unchanged score", check)
		}
	}
	if err := writeProjectFormatEdit(projectFormatEdit{path: path, before: formatted, formatted: before}); err == nil {
		t.Fatal("formatter accepted an unresolved recovery conflict")
	}
	current, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(current, formatted) {
		t.Fatalf("refused format changed the score: %q, %v", current, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0640 {
		t.Fatalf("file mode changed: %v, %v", info, err)
	}
}
