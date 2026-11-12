package edit

import (
	"errors"
	"time"

	"m31labs.dev/cicada/notation"
)

// Compiler validates and compiles a candidate. files maps absolute paths to
// replacement bytes for other project sources. The error text is what the
// user sees, so implementations return the diagnostic text Studio shows today.
type Compiler interface {
	Compile(source []byte, files map[string][]byte) (*Plan, error)
}

// RenderCheck renders source deterministically and returns the SHA-256 of
// its PCM24 output. nil means intents that need it refuse with ErrNoRenderCheck.
type RenderCheck interface {
	Hash(source []byte) (string, error)
}

// StagedRenderCheck is a RenderCheck that can render the state a batch would
// commit: files holds the cumulative auxiliary files staged by earlier intents
// (path -> bytes, for example an upgraded cicada.mod). Hosts' checks implement
// it so a render never reads a superseded file from disk.
type StagedRenderCheck interface {
	RenderCheck
	HashWith(source []byte, files map[string][]byte) (string, error)
}

// SourceRenderCheck binds the same project render to another owned source.
// That source's before/after buffers replace its bytes, while files pins the
// entry and earlier batch edits. Manifest resolution still loads the project.
type SourceRenderCheck interface {
	RenderCheck
	ForSource(path string, files map[string][]byte) (RenderCheck, error)
}

// SnapshotRenderCheck compares two candidates against one snapshot of the
// project's source inputs (the files staged by the batch laid over unsaved
// buffers over disk), so a file changed, or changed and restored, while the
// renders run cannot make them read different inputs. Assets still load from
// disk and are restated after rendering.
type SnapshotRenderCheck interface {
	StagedRenderCheck
	// Compare renders before and after from one snapshot and reports whether
	// they match. identity names that snapshot and both candidates.
	Compare(before, after []byte, files map[string][]byte) (same bool, identity string, err error)
	// PairIdentity names the same inputs as they are now; ok is false when they
	// cannot be identified.
	PairIdentity(before, after []byte, files map[string][]byte) (identity string, ok bool)
}

// ErrProjectChanged is returned by a render check, and becomes a refusal, when
// project inputs changed (or cannot be identified) while a render ran, so the
// result must not be used or remembered.
var ErrProjectChanged = errors.New("the project changed during the render check; try again")

var ErrNoRenderCheck = errors.New("render check unavailable; the conversion is refused rather than risk changing the music")

var ErrNoUpgrade = errors.New("edition upgrade unavailable")

// ParamWriter says which writer a SetParam runs. Routes pin it; the generic
// intent surface leaves it empty and resolves by entity.
type ParamWriter string

const (
	ParamWriterAuto       ParamWriter = ""
	ParamWriterInstrument ParamWriter = "instrument"
	ParamWriterMixer      ParamWriter = "mixer"
)

type Options struct {
	// KeepInvalid makes Apply return the edited source even when it does not
	// compile, with the compile error in Result.Invalid and no Plan. A host
	// uses it to compare diagnostics before and after an edit, as cicada
	// check does to prove a fix; such a result must never be written.
	KeepInvalid bool
	// ParamWriter pins SetParam to one writer. It belongs to the route that
	// calls Apply, so it is not part of any intent's JSON.
	ParamWriter ParamWriter
	// MixerGuard, when set, runs before any mixer write, whether the route
	// pinned the mixer writer or the generic surface resolved to it. A non-nil
	// error refuses the whole envelope, so no earlier intent is committed.
	MixerGuard func() error
	// CaptureGuard, when set, runs before an automation or tempo edit. A non-nil
	// error refuses the whole envelope. Hosts use it to refuse score edits
	// while a recording is open.
	CaptureGuard func() error
	Compiler     Compiler
	// Base is the compiled state of the source passed to Apply, with no
	// staged files, that the host kept from an earlier edit or read: Apply
	// reads the unchanged source from it, and builds the state of each later
	// set of bytes from it, so it parses each set of bytes once. It must come
	// from Compiler (StateCompiler) and hold exactly the bytes passed to Apply
	// and the saved project's other files; Apply compares the entry's bytes
	// and moves to a new state when they differ. nil builds the base once
	// through Compiler when it is a StateCompiler. After a refused edit Base
	// is as it was.
	Base         State
	RenderCheck  RenderCheck
	Path         string                // absolute path of the entry score
	Edition      int                   // from the manifest or header; 0 lets the header decide
	ManifestPath string                // "" without a manifest
	Manifest     []byte                // manifest bytes when ManifestPath != ""
	Sources      []notation.SourceFile // project sources as project.ReadSources fills them; may be nil
	Now          func() time.Time
	// ParseProject parses source the way the host parses a score on disk
	// (assets, libraries, manifest edition). nil falls back to Sources and
	// Edition. files supplies the cumulative auxiliary overrides, including
	// the manifest, and the host must overlay them on its project inputs.
	ParseProject func(source []byte, files map[string][]byte) (*notation.Score, []notation.Diagnostic, error)
	// Diagnose returns every diagnostic cicada check gives the project with
	// the entry replaced by source and the staged files, errors and
	// warnings. The structural intents use it to name every problem a
	// change causes. Nil leaves the parse and compile errors.
	Diagnose func(source []byte, files map[string][]byte) ([]notation.Diagnostic, error)
	// UpgradeEdition rewrites an edition-1 score (and its manifest, returned
	// as a File) to edition 2. cmd/cicada implements it with migration and
	// edition, which edit must not import (migration imports project).
	// staged supplies the latest auxiliary bytes; the hook must use them
	// before falling back to its original manifest.
	UpgradeEdition func(source []byte, staged map[string][]byte) (upgraded []byte, files []File, err error)
}
