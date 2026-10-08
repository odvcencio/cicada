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

var ErrNoRenderCheck = errors.New("render check unavailable; the conversion is refused rather than risk changing the music")

var ErrNoUpgrade = errors.New("edition upgrade unavailable")

type Options struct {
	Compiler     Compiler
	RenderCheck  RenderCheck
	Path         string                // absolute path of the entry score
	Edition      int                   // from the manifest or header; 0 lets the header decide
	ManifestPath string                // "" without a manifest
	Manifest     []byte                // manifest bytes when ManifestPath != ""
	Sources      []notation.SourceFile // project sources as project.ReadSources fills them; may be nil
	Now          func() time.Time
	// ParseProject parses source the way the host parses a score on disk
	// (assets, libraries, manifest edition). nil falls back to Sources and
	// Edition. cmd/cicada injects parseScoreForPath.
	ParseProject func(source []byte) (*notation.Score, []notation.Diagnostic, error)
	// UpgradeEdition rewrites an edition-1 score (and its manifest, returned
	// as a File) to edition 2. cmd/cicada implements it with migration and
	// edition, which edit must not import (migration imports project).
	UpgradeEdition func(source []byte) (upgraded []byte, files []File, err error)
}
