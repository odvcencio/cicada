package main

import (
	"net/http"
	"net/http/httptest"
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
