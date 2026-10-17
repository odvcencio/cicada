package main

import "m31labs.dev/cicada/workstation/control"

// vocalDraftDiffers reports whether the lane's gain or timing edits differ from
// what the server rendered for the selected take and range. The Earlier, Later
// and Reset gain buttons change these values without touching a form control,
// so the unsaved-work registry reads them from here.
func vocalDraftDiffers(gain float64, edits control.VocalEdits, phrases []control.VocalPhrase, take string, region control.VocalRange) bool {
	return gain != control.VocalPhraseGain(phrases, take, region) || edits != control.VocalPhraseEdits(phrases, take, region)
}
