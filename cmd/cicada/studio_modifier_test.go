package main

import (
	"bytes"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestStudioAccentAndSlideCellsWriteSource(t *testing.T) {
	handler, path := studioTestHandler(t)
	page := studioCall(t, handler, "/", nil)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `data-pattern="pulse" data-step="0" data-modifier="accent"`) || !strings.Contains(page.Body.String(), `data-pattern="pulse" data-step="0" data-modifier="slide"`) {
		t.Fatalf("Studio did not render source-linked modifier controls: %d", page.Code)
	}
	source := []byte(studioScore)
	for _, edit := range []struct {
		modifier string
		want     string
	}{
		{"accent", "{ 1^ . 5 . }"},
		{"slide", "{ 1^~ . 5 . }"},
		{"accent", "{ 1~ . 5 . }"},
		{"slide", "{ 1 . 5 . }"},
	} {
		result := studioCall(t, handler, "/api/toggle", studioEdit{Revision: studioRevision(source), Pattern: "pulse", Step: 0, Modifier: edit.modifier})
		if result.Code != http.StatusOK {
			t.Fatalf("%s toggle failed: %d %s", edit.modifier, result.Code, result.Body.String())
		}
		var err error
		source, err = os.ReadFile(path)
		if err != nil || !bytes.Contains(source, []byte(edit.want)) {
			t.Fatalf("%s toggle did not write %q: %s, %v", edit.modifier, edit.want, source, err)
		}
	}
	if string(source) != studioScore {
		t.Fatalf("modifier round trip changed other source: %s", source)
	}
	pitch := 48
	conflict := studioCall(t, handler, "/api/toggle", studioEdit{Revision: studioRevision(source), Pattern: "pulse", Step: 0, Pitch: &pitch, Modifier: "accent"})
	if conflict.Code != http.StatusBadRequest {
		t.Fatalf("combined pitch and modifier edit was accepted: %d", conflict.Code)
	}
}

func TestModifierGridPreservesChanceAndSharedPhrase(t *testing.T) {
	source := []byte(strings.Replace(studioScore, "1 . 5 .", "1?70 . 5 .", 1))
	accented, err := toggledModifierSource(source, "pulse", "", 0, "accent")
	if err != nil || !bytes.Contains(accented, []byte("1^?70 . 5 .")) {
		t.Fatalf("accent did not precede chance: %s, %v", accented, err)
	}
	slid, err := toggledModifierSource(accented, "pulse", "", 0, "slide")
	if err != nil || !bytes.Contains(slid, []byte("1^~?70 . 5 .")) {
		t.Fatalf("slide did not preserve accent and chance: %s, %v", slid, err)
	}
	phrase := []byte("track bass acid {}\nphrase hook { 1 . }\npattern pulse { use hook*2 }\nscene main { bass = pulse }\nsong { main }\n")
	shared, err := toggledModifierSource(phrase, "pulse", "", 2, "accent")
	if err != nil || !bytes.Contains(shared, []byte("phrase hook { 1^ . }")) || !bytes.Contains(shared, []byte("use hook*2")) {
		t.Fatalf("phrase modifier did not edit shared source: %s, %v", shared, err)
	}
	for _, edit := range []struct {
		lane     string
		index    int
		modifier string
	}{
		{"", 1, "accent"}, // rest
		{"bd", 0, "accent"},
		{"", 0, "chance"},
	} {
		if _, err := toggledModifierSource(source, "pulse", edit.lane, edit.index, edit.modifier); err == nil {
			t.Fatalf("invalid modifier edit accepted: %+v", edit)
		}
	}
	tied := []byte(strings.Replace(studioScore, "1 . 5 .", "1 - 5 .", 1))
	if _, err := toggledModifierSource(tied, "pulse", "", 1, "slide"); err == nil {
		t.Fatal("tie accepted a slide modifier")
	}
}
