package edit

import (
	"crypto/sha256"
	"encoding/hex"

	"m31labs.dev/cicada/internal/paramdefs"
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
	Title        string
	TempoMilli   int
	KeyRoot      uint8
	Scale        string
	Tracks       []Track
	Patterns     []Pattern
	Scenes       []Scene
	Song         []SongEntry
	Placements   []Placement
	Markers      []Marker
	Clips        []Clip
	Names        map[string]bool // every declared identifier (samplers, instruments, kits, tracks, patterns, clips, effects, buses)
	ResolveParam func(path string) (Param, error)
}

type Track struct {
	ID, Kind  string
	Polyphony int
	Slots     [16]*string
}

type Pattern struct {
	ID, Kind   string
	Steps      uint8
	Transpose  int8
	StepTicks  uint16 // zero means 240 (project/model.go:147)
	Data       []*Step
	Lanes      map[string][]*Step
	Expression []NoteExpression // resolved per-step expression (project/model.go:157); used by RecordTake (T32)
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
func (p Pattern) LengthTicks() int64 { return int64(p.Steps) * p.StepTicksOrDefault() }

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
}
