package main

import (
	"fmt"
	"io"
	"sync/atomic"

	"m31labs.dev/cicada/host/liveplay"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/fx/ampcab"
	"m31labs.dev/cicada/project"
)

type studioAmpFollowerSource interface {
	io.Reader
	MonitorAmpTargets(liveplay.PreviewTarget) (engine.AmpTargets, bool)
}

type studioAmpFollower struct {
	player  studioAmpFollowerSource
	target  liveplay.PreviewTarget
	initial ampcab.Params
	broken  atomic.Bool
}

type studioAmpFollowDevice interface {
	SetAmpFollower(*studioAmpFollower) error
}

// initialAmpTargets seeds the monitor during count-in. Playback takes over
// after its first Read and follows all track automation at device-period rate.
func initialAmpTargets(p *project.Project, trackID string, tick int64) (ampcab.Params, error) {
	var effect *project.Effect
	for _, track := range p.Tracks {
		if track.ID != trackID {
			continue
		}
		inserts := track.Mixer.Inserts
		if len(inserts) == 0 && track.Mixer.Insert != "" && track.Mixer.Insert != "none" {
			inserts = []string{track.Mixer.Insert}
		}
		for _, name := range inserts {
			for i := range p.Effects {
				fx := &p.Effects[i]
				if fx.ID == name && fx.Kind == "ampcab" {
					if effect != nil {
						return ampcab.Params{}, fmt.Errorf("recording track needs exactly one amp insert")
					}
					effect = fx
				}
			}
		}
	}
	if effect == nil {
		return ampcab.Params{}, fmt.Errorf("recording track needs exactly one amp insert")
	}
	params, err := project.AmpCabParamsFromValues(effect.Params)
	if err != nil {
		return params, err
	}
	for _, lane := range p.Automation {
		if n, ok := project.AutomationValue(p, lane, tick); ok {
			switch lane.Path {
			case trackID + ".amp_drive":
				params.Drive = n
			case trackID + ".amp_tone":
				params.Tone = n
			case trackID + ".amp_output":
				params.Output = n
			}
		}
	}
	return params, params.Validate()
}

func (s *studio) releaseAutomationMonitor(recording *studioAutomationRecording) {
	if recording == nil || recording.MonitorFollower == nil {
		return
	}
	t := s.transport
	t.mu.Lock()
	defer t.mu.Unlock()
	if device, ok := t.audio.(studioAmpFollowDevice); ok {
		_ = device.SetAmpFollower(nil)
	}
	t.monitorWrite = false
	recording.MonitorFollower = nil
}
