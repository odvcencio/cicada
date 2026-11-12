package engine

// OfflineLaneCounts summarize how an offline render used its lanes
// (EnableOfflineParallel), for a host's render report.
type OfflineLaneCounts struct {
	// Segments counts the runs of frames between control changes offered to
	// the lanes, short ones included; ParallelSegments, those that ran on two
	// or more lanes; StarvedSegments, those worth more lanes that found no
	// processor free.
	Segments, ParallelSegments, StarvedSegments int64
	// Peak is the most lanes that rendered one segment at once.
	Peak int
}
