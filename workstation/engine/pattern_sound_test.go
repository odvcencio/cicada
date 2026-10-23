package main

import (
	"net/url"
	"testing"
)

func TestPatternSoundContextStaysWithinTheCurrentPhrase(t *testing.T) {
	base := "http://127.0.0.1:1234/studio?dock=clip&pattern=played&track=keys&note=2&scene=ending#note-gestures"
	for _, check := range []struct {
		name, raw string
		current   bool
		want      bool
	}{
		{"current", base, false, true},
		{"note selection", "/?panel=patterns&pattern=played&note=3#shared-gestures", false, true},
		{"native return", "/studio?pattern=played&tab=session&region=detail", false, true},
		{"other pattern", "/studio?pattern=another&track=keys", false, false},
		{"other explicit phrase", "/studio?pattern=another&track=keys", true, false},
		{"other origin", "http://127.0.0.1:5678/studio?pattern=played", false, false},
		{"credentials", "http://other@127.0.0.1:1234/studio?pattern=played", false, false},
		{"action", "/__actions/pattern?pattern=played", false, false},
		{"unrelated panel", "/studio?dock=device", false, false},
		{"implicit current phrase", "/studio?dock=clip&region=detail", true, true},
		{"bad escape", "/studio?pattern=%xx", false, false},
	} {
		t.Run(check.name, func(t *testing.T) {
			got, ok := patternSoundURL(check.raw, base, "played", "upper", check.current)
			if ok != check.want {
				t.Fatalf("rewrite %v, want %v: %s", ok, check.want, got)
			}
			if !ok {
				if got != check.raw {
					t.Fatal("changed an unrelated destination")
				}
				return
			}
			before, _ := url.Parse(check.raw)
			after, _ := url.Parse(got)
			if after.Query().Get("track") != "upper" || after.Query().Get("pattern") != "played" || after.Fragment != before.Fragment {
				t.Fatal("sound choice lost phrase selection or fragment", got)
			}
			for _, key := range []string{"dock", "tab", "region", "note", "scene"} {
				if before.Query().Get(key) != after.Query().Get(key) {
					t.Fatal("sound choice changed editing context", key, got)
				}
			}
		})
	}
}
