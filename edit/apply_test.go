package edit

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

type fakeCompiler struct{ calls int }

func (c *fakeCompiler) Compile(source []byte, files map[string][]byte) (*Plan, error) {
	c.calls++
	if bytes.Contains(source, []byte("???")) {
		return nil, errors.New("1:1 CICADA-SYNTAX: bad")
	}
	return &Plan{Revision: Revision(source)}, nil
}

func TestApplyWithNoIntentsIsByteIdentical(t *testing.T) {
	source := []byte("title \"x\"\n")
	compiler := &fakeCompiler{}
	result, err := Apply(source, Envelope{Version: 1, Revision: Revision(source)}, Options{Compiler: compiler})
	if err != nil || !bytes.Equal(result.Source, source) || !result.Unchanged || compiler.calls != 1 {
		t.Fatalf("%+v %v calls=%d", result, err, compiler.calls)
	}
}

func TestApplyChecksRevisionAndCompilesCandidate(t *testing.T) {
	source := []byte("title \"x\"\n")
	_, err := Apply(source, Envelope{Version: 1, Revision: "stale"}, Options{Compiler: &fakeCompiler{}})
	var conflict *ConflictError
	if !errors.As(err, &conflict) || conflict.Actual != Revision(source) {
		t.Fatalf("%v", err)
	}
	_, err = Apply(source, Envelope{Version: 1, Intents: []Intent{&breakIntent{}}}, Options{Compiler: &fakeCompiler{}})
	if err == nil || !strings.Contains(err.Error(), "CICADA-SYNTAX") {
		t.Fatalf("candidate compile error not surfaced: %v", err)
	}
	if _, err := Apply(source, Envelope{Version: 1}, Options{}); err == nil {
		t.Fatal("nil compiler accepted")
	}
}

type breakIntent struct{}

func (breakIntent) Kind() string { return "break" }

func init() {
	Register("break", func() Intent { return &breakIntent{} })
	Handle("break", func(ctx *Context, _ Intent) error { ctx.Source = append(ctx.Source, "???"...); return nil })
}
