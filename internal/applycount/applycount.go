// Package applycount counts the work one edit does: syntax parses, items
// lowered, entities compiled, validation passes, engine builds, edit plans and
// revision hashes. The edit benchmark (internal/benchscore/applybench) reads
// the counts around each edit.Apply, so a change that makes an edit cost less
// shows up as a smaller count, not only as a shorter time.
//
// Counting exists only in builds with the cicada_counters tag. In every other
// build Enabled is the constant false, and each hook in notation, project and
// edit sits behind "if applycount.Enabled", which the compiler removes, so a
// normal build runs and links none of it.
//
// The counts are process-wide. Read them with Snapshot before and after the
// work and subtract; nothing else may parse or compile in between.
package applycount

// Counts is one reading of every counter.
type Counts struct {
	// SyntaxParses counts full parses of source text into a syntax tree
	// (notation's parseSyntax: lowering, declaration names, imports, documents).
	SyntaxParses int64
	// BytesParsed is the source bytes handed to those parses.
	BytesParsed int64
	// ItemsLowered counts top-level declarations lowered into a notation.Score.
	ItemsLowered int64
	// NotationValidations counts notation.Validate passes over a whole score.
	NotationValidations int64
	// EntitiesCompiled counts the declarations project.FromScore compiles into
	// project entities: assets, clips, samplers, instruments, kits, routed
	// effects, tracks, buses, exports, patterns, scenes, automation lanes,
	// modulators, routes, placements and markers.
	EntitiesCompiled int64
	// ProjectValidations counts project.ValidateProject passes.
	ProjectValidations int64
	// EngineBuilds counts engine configurations built from a whole project:
	// the schedule rules (host/schedule.Validate), cicada check's engine
	// compile and every render build one.
	EngineBuilds int64
	// EditPlans counts project.EditPlan views built from a compiled project.
	EditPlans int64
	// RevisionHashes counts SHA-256 revisions of source text (edit.Revision).
	RevisionHashes int64
	// BytesHashed is the source bytes those hashes read.
	BytesHashed int64
}

// Sub returns c minus before, the work done between two readings.
func (c Counts) Sub(before Counts) Counts {
	return Counts{
		SyntaxParses:        c.SyntaxParses - before.SyntaxParses,
		BytesParsed:         c.BytesParsed - before.BytesParsed,
		ItemsLowered:        c.ItemsLowered - before.ItemsLowered,
		NotationValidations: c.NotationValidations - before.NotationValidations,
		EntitiesCompiled:    c.EntitiesCompiled - before.EntitiesCompiled,
		ProjectValidations:  c.ProjectValidations - before.ProjectValidations,
		EngineBuilds:        c.EngineBuilds - before.EngineBuilds,
		EditPlans:           c.EditPlans - before.EditPlans,
		RevisionHashes:      c.RevisionHashes - before.RevisionHashes,
		BytesHashed:         c.BytesHashed - before.BytesHashed,
	}
}
