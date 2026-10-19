package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkstationRecoveryScopeUsesProfileAndCanonicalScore(t *testing.T) {
	dir := t.TempDir()
	profile := filepath.Join(dir, "profile")
	t.Setenv("CICADA_AUDIO_SETTINGS_DIR", filepath.Join(profile, "audio"))
	score := filepath.Join(dir, "main.cicada")
	if err := os.WriteFile(score, []byte("title \"Test\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	first, err := studioWorkstationStateDir(score)
	if err != nil || !strings.HasPrefix(first, filepath.Join(profile, "workspaces")+string(filepath.Separator)) {
		t.Fatalf("wrong private recovery directory: %s %v", first, err)
	}
	other := filepath.Join(dir, "other.cicada")
	if err = os.WriteFile(other, []byte("title \"Other\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	second, err := studioWorkstationStateDir(other)
	if err != nil || first == second {
		t.Fatal("different score reused private note recovery")
	}
	alias := filepath.Join(dir, "alias.cicada")
	if err = os.Symlink(score, alias); err != nil {
		t.Skip("symlinks unavailable", err)
	}
	third, err := studioWorkstationStateDir(alias)
	if err != nil || first != third {
		t.Fatal("score alias lost recovery identity")
	}
}
