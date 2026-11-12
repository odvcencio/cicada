package renderlanes

// The hand-off points where the lanestress hook (StressPoint) may delay a
// goroutine. Each call passes its site and two values it already holds, such
// as the segment number and the lane, so the hook needs no shared state.
const (
	StressSegmentPublished = iota + 1 // Begin, after the segment store
	StressBeforeWake                  // Begin, between a wake compare-and-swap and its send
	StressAfterWake                   // Begin, after each lane's wake
	StressReadyChecked                // Ready, between reached() and failed.Load()
	StressSegmentSeen                 // work, after a lane takes a new segment
	StressBeforeDone                  // work, before a lane reports it is done
	StressParked                      // nextSegment, between parked.Store and the segment load
	StressBeforeUnpark                // nextSegment, before the compare-and-swap that unparks
	StressWoken                       // nextSegment, after a wake arrives
	StressFailurePublish              // renderLane, before a failed lane's progress store
	StressChunkPublish                // renderLane, before each chunk's progress store
	StressTrackStart                  // renderTrack (engine and direct mixer), before a track's frames
	StressFaultPublish                // renderTrack, before a fault's frame store
)
