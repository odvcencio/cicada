package edit

import (
	"bytes"
	"errors"
	"fmt"

	"m31labs.dev/cicada/notation"
)

type Result struct {
	Source    []byte
	Files     []File
	Label     string
	Response  map[string]any
	Plan      *Plan // compiled candidate
	Unchanged bool
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
	Source    []byte // working source; handlers replace it
	Options   Options
	Envelope  Envelope
	plan      *Plan
	files     []File
	fileIndex map[string]int
	label     string
	response  map[string]any
}

// Parse parses the working source standalone, as today's writers do.
func (c *Context) Parse() (*notation.Score, []notation.Diagnostic) {
	if c.Options.Edition == 0 {
		return notation.Parse(c.Source)
	}
	return notation.ParseEdition(c.Source, c.Options.Edition)
}

// ParseProject parses with the other project sources, through
// Options.ParseProject when the host provides it.
func (c *Context) ParseProject() (*notation.Score, []notation.Diagnostic, error) {
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

func (c *Context) SetLabel(label string) { c.label = label }
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
	for _, intent := range env.Intents {
		handler := handlers[intent.Kind()]
		if handler == nil {
			return nil, fmt.Errorf("no handler for intent kind %q", intent.Kind())
		}
		if err := handler(ctx, intent); err != nil {
			return nil, err
		}
		// A handler may replace Source without resolving a plan. The next
		// intent must compile the latest entry and auxiliary bytes together.
		ctx.plan = nil
	}
	plan, err := opts.Compiler.Compile(ctx.Source, ctx.fileOverrides())
	if err != nil {
		return nil, err
	}
	result := &Result{Source: ctx.Source, Files: ctx.files, Label: ctx.label, Response: ctx.response, Plan: plan}
	result.Unchanged = bytes.Equal(ctx.Source, source) && len(ctx.files) == 0
	return result, nil
}
