//go:build tinygo || js

package render

// Browser builds render serially: they never start lanes.
type trackLanes struct {
	end      int
	prepared bool
}

func resolveJobs(int) int                                       { return 1 }
func plannedLanes(int, int) int                                 { return 1 }
func newTrackLanes([]trackRuntime, int, int) *trackLanes        { return nil }
func (l *trackLanes) lanes() int                                { return 1 }
func (l *trackLanes) counts() laneCounts                        { return laneCounts{} }
func (l *trackLanes) close()                                    {}
func (l *trackLanes) begin()                                    {}
func (l *trackLanes) finish()                                   {}
func (l *trackLanes) prepare([]trackRuntime, int, int)          {}
func (l *trackLanes) sample(int, int) (float32, float32, error) { return 0, 0, nil }
