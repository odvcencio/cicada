package edit

import "fmt"

// ConflictError reports that the envelope revision does not match the source.
type ConflictError struct{ Expected, Actual string }

func (e *ConflictError) Error() string { return "score changed on disk; reload before saving" }

// SharedPhraseError reports a step that comes from a shared phrase.
type SharedPhraseError struct {
	Pattern, Phrase string
	Step            int
}

func (e *SharedPhraseError) Error() string {
	return fmt.Sprintf("step %d of pattern %s comes from phrase %s; set shared to \"definition\" to edit the phrase, \"detach\" to detach this use, or \"pattern\" to detach every use", e.Step+1, e.Pattern, e.Phrase)
}

// RefusalError reports an intent that is refused rather than risk the music.
type RefusalError struct{ Intent, Cause string }

func (e *RefusalError) Error() string { return e.Intent + " refused: " + e.Cause }
