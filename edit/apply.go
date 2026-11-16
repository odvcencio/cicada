package edit

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
)

type Result struct {
	Source []byte
	Files  []File
	Label  string
	// Labels holds, for each intent in order, the history label it set, or
	// "" when it set none.
	Labels []string
	// Warnings holds, for each intent in order, the changes it made that the
	// agent accepted with a flag, such as random choices that change.
	Warnings [][]string
	Response map[string]any
	Plan     *Plan // compiled candidate
	// Invalid is the compile error of a candidate that Options.KeepInvalid
	// kept; Plan is nil then.
	Invalid   error
	Unchanged bool
	// RenderChecked reports that an intent consulted the render check, so the
	// result depends on rendered audio and not only on source text.
	RenderChecked bool
	// RenderIdentity is the identity of exactly the inputs the renders used
	// (the working source at that point in the batch, the files staged so far
	// and what they reference), taken before the first render and confirmed
	// after the last; empty when the check does not identify inputs.
	// RenderRecheck recomputes it from the same sources and staged files so a
	// host can compare it again right before committing.
	RenderIdentity string
	RenderRecheck  func() (identity string, ok bool)
}

// File is an auxiliary source the commit writes atomically with the score.
type File struct {
	Path          string
	Before, After []byte
}

type Handler func(ctx *Context, intent Intent) error

var handlers = map[string]Handler{}

// Handle binds a handler to a registered kind.
func Handle(kind string, handler Handler) {
	if _, ok := registry[kind]; !ok {
		panic("edit: handler for unregistered kind " + kind)
	}
	handlers[kind] = handler
}

type Context struct {
	Source       []byte // working source; handlers replace it
	Options      Options
	Envelope     Envelope
	plan         *Plan
	parseProject func([]byte, map[string][]byte) (*notation.Score, []notation.Diagnostic, error)
	files        []File
	fileIndex    map[string]int
	label        string
	labelSet     bool
	rendered     bool
	identities   []string                // verified identity of each render comparison
	rechecks     []func() (string, bool) // recompute each identity from the same inputs
	response     map[string]any
	running      int          // the index of the intent that runs
	final        []finalCheck // checks that run after the last intent
	warnings     []string     // what the running intent changed that its label does not say
	meters       meterCache
}

// warn records, for the running intent, a change the result reports beside
// the label: something the edit changes that the agent asked to accept.
func (c *Context) warn(format string, args ...any) {
	c.warnings = append(c.warnings, fmt.Sprintf(format, args...))
}

// finalCheck is a check an intent leaves for the end of the envelope, when
// later intents have had their turn; a failure names that intent.
type finalCheck struct {
	index int
	kind  string
	check func(*Context) error
}

// afterIntents runs check after the envelope's last intent and before the
// compile, and reports its error as the running intent's.
func (c *Context) afterIntents(check func(*Context) error) {
	kind := ""
	if c.running < len(c.Envelope.Intents) {
		kind = c.Envelope.Intents[c.running].Kind()
	}
	c.final = append(c.final, finalCheck{index: c.running, kind: kind, check: check})
}

// meterCache keeps the meter map of one working source.
type meterCache struct {
	source []byte
	valid  bool
	m      seq.MeterMap
}

// MeterMap places the edited song's bar and beat lines: the map of the whole
// project as the working source stands, or 4/4 when no project file declares
// a meter or the project does not parse. It is read again only when the
// working source changes.
func (c *Context) MeterMap() seq.MeterMap {
	if c.meters.valid && bytes.Equal(c.meters.source, c.Source) {
		return c.meters.m
	}
	var m seq.MeterMap
	if c.declaresMeter() {
		if score, _, err := c.ParseProject(); err == nil && score != nil {
			m = score.MeterMap()
		}
	}
	c.meters = meterCache{source: append(c.meters.source[:0], c.Source...), valid: true, m: m}
	return m
}

// declaresMeter reports whether any project file could declare a meter, so
// a song without one never pays for a parse.
func (c *Context) declaresMeter() bool {
	word := []byte("meter")
	if bytes.Contains(c.Source, word) {
		return true
	}
	for _, file := range c.Options.Sources {
		if file.Path != c.Options.Path && bytes.Contains(file.Source, word) {
			return true
		}
	}
	return false
}

// Parse parses the working buffer. Recording token writers can retain the
// complete project context when the buffer owns only part of the score.
func (c *Context) Parse() (*notation.Score, []notation.Diagnostic) {
	if c.parseProject != nil {
		score, ds, err := c.ParseProject()
		if err != nil {
			return nil, []notation.Diagnostic{{Severity: "error", Message: err.Error()}}
		}
		return score, ds
	}
	if c.Options.Edition == 0 {
		return notation.Parse(c.Source)
	}
	return notation.ParseEdition(c.Source, c.Options.Edition)
}

// ParseProject parses with the other project sources, through
// Options.ParseProject when the host provides it.
func (c *Context) ParseProject() (*notation.Score, []notation.Diagnostic, error) {
	if c.parseProject != nil {
		return c.parseProject(c.Source, c.fileOverrides())
	}
	if c.Options.ParseProject != nil {
		return c.Options.ParseProject(c.Source, c.fileOverrides())
	}
	if len(c.Options.Sources) == 0 {
		score, ds := c.Parse()
		return score, ds, nil
	}
	files := append([]notation.SourceFile(nil), c.Options.Sources...)
	entryFound := false
	for i := range files {
		files[i].Source, _ = c.FileSource(files[i].Path)
		entryFound = entryFound || files[i].Path == c.Options.Path
	}
	if !entryFound {
		files = append([]notation.SourceFile{{Path: c.Options.Path, Source: c.Source}}, files...)
	}
	score, ds := notation.ParseFiles(files, c.Options.Edition)
	return score, ds, nil
}

// CurrentPlan compiles the working source once for entity resolution.
func (c *Context) CurrentPlan() (*Plan, error) {
	if c.plan != nil {
		return c.plan, nil
	}
	plan, err := c.Options.Compiler.Compile(c.Source, c.fileOverrides())
	if err != nil {
		return nil, err
	}
	c.plan = plan
	return plan, nil
}

func (c *Context) SetLabel(label string) { c.label, c.labelSet = label, true }
func (c *Context) Respond(key string, value any) {
	if c.response == nil {
		c.response = map[string]any{}
	}
	c.response[key] = value
}

// FileSource returns the latest working bytes for a supplied source or
// manifest. It never reads disk, and the returned copy is safe to edit.
func (c *Context) FileSource(path string) ([]byte, bool) {
	if path == c.Options.Path {
		return bytes.Clone(c.Source), true
	}
	if index, ok := c.fileIndex[path]; ok {
		return bytes.Clone(c.files[index].After), true
	}
	if path != "" && path == c.Options.ManifestPath {
		return bytes.Clone(c.Options.Manifest), true
	}
	for _, file := range c.Options.Sources {
		if file.Path == path {
			return bytes.Clone(file.Source), true
		}
	}
	return nil, false
}

// AddFile stages one cumulative change per path, retaining the first Before
// for the commit's revision check. Callers patch FileSource's working bytes.
func (c *Context) AddFile(file File) {
	if c.fileIndex == nil {
		c.fileIndex = make(map[string]int)
	}
	if index, ok := c.fileIndex[file.Path]; ok {
		c.files[index].After = bytes.Clone(file.After)
	} else {
		c.fileIndex[file.Path] = len(c.files)
		c.files = append(c.files, File{Path: file.Path, Before: bytes.Clone(file.Before), After: bytes.Clone(file.After)})
	}
	c.plan = nil
}

func (c *Context) fileOverrides() map[string][]byte {
	files := make(map[string][]byte, len(c.files))
	for _, file := range c.files {
		files[file.Path] = bytes.Clone(file.After)
	}
	return files
}

func hasErrors(ds []notation.Diagnostic) bool {
	for _, d := range ds {
		if d.Severity == "error" {
			return true
		}
	}
	return false
}

// Apply runs the intents in order on a copy of source, then validates and
// compiles the candidate. It never touches disk.
func Apply(source []byte, env Envelope, opts Options) (*Result, error) {
	if opts.Compiler == nil {
		return nil, errors.New("edit: Options.Compiler is required")
	}
	if env.Version != EnvelopeVersion {
		return nil, fmt.Errorf("unsupported envelope version %d", env.Version)
	}
	if env.Revision != "" && env.Revision != Revision(source) {
		return nil, &ConflictError{Expected: env.Revision, Actual: Revision(source)}
	}
	ctx := &Context{Source: bytes.Clone(source), Options: opts, Envelope: env}
	labels := make([]string, 0, len(env.Intents))
	warnings := make([][]string, 0, len(env.Intents))
	for index, intent := range env.Intents {
		handler := handlers[intent.Kind()]
		if handler == nil {
			return nil, intentError(index, intent.Kind(), fmt.Errorf("no handler for intent kind %q", intent.Kind()))
		}
		ctx.labelSet, ctx.running, ctx.warnings = false, index, nil
		if err := handler(ctx, intent); err != nil {
			return nil, intentError(index, intent.Kind(), err)
		}
		if ctx.labelSet {
			labels = append(labels, ctx.label)
		} else {
			labels = append(labels, "")
		}
		warnings = append(warnings, ctx.warnings)
		// A handler may replace Source without resolving a plan. The next
		// intent must compile the latest entry and auxiliary bytes together.
		ctx.plan = nil
	}
	for _, final := range ctx.final {
		ctx.plan = nil
		if err := final.check(ctx); err != nil {
			return nil, intentError(final.index, final.kind, err)
		}
	}
	plan, err := opts.Compiler.Compile(ctx.Source, ctx.fileOverrides())
	if err != nil && opts.KeepInvalid {
		return &Result{Source: ctx.Source, Files: ctx.files, Label: ctx.label, Labels: labels, Invalid: err, Unchanged: bytes.Equal(ctx.Source, source) && len(ctx.files) == 0}, nil
	}
	if err != nil {
		return nil, err
	}
	result := &Result{Source: ctx.Source, Files: ctx.files, Label: ctx.label, Labels: labels, Warnings: warnings, Response: ctx.response, Plan: plan, RenderChecked: ctx.rendered}
	if len(ctx.rechecks) > 0 {
		result.RenderIdentity = strings.Join(ctx.identities, "\n")
		rechecks := ctx.rechecks
		result.RenderRecheck = func() (string, bool) {
			parts := make([]string, 0, len(rechecks))
			for _, recheck := range rechecks {
				identity, ok := recheck()
				if !ok {
					return "", false
				}
				parts = append(parts, identity)
			}
			return strings.Join(parts, "\n"), true
		}
	}
	result.Unchanged = bytes.Equal(ctx.Source, source) && len(ctx.files) == 0
	return result, nil
}
