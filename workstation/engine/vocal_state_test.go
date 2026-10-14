package main

import (
	"testing"

	"m31labs.dev/cicada/workstation/control"
)

func TestVocalDraftDiffersOnNudgeAndGainButAcceptedValuesAreClean(t *testing.T) {
	phrases := []control.VocalPhrase{{Take: "t", Start: 0, End: 960, GainDB: -3, OffsetMS: 10}}
	region := control.VocalRange{Start: 0, End: 480}
	gain, edits := control.VocalPhraseGain(phrases, "t", region), control.VocalPhraseEdits(phrases, "t", region)
	if vocalDraftDiffers(gain, edits, phrases, "t", region) {
		t.Fatal("the accepted gain and timing are not a draft")
	}
	later := edits
	later.OffsetMS += 5
	if !vocalDraftDiffers(gain, later, phrases, "t", region) {
		t.Error("Later 5 ms must mark the take unsaved")
	}
	earlier := edits
	earlier.OffsetMS -= 5
	if !vocalDraftDiffers(gain, earlier, phrases, "t", region) {
		t.Error("Earlier 5 ms must mark the take unsaved")
	}
	if !vocalDraftDiffers(0, edits, phrases, "t", region) {
		t.Error("Reset gain away from the accepted gain must mark the take unsaved")
	}
	if vocalDraftDiffers(gain, later, phrases, "t", region) == vocalDraftDiffers(gain, edits, phrases, "t", region) {
		t.Error("nudging and undoing the nudge must differ")
	}
	back := later
	back.OffsetMS -= 5
	if vocalDraftDiffers(gain, back, phrases, "t", region) {
		t.Error("nudging back to the accepted value clears the draft")
	}
}
