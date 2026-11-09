package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestShellPanesFollowTheLayoutCookie(t *testing.T) {
	for _, test := range []struct {
		cookie        string
		library, code bool
	}{
		{"", true, false}, // no preference yet: Library open, Code closed
		{"10", true, false},
		{"11", true, true},
		{"01", false, true},
		{"00", false, false},
		{"1", true, false}, // malformed values keep the defaults
		{"abc", true, false},
	} {
		r := httptest.NewRequest(http.MethodGet, "/studio", nil)
		if test.cookie != "" {
			r.AddCookie(&http.Cookie{Name: "cicada_panes", Value: test.cookie})
		}
		if library, code := shellPanes(r); library != test.library || code != test.code {
			t.Errorf("cookie %q: library %v code %v, want %v %v", test.cookie, library, code, test.library, test.code)
		}
	}
}
