package control

// CaptureState is the small polling projection of Tymbal's durable recorder.
// PCM and source candidates remain on the native side of the boundary.
type CaptureState struct {
	Active  string           `json:"activeCapture"`
	Rate    int              `json:"sampleRate"`
	Capture *CaptureSnapshot `json:"capture"`
	Input   InputMeterState  `json:"input"`
	Loop    *struct {
		Passes  int      `json:"passes"`
		Pass    int      `json:"pass"`
		TakeIDs []string `json:"takeIds"`
	} `json:"loopRecording"`
}
type CaptureSnapshot struct {
	Recording       bool   `json:"recording"`
	Completed       bool   `json:"completed"`
	RemainingFrames int64  `json:"countInRemainingFrames"`
	WrittenFrames   uint64 `json:"writtenFrames"`
	Incomplete      bool   `json:"incomplete"`
	Error           string `json:"error"`
}
