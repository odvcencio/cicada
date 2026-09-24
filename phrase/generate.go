package phrase

import (
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
)

// Generate creates a seeded acid phrase and the same music as parseable
// Cicada source. Structure variants reuse equal A bars by value.
func Generate(params Params) (Result, error) {
	p, err := normalize(params)
	if err != nil {
		return Result{}, err
	}
	a, trace, err := buildBaseBar(p)
	if err != nil {
		return Result{}, err
	}
	bars := map[byte][]noteState{'a': a}
	var order []byte
	switch p.Structure {
	case A:
		order = []byte{'a'}
	case AABA:
		order = []byte{'a', 'a', 'b', 'a'}
	case ABAB:
		order = []byte{'a', 'b', 'a', 'b'}
	case ABAC:
		order = []byte{'a', 'b', 'a', 'c'}
	case AAAB:
		order = []byte{'a', 'a', 'a', 'b'}
	}
	if p.Structure != A {
		b, draws, err := mutateNotes(a, p.Seed^0xB, []Op{{Kind: NudgeDegree}, {Kind: ToggleAccent}, {Kind: ToggleSlide}}, 0, p)
		if err != nil {
			return Result{}, err
		}
		bars['b'] = b
		trace = append(trace, draws...)
	}
	if p.Structure == ABAC {
		c, draws, err := mutateNotes(a, p.Seed^0xC, []Op{{Kind: RotateRhythm, Arg: 2}, {Kind: NudgeDegree}, {Kind: NudgeDegree}}, 0, p)
		if err != nil {
			return Result{}, err
		}
		bars['c'] = c
		trace = append(trace, draws...)
	}
	result := Result{Bars: make([]seq.Pattern, 0, len(order)), Trace: trace}
	for _, letter := range order {
		pattern, err := encodeBar(bars[letter], p)
		if err != nil {
			return Result{}, err
		}
		result.Bars = append(result.Bars, pattern)
	}
	result.Notation, err = sourceForBars(bars, order, p)
	if err != nil {
		return Result{}, err
	}
	document, err := notation.ParseDocument([]byte(result.Notation))
	if err != nil {
		return Result{}, err
	}
	formatted, err := notation.Format(document)
	if err != nil {
		return Result{}, err
	}
	result.Notation = string(formatted)
	return result, nil
}
