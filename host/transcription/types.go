// Package transcription converts monophonic recordings into score notes.
// Analysis runs outside the audio callback.
package transcription

// Options controls score inference and rhythmic quantization.
type Options struct {
	Tempo  float64 `json:"tempo"`
	Key    string  `json:"key"`
	Grid   int     `json:"grid"`
	Strict float64 `json:"strict"`
}

func DefaultOptions() Options {
	return Options{Key: "auto", Grid: 16, Strict: 1}
}

// PitchPoint retains pitch movement in cents relative to the nearest MIDI note.
// Time is measured in seconds from the beginning of the recording.
type PitchPoint struct {
	Time  float64 `json:"time"`
	Cents float64 `json:"cents"`
}

// Note contains measured timing and pitch before rhythmic quantization.
// MIDI is fractional; Cents is its deviation from the nearest MIDI note.
type Note struct {
	Start      float64      `json:"start"`
	End        float64      `json:"end"`
	PitchHz    float64      `json:"pitchHz"`
	MIDI       float64      `json:"midi"`
	Cents      float64      `json:"cents"`
	Confidence float64      `json:"confidence"`
	Velocity   int          `json:"velocity"`
	Pitch      []PitchPoint `json:"pitch,omitempty"`
}

type Result struct {
	Notes           []Note   `json:"notes"`
	Tempo           float64  `json:"tempo"`
	TempoConfidence float64  `json:"tempoConfidence"`
	Meter           string   `json:"meter"`
	MeterConfidence float64  `json:"meterConfidence"`
	Key             string   `json:"key"`
	KeyConfidence   float64  `json:"keyConfidence"`
	Warnings        []string `json:"warnings,omitempty"`
	Source          string   `json:"source"`
	InputSHA256     string   `json:"inputSHA256,omitempty"`
	QuantizationMS  float64  `json:"quantizationMS"`
}
