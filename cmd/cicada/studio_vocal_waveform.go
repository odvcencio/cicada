package main

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"sync"

	"m31labs.dev/cicada/host/sampleasset"
	"m31labs.dev/cicada/host/takejournal"
)

type vocalWaveform struct {
	Take  string       `json:"take"`
	Start int64        `json:"startTick"`
	End   int64        `json:"endTick"`
	Peaks [][2]float32 `json:"peaks"`
	Peak  float64      `json:"peak"`
}

// Immutable peak summaries stay separate from recorder/source locks and PCM
// admission. Serialize analysis to bound disk work; retain at most 16 summaries.
type vocalWaveformCache struct {
	mu    sync.Mutex
	items map[string]vocalWaveform
	order []string
}

func (s *studio) takeWaveform(w http.ResponseWriter, r *http.Request) {
	bins := 4096
	if value := r.URL.Query().Get("bins"); value != "" {
		var err error
		bins, err = strconv.Atoi(value)
		if err != nil || bins < 64 || bins > 8192 {
			studioJSON(w, http.StatusBadRequest, map[string]string{"error": "waveform bins must be 64 to 8192"})
			return
		}
	}
	s.mu.Lock()
	if s.takes == nil {
		s.mu.Unlock()
		studioJSON(w, http.StatusNotFound, map[string]string{"error": "no retained takes"})
		return
	}
	take, err := s.takes.Get(r.PathValue("id"))
	s.mu.Unlock()
	if err != nil {
		studioJSON(w, http.StatusNotFound, map[string]string{"error": "take not found"})
		return
	}
	if take.Timeline == nil || !take.Timeline.Valid() || take.Asset.Path == "" || take.Frames == 0 {
		studioJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "choose a published Session take"})
		return
	}
	lo, hi := takeCoverage(take)
	key := fmt.Sprintf("%s/%s/%d/%d/%d", take.ID, take.Asset.SHA256, lo, hi, bins)
	s.vocalWaveforms.mu.Lock()
	defer s.vocalWaveforms.mu.Unlock()
	result, found := s.vocalWaveforms.items[key]
	if !found {
		result, err = analyzeVocalWaveform(r.Context(), takeRoot(s.path), take, bins)
		if err != nil {
			studioJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
			return
		}
		if s.vocalWaveforms.items == nil {
			s.vocalWaveforms.items = map[string]vocalWaveform{}
		}
		if len(s.vocalWaveforms.order) == 16 {
			delete(s.vocalWaveforms.items, s.vocalWaveforms.order[0])
			s.vocalWaveforms.order = s.vocalWaveforms.order[1:]
		}
		s.vocalWaveforms.items[key] = result
		s.vocalWaveforms.order = append(s.vocalWaveforms.order, key)
	}
	studioJSON(w, http.StatusOK, result)
}

func analyzeVocalWaveform(ctx context.Context, dir string, take takejournal.Take, bins int) (vocalWaveform, error) {
	lo, hi := takeCoverage(take)
	result := vocalWaveform{Take: take.ID, Start: lo, End: hi}
	clock, err := takeClock(take)
	if err != nil {
		return result, err
	}
	first := takeOrigin(take) + clock.SampleAtTick(lo) - clock.SampleAtTick(take.Timeline.PlaybackStartTick)
	count := clock.SampleAtTick(hi) - clock.SampleAtTick(lo)
	if hi <= lo || first < 0 || count <= 0 || first+count > int64(take.Frames) {
		return result, fmt.Errorf("take has no covered vocal audio")
	}
	// Each bin covers equal musical time, so a transient stays under its bar
	// when the recording crosses a tempo step. Frame-uniform bins drift here.
	bins = int(min(int64(bins), count, hi-lo))
	ends := make([]int64, bins)
	for i := range ends {
		ends[i] = clock.SampleAtTick(lo+(hi-lo)*int64(i+1)/int64(bins)) - clock.SampleAtTick(lo)
	}
	result.Peaks = make([][2]float32, bins)
	for i := range result.Peaks {
		result.Peaks[i] = [2]float32{math.MaxFloat32, -math.MaxFloat32}
	}
	stream, err := sampleasset.OpenStream(dir, take.Asset)
	if err != nil {
		return result, err
	}
	defer stream.Close()
	var left, right [4096]float32
	bin := 0
	for at := int64(0); at < count; {
		n := min(int64(len(left)), count-at)
		if err = stream.ReadFrames(ctx, first+at, left[:n], right[:n]); err != nil {
			return result, err
		}
		for i := int64(0); i < n; i++ {
			v := left[i]*.5 + right[i]*.5 // same mono signal as the comp print
			for bin+1 < bins && at+i >= ends[bin] {
				bin++
			}
			result.Peaks[bin][0] = min(result.Peaks[bin][0], v)
			result.Peaks[bin][1] = max(result.Peaks[bin][1], v)
			result.Peak = max(result.Peak, math.Abs(float64(v)))
		}
		at += n
	}
	return result, nil
}
