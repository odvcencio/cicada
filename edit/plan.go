package edit

import (
	"crypto/sha256"
	"encoding/hex"

	"m31labs.dev/cicada/internal/paramdefs"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/musictime"
)

// Revision is the SHA-256 of the exact source bytes; Studio's studioRevision delegates to it.
func Revision(source []byte) string {
	sum := sha256.Sum256(source)
	return hex.EncodeToString(sum[:])
}

// Plan is the pure view of a compiled score that handlers resolve entities
// against. Slices follow source order, which is also engine index order.
type Plan struct {
	Revision     string
	Edition      int
	HasArrange   bool // the score has an arrange block (placements, even if none are listed)
	Title        string
	TempoMilli   int
	TempoChanges []musictime.TempoChange
	KeyRoot      uint8
	Scale        string
	Tracks       []Track
	Patterns     []Pattern
	Scenes       []Scene
	Song         []SongEntry
	Placements   []Placement
	Markers      []Marker
	Clips        []Clip
	Names        map[string]bool // occupied authored identifiers and import aliases, including templates omitted by compilation
	ResolveParam func(path string) (Param, error)
}

type Track struct {
	ID, Kind          string
	Polyphony         int
	Slots             [16]*string
	RecordingPatterns []string // Compiler-validated destinations, including unused local patterns.
}

func (t Track) canRecordPattern(id string) bool {
	for _, slot := range t.Slots {
		if slot != nil && *slot == id {
			return true
		}
	}
	for _, pattern := range t.RecordingPatterns {
		if pattern == id {
			return true
		}
	}
	return false
}

type Pattern struct {
	// CopySource emits an independent local declaration from the compiled
	// musical material, without writing or depending on its owning source.
	CopySource  func(name string) (string, error) `json:"-"`
	Performance *seq.Performance
	ID, Kind    string
	Steps       uint8
	Transpose   int8
	StepTicks   uint16 // zero means 240 (project/model.go:147)
	Data        []*Step
	Lanes       map[string][]*Step
	Expression  []NoteExpression // resolved per-step expression (project/model.go:157); used by RecordTake (T32)
}

// StepTicksOrDefault follows project/compile.go:119-125: an explicit
// step of one sixteenth is stored as zero.
func (p Pattern) StepTicksOrDefault() int64 {
	if p.StepTicks == 0 {
		return 240
	}
	return int64(p.StepTicks)
}

// LengthTicks is Steps x StepTicks. Never use Steps x seq.TicksPerStep.
func (p Pattern) LengthTicks() int64 {
	if p.Performance != nil {
		return p.Performance.LengthTicks
	}
	return int64(p.Steps) * p.StepTicksOrDefault()
}

type Step struct {
	Note                           uint8
	Accent, Slide, Tie             bool
	Ratchet, Probability, Velocity uint8
	Notes                          []int
}

type NoteExpression struct {
	PitchCents, Pressure, Timbre, VibratoDepthCents float32
}

type Scene struct {
	ID       string
	Bindings map[string]string
	Settings []Setting
}

type Setting struct {
	Path, Unit, Text string
	Number           *float64
}

type SongEntry struct {
	Scene string
	Bars  int
}

type Placement struct {
	ID, Track, Content  string
	AtTick, LengthTicks int64
}

type Marker struct {
	ID     string
	AtTick int64
}

type Clip struct {
	ID, Asset            string
	StartFrame, EndFrame int64
}

type Param struct {
	Path       string
	Descriptor paramdefs.Descriptor
	Value      *float64 // effective compiled numeric value, when supplied by the host
}
