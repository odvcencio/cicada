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
	Kits          []Kit
	Tracks        []Track
	Phrases       []Phrase
	Patterns      []Pattern
	Scenes        []Scene
	Song          []SongEntry
	SongPosition  Position
	Effects       []Effect
	Buses         []Bus
	Master        []Param
	HasMaster     bool
	Exports       []Export
}

type Track struct {
	Name     string
	Kind     string
	Params   []Param
	Position Position
}

// Kit binds drum lanes to built-in voices or declared mono instruments.
type Kit struct {
	Name     string
	Bindings []KitBinding
	Position Position
}

type KitBinding struct {
	Lane     string
	Target   string
	Position Position
}

// Instrument is source code for a custom voice. Expressions are compiled to
// a bounded DSP graph in package instrument; they are not executed by Parse.
type Instrument struct {
	Name           string
	Octave         int // home register for notes without an explicit octave
	OctaveSet      bool
	OctavePosition Position
	Params         []InstrumentParam
	Mode           string
	Lets           []Let
	Output         *Expr
	Position       Position
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
	Target        string
	Pre           bool
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
	Settings []SceneSetting
	Position Position
}

type Binding struct {
	Track    string
	Pattern  string
	Position Position
}

// SceneSetting is a path-addressed value applied with the scene's bindings.
// Value keeps its source spelling until project compilation.
type SceneSetting struct {
	Path          string
	Value         string
	Position      Position
	ValuePosition Position
}

type SongEntry struct {
	Scene    string
	Bars     int
	Position Position
}

type Effect struct {
	Name     string
	Kind     string
	Legacy   bool
	Params   []Param
	Position Position
}

// Bus is a named mixer destination. P2a currently lowers only the built-in
// music and sfx buses; other declarations parse for precise diagnostics.
type Bus struct {
	Name     string
	Params   []Param
	Position Position
}

// Export stores one named render delivery target.
type Export struct {
	Name     string
	Params   []Param
	Position Position
}
