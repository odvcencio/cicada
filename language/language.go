// Package language is Cicada's editor tooling. The tree-sitter queries next
// to cicada.grammar classify every construct in a score; this package runs
// them with gotreesitter to highlight, outline, and render scores.
package language

import (
	_ "embed"
	"fmt"
	"sync"

	gts "github.com/odvcencio/gotreesitter"

	"m31labs.dev/cicada/notation"
)

// Query sources in Neovim's query formats. Editors that read queries from
// disk can use the .scm files next to cicada.grammar.
var (
	//go:embed highlights.scm
	HighlightsQuery string
	//go:embed locals.scm
	LocalsQuery string
	//go:embed tags.scm
	TagsQuery string
	//go:embed folds.scm
	FoldsQuery string
	//go:embed indents.scm
	IndentsQuery string
)

type compiled struct {
	lang       *gts.Language
	highlights *gts.Query
	locals     *gts.Query
}

var (
	compileOnce sync.Once
	queries     compiled
	compileErr  error
)

// load compiles the queries once. A compiled gotreesitter Query is safe for
// concurrent execution.
func load() (*compiled, error) {
	compileOnce.Do(func() {
		queries.lang, compileErr = notation.Language()
		if compileErr != nil {
			return
		}
		if queries.highlights, compileErr = gts.NewQuery(HighlightsQuery, queries.lang); compileErr != nil {
			compileErr = fmt.Errorf("highlights.scm: %w", compileErr)
			return
		}
		if queries.locals, compileErr = gts.NewQuery(LocalsQuery, queries.lang); compileErr != nil {
			compileErr = fmt.Errorf("locals.scm: %w", compileErr)
		}
	})
	return &queries, compileErr
}

// NewHighlighter returns a gotreesitter Highlighter over highlights.scm. It
// yields flat ranges, one capture per byte, for hosts that paint that way.
// Highlight keeps nested spans and resolves locals.
func NewHighlighter(opts ...gts.HighlighterOption) (*gts.Highlighter, error) {
	lang, err := notation.Language()
	if err != nil {
		return nil, err
	}
	return gts.NewHighlighter(lang, HighlightsQuery, opts...)
}

// NewTagger returns a gotreesitter Tagger over tags.scm.
func NewTagger(opts ...gts.TaggerOption) (*gts.Tagger, error) {
	lang, err := notation.Language()
	if err != nil {
		return nil, err
	}
	return gts.NewTagger(lang, TagsQuery, opts...)
}

func parse(src []byte) (*gts.Tree, *compiled, error) {
	q, err := load()
	if err != nil {
		return nil, nil, err
	}
	tree, err := gts.NewParser(q.lang).Parse(src)
	if err != nil {
		return nil, nil, err
	}
	if tree.RootNode() == nil {
		return nil, nil, fmt.Errorf("parse produced no tree")
	}
	return tree, q, nil
}
