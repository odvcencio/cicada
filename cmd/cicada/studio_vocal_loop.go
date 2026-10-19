package main

import (
	"fmt"
	"sync"

	"m31labs.dev/cicada/host/capture"
	"m31labs.dev/cicada/host/takejournal"
)

// One continuous audio owner retains the initial count-in and splits passes on
// the storage writer. No device reopening, count-in restart or control polling
// occurs between passes. This mutex belongs only to the writer/control sides.
type vocalLoopCapture struct {
	mu         sync.Mutex
	store      *takejournal.Store
	template   takejournal.Take
	passes     int
	duration   int64
	ids        []string
	index      int
	rawBase    uint64
	originSet  bool
	mappedBias int64
	complete   bool
	fault      error
}

func newVocalLoopCapture(store *takejournal.Store, take takejournal.Take, passes int) *vocalLoopCapture {
	clock := takeClock(take)
	return &vocalLoopCapture{store: store, template: take, passes: passes,
		duration: clock.SampleAtTick(take.Timeline.EndTick) - clock.SampleAtTick(take.Timeline.StartTick), ids: []string{take.ID}}
}

func (l *vocalLoopCapture) snapshot() any {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return struct {
		Passes  int      `json:"passes"`
		Pass    int      `json:"pass"`
		TakeIDs []string `json:"takeIds"`
	}{l.passes, l.index + 1, append([]string(nil), l.ids...)}
}

func (l *vocalLoopCapture) publishCurrent(incomplete bool) error {
	if l.complete {
		return nil
	}
	id := l.ids[l.index]
	if err := l.store.Finalize(id, incomplete); err != nil {
		return err
	}
	if err := l.store.Publish(id); err != nil {
		return err
	}
	l.complete = true
	return nil
}

func (l *vocalLoopCapture) write(record capture.RecordedBlock, pcm [][]float32) (err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.fault != nil {
		return l.fault
	}
	defer func() {
		if err != nil {
			l.fault = err
		}
	}()
	if !l.originSet {
		// The same first-block mapping used by comp printing anchors every
		// source frame. Small later timestamp jitter cannot drop or duplicate
		// accepted input at a pass boundary.
		l.mappedBias = record.Placement.EngineFrame - int64(record.RawFrame)
		l.originSet = true
	}
	// A gap that crosses a pass boundary cannot be assigned silently to either
	// performance. Retain the durable prefix and stop rather than invent a pass.
	if record.Timing.GapFrames > 0 || record.Timing.Flags != 0 {
		// Retain the accepted input, including its declared hole, before stopping
		// the repetition. It remains auditionable even when timing is unreliable.
		if l.complete && l.index+1 < l.passes {
			id, e := l.store.BeginTimeline(l.template.Track, l.template.Scene, l.template.Expected, l.template.Rate, l.template.Channels, l.template.Timeline)
			if e != nil {
				return e
			}
			l.ids = append(l.ids, id)
			l.index++
			l.rawBase = record.RawFrame - record.Timing.GapFrames
			l.complete = false
		}
		if !l.complete {
			block := record
			block.RawFrame -= l.rawBase
			block.Placement.EngineFrame -= int64(l.index) * l.duration
			if e := l.store.Write(l.ids[l.index], block, pcm); e != nil {
				return e
			}
		}
		return fmt.Errorf("loop recording input was interrupted; completed passes are retained")
	}
	for at := 0; at < record.Timing.Frames; {
		mapped := int64(record.RawFrame) + int64(at) + l.mappedBias
		if l.complete {
			if l.index+1 >= l.passes {
				return nil
			}
			if mapped != int64(l.index+1)*l.duration {
				return fmt.Errorf("loop recording timing changed between passes")
			}
			id, err := l.store.BeginTimeline(l.template.Track, l.template.Scene, l.template.Expected, l.template.Rate, l.template.Channels, l.template.Timeline)
			if err != nil {
				return err
			}
			l.ids = append(l.ids, id)
			l.index++
			l.rawBase = record.RawFrame + uint64(at)
			l.complete = false
		}
		end := int64(l.index+1) * l.duration
		n := min(record.Timing.Frames-at, int(end-mapped))
		if n <= 0 {
			return fmt.Errorf("loop recording exceeded its mapped pass boundary")
		}
		block := record
		block.RawFrame = record.RawFrame + uint64(at) - l.rawBase
		block.Timing.Frames = n
		block.Timing.EngineFrame += int64(at) - int64(l.index)*l.duration
		block.Timing.DeviceFrame += uint64(at)
		advanceNano := int64(at) * 1_000_000_000 / int64(l.template.Rate)
		if block.Timing.InputTime.Valid {
			block.Timing.InputTime.Nano += advanceNano
		}
		if block.Timing.OutputTime.Valid && (block.Timing.OutputTime.Reference == capture.ReferenceFirstFrame || block.Timing.OutputTime.Reference == capture.ReferenceEstimatedFirstFrame) {
			block.Timing.OutputTime.Nano += advanceNano
		}
		block.Placement.RawEngineFrame += int64(at) - int64(l.index)*l.duration
		block.Placement.MappedEngineFrame += int64(at) - int64(l.index)*l.duration
		block.Placement.EngineFrame = mapped - int64(l.index)*l.duration
		var channels [2][]float32
		for ch := range pcm {
			channels[ch] = pcm[ch][at : at+n]
		}
		if err = l.store.Write(l.ids[l.index], block, channels[:len(pcm)]); err != nil {
			return err
		}
		at += n
		if mapped+int64(n) == end {
			if err = l.publishCurrent(false); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *studio) finishVocalLoop(l *vocalLoopCapture, snapshot capture.Snapshot, drainErr error) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.complete {
		if snapshot.TrailingGap != nil {
			block := *snapshot.TrailingGap
			block.RawFrame -= l.rawBase
			block.Placement.EngineFrame -= int64(l.index) * l.duration
			if err := l.store.Write(l.ids[l.index], block, nil); err != nil {
				return err
			}
		}
		if err := l.publishCurrent(snapshot.Incomplete || drainErr != nil || l.fault != nil); err != nil {
			return err
		}
	}
	if snapshot.Incomplete || drainErr != nil || l.fault != nil {
		return fmt.Errorf("loop passes retained with input gaps; audition them before choosing a vocal")
	}
	id := l.ids[l.index]
	take, err := l.store.Get(id)
	if err != nil {
		return err
	}
	start, end := takeCoverage(take)
	if end <= start {
		return fmt.Errorf("loop pass retained without a usable vocal phrase")
	}
	return s.commitTake(id, take.Expected, nil)
}
