package control

// CaptureState is the small polling projection of Tymbal's durable recorder.
// PCM and source candidates remain on the native side of the boundary.
type CaptureState struct {
	Active  string           `json:"activeCapture"`
	Rate    int              `json:"sampleRate"`
	Capture *CaptureSnapshot `json:"capture"`
}
type CaptureSnapshot struct {
	Recording       bool   `json:"recording"`
	RemainingFrames int64  `json:"countInRemainingFrames"`
	WrittenFrames   uint64 `json:"writtenFrames"`
	Incomplete      bool   `json:"incomplete"`
	Error           string `json:"error"`
}
