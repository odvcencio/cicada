//go:build cicada_counters

package applycount

import "sync/atomic"

// Enabled is true only in builds with the cicada_counters tag.
const Enabled = true

var (
	syntaxParses        atomic.Int64
	bytesParsed         atomic.Int64
	itemsLowered        atomic.Int64
	notationValidations atomic.Int64
	entitiesCompiled    atomic.Int64
	projectValidations  atomic.Int64
	engineBuilds        atomic.Int64
	editPlans           atomic.Int64
	revisionHashes      atomic.Int64
	bytesHashed         atomic.Int64
)

// Snapshot reads every counter.
func Snapshot() Counts {
	return Counts{
		SyntaxParses:        syntaxParses.Load(),
		BytesParsed:         bytesParsed.Load(),
		ItemsLowered:        itemsLowered.Load(),
		NotationValidations: notationValidations.Load(),
		EntitiesCompiled:    entitiesCompiled.Load(),
		ProjectValidations:  projectValidations.Load(),
		EngineBuilds:        engineBuilds.Load(),
		EditPlans:           editPlans.Load(),
		RevisionHashes:      revisionHashes.Load(),
		BytesHashed:         bytesHashed.Load(),
	}
}

func SyntaxParse(bytes int) {
	syntaxParses.Add(1)
	bytesParsed.Add(int64(bytes))
}

func ItemsLowered(n int)     { itemsLowered.Add(int64(n)) }
func NotationValidation()    { notationValidations.Add(1) }
func EntitiesCompiled(n int) { entitiesCompiled.Add(int64(n)) }
func ProjectValidation()     { projectValidations.Add(1) }
func EngineBuild()           { engineBuilds.Add(1) }
func PlanBuilt()             { editPlans.Add(1) }

func RevisionHash(bytes int) {
	revisionHashes.Add(1)
	bytesHashed.Add(int64(bytes))
}
