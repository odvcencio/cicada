package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrivateStudioServiceRequiresChildCredential(t *testing.T) {
	calls := 0
	handler, token, err := privateStudioService(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusNoContent)
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, credential := range []string{"", "Bearer wrong", "Bearer " + token} {
		r := httptest.NewRequest(http.MethodPost, "/api/source", nil)
		r.Header.Set("Authorization", credential)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		want := http.StatusUnauthorized
		if credential == "Bearer "+token {
			want = http.StatusNoContent
		}
		if w.Code != want {
			t.Fatalf("status = %d, want %d", w.Code, want)
		}
	}
	if calls != 1 {
		t.Fatalf("domain calls = %d, want 1", calls)
	}
}

func TestStudioWorkstationStateStaysInSelectedProfile(t *testing.T) {
	for _, customAudio := range []bool{false, true} {
		t.Run(fmt.Sprint(customAudio), func(t *testing.T) {
			profile := t.TempDir()
			t.Setenv("CICADA_STATE_DIR", profile)
			t.Setenv("CICADA_AUDIO_SETTINGS_DIR", "")
			if customAudio {
				t.Setenv("CICADA_AUDIO_SETTINGS_DIR", filepath.Join(profile, "audio"))
			}
			score := filepath.Join(t.TempDir(), "main.cicada")
			if err := os.WriteFile(score, []byte("title \"Profile\"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			state, err := studioWorkstationStateDir(score)
			if err != nil || !strings.HasPrefix(state, filepath.Join(profile, "workspaces")+string(filepath.Separator)) {
				t.Fatalf("workspace state escaped profile: %q, %v", state, err)
			}
		})
	}
}
