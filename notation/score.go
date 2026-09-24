package notation

// Position refers to the source file, with one-based line and Unicode scalar column.
type Position struct {
	Line   int
	Column int
}

type Diagnostic struct {
	Code     string
	Message  string
	Severity string // error or warning
	Position Position
}

// Score is the typed source model. Runtime project compilation is a separate
// stage, so pitch spelling and source positions remain available to tools.
type Score struct {
	Version       int
	Title         string
	TitlePosition Position
	TempoMilli    int64
	KeyRoot       string
	Scale         string
	Seed          uint64
	SeedLiteral   string
	SeedPosition  Position
	Instruments   []Instrument
	Tracks        []Track
	Phrases       []Phrase
	Patterns      []Pattern
	Scenes        []Scene
	Song          []SongEntry
	SongPosition  Position
	Effects       []Effect
}

type Track struct {
	Name     string
	Kind     string
	Params   []Param
	Position Position
}

// Instrument is source code for a custom voice. Expressions are compiled to
// a bounded DSP graph in package instrument; they are not executed by Parse.
type Instrument struct {
	Name     string
	Params   []InstrumentParam
	Mode     string
	Lets     []Let
	Output   *Expr
	Position Position
}

type InstrumentParam struct {
	Name     string
	Unit     string
	Default  string
	Position Position
}

type Let struct {
	Name     string
	Value    *Expr
	Position Position
}

type Expr struct {
	Kind     string // name, number, binary, call
	Text     string // identifier, literal, operator, or function
	Left     *Expr
	Right    *Expr
	Args     []*Expr
	Position Position
}

type Param struct {
	Name          string
	Value         string
	Position      Position
	ValuePosition Position
}

type Pattern struct {
	Name     string
	Kind     string
	Attrs    []Param
	Parts    []PatternPart // source order before expansion
	Steps    []StepToken
	Lanes    []Lane
	Position Position
}

type StepToken struct {
	Text      string
	Transpose int // phrase-use transform, in semitones
	Position  Position
}

type Phrase struct {
	Name     string
	Steps    []StepToken
	Position Position
}

type PatternPart struct {
	Step *StepToken
	Use  *PhraseUse
}

type PhraseUse struct {
	Name      string
	Repeat    int
	Transpose int
	Position  Position
}

type Lane struct {
	Name     string
	Hits     []StepToken
	Position Position
}

type Scene struct {
	Name     string
	Bindings []Binding
	Position Position
}

type Binding struct {
	Track    string
	Pattern  string
	Position Position
}

type SongEntry struct {
	Scene    string
	Bars     int
	Position Position
}

type Effect struct {
	Name     string
	Params   []Param
	Position Position
}
