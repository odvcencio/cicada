//go:build !cicada_counters

package applycount

// Enabled is false in a normal build: every hook is dead code.
const Enabled = false

// Snapshot returns zero counts in a normal build.
func Snapshot() Counts { return Counts{} }

func SyntaxParse(bytes int)  {}
func ItemsLowered(n int)     {}
func NotationValidation()    {}
func EntitiesCompiled(n int) {}
func ProjectValidation()     {}
func EngineBuild()           {}
func PlanBuilt()             {}
func RevisionHash(bytes int) {}
