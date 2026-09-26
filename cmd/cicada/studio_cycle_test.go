package main

import (
	"bytes"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestStudioRatchetAndChanceCellsWriteSource(t *testing.T) {
	handler, path := studioTestHandler(t)
	page := studioCall(t, handler, "/", nil)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `data-pattern="pulse" data-step="0" data-modifier="ratchet"`) || !strings.Contains(page.Body.String(), `data-pattern="pulse" data-step="0" data-modifier="chance"`) {
		t.Fatalf("Studio did not render ratchet and chance controls: %d", page.Code)
	}
	source := []byte(studioScore)
	for _, edit := range []struct {
		kind string
		want string
	}{
		{"ratchet", "{ 1*2 . 5 . }"},
		{"ratchet", "{ 1*3 . 5 . }"},
		{"chance", "{ 1*3?75 . 5 . }"},
		{"chance", "{ 1*3?50 . 5 . }"},
		{"chance", "{ 1*3?25 . 5 . }"},
		{"chance", "{ 1*3 . 5 . }"},
	} {
		result := studioCall(t, handler, "/api/toggle", studioEdit{Revision: studioRevision(source), Pattern: "pulse", Step: 0, Modifier: edit.kind})
		if result.Code != http.StatusOK {
			t.Fatalf("%s cycle failed: %d %s", edit.kind, result.Code, result.Body.String())
		}
		var err error
		source, err = os.ReadFile(path)
		if err != nil || !bytes.Contains(source, []byte(edit.want)) {
			t.Fatalf("%s cycle did not write %q: %s, %v", edit.kind, edit.want, source, err)
		}
	}
}

func TestCyclePreservesOtherModifiersAndPhraseSource(t *testing.T) {
	source := []byte(strings.Replace(studioScore, "1 . 5 .", "1^~*8%70 . 5 .", 1))
	ratchet, err := cycledStepSource(source, "pulse", "", 0, "ratchet")
	if err != nil || !bytes.Contains(ratchet, []byte("1^~%70 . 5 .")) {
		t.Fatalf("ratchet wrap lost note modifiers: %s, %v", ratchet, err)
	}
	chance, err := cycledStepSource(ratchet, "pulse", "", 0, "chance")
	if err != nil || !bytes.Contains(chance, []byte("1^~?50 . 5 .")) {
		t.Fatalf("chance cycle did not modernize legacy spelling: %s, %v", chance, err)
	}
	phrase := []byte("track bass acid {}\nphrase hook { 1 . }\npattern pulse { use hook*2 }\nscene main { bass = pulse }\nsong { main }\n")
	shared, err := cycledStepSource(phrase, "pulse", "", 2, "chance")
	if err != nil || !bytes.Contains(shared, []byte("phrase hook { 1?75 . }")) || !bytes.Contains(shared, []byte("use hook*2")) {
		t.Fatalf("phrase chance did not edit shared source: %s, %v", shared, err)
	}
	for _, edit := range []struct {
		lane  string
		index int
		kind  string
	}{
		{"", 1, "chance"}, // rest
		{"bd", 0, "chance"},
		{"", 0, "accent"},
	} {
		if _, err := cycledStepSource(source, "pulse", edit.lane, edit.index, edit.kind); err == nil {
			t.Fatalf("invalid dynamics edit accepted: %+v", edit)
		}
	}
}
