package project

import "m31labs.dev/cicada/instrument"

// instrumentCache remembers compileInstrument's result per declared
// instrument value, inside one State. compileInstrument writes the instrument
// as a small score and parses it, so ValidateProject and the engine build
// each parse every instrument again; a cache compiles each value once.
//
// The result depends only on the instrument's source text and its ID
// (compileInstrument's score is built from nothing else), so that pair is
// the key. A nil cache compiles every time. A cache is not safe for
// concurrent use.
//
// The programs it returns are shared by every caller and must not be
// changed: ValidateProject and the engine build only read them
// (instrument.LowerWithModulators builds a new graph program).
type instrumentCache struct {
	entries map[string]instrumentResult
	// previous is the cache of the state this one was derived from (State.With).
	// An entry found there is copied here, so each cache holds only the
	// instruments of its own project, and dropping a state drops its
	// instruments.
	previous *instrumentCache
}

type instrumentResult struct {
	program *instrument.Program
	err     error
}

func newInstrumentCache(previous *instrumentCache) *instrumentCache {
	// A state that was only parsed, such as one between two intents of a
	// batch, compiled no instrument: look past it to the last state that did.
	for previous != nil && len(previous.entries) == 0 {
		previous = previous.previous
	}
	return &instrumentCache{entries: map[string]instrumentResult{}, previous: previous}
}

func (c *instrumentCache) compile(inst Instrument) (*instrument.Program, error) {
	if c == nil {
		return compileInstrument(inst)
	}
	source, err := instrumentSource(inst)
	if err != nil {
		// compileInstrument fails the same way before it parses anything.
		return compileInstrument(inst)
	}
	key := inst.ID + "\x00" + source
	if result, ok := c.entries[key]; ok {
		return result.program, result.err
	}
	if c.previous != nil {
		if result, ok := c.previous.entries[key]; ok {
			c.entries[key] = result
			return result.program, result.err
		}
	}
	program, err := compileInstrument(inst)
	c.entries[key] = instrumentResult{program: program, err: err}
	return program, err
}

// forget drops the link to the previous state's cache, so that cache can be
// collected.
func (c *instrumentCache) forget() {
	if c != nil {
		c.previous = nil
	}
}
