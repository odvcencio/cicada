package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/host/liveplay"
	"m31labs.dev/cicada/project"
)

type studioAutomationPreview struct {
	Player  *liveplay.Player
	Version uint64
}

func releaseAutomationPreview(p studioAutomationPreview) {
	if p.Player != nil && p.Version != 0 {
		go p.Player.CancelPreviewWait(p.Version)
	}
}

type recordedAutomationPoint struct {
	Tick  int64   `json:"tick"`
	Value float64 `json:"value"`
}
type studioAutomationRecording struct {
	Preview        studioAutomationPreview   `json:"-"`
	ID             string                    `json:"id"`
	Path           string                    `json:"path"`
	Revision       string                    `json:"revision"`
	SourceRevision string                    `json:"sourceRevision"`
	Unit           string                    `json:"unit"`
	Active         bool                      `json:"active"`
	Saving         bool                      `json:"-"`
	End            int64                     `json:"end"`
	Epoch          uint64                    `json:"epoch"`
	Points         []recordedAutomationPoint `json:"points"`
	Project        *project.Project          `json:"-"`
	Written        time.Time                 `json:"-"`
}

func (s *studio) automationDraftPath() string {
	return filepath.Join(takeRoot(s.path), ".cicada", "automation-take.json")
}

func (s *studio) removeAutomationDraft() error {
	root, err := os.OpenRoot(takeRoot(s.path))
	if err != nil {
		return err
	}
	defer root.Close()
	err = root.Remove(".cicada/automation-take.json")
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (s *studio) persistAutomationRecording(force bool) error {
	r := s.automationRecording
	if r == nil || !force && time.Since(r.Written) < 250*time.Millisecond {
		return nil
	}
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(takeRoot(s.path))
	if err != nil {
		return err
	}
	defer root.Close()
	if err = root.MkdirAll(".cicada", 0700); err != nil {
		return err
	}
	temporary := fmt.Sprintf(".cicada/.automation-%s-%d.tmp", r.ID, time.Now().UnixNano())
	f, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(temporary)
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = root.Rename(temporary, ".cicada/automation-take.json"); err != nil {
		return err
	}
	r.Written = time.Now()
	return nil
}

func (s *studio) restoreAutomationRecording() {
	if s.automationRecording != nil {
		return
	}
	root, err := os.OpenRoot(takeRoot(s.path))
	if err != nil {
		return
	}
	defer root.Close()
	f, err := root.Open(".cicada/automation-take.json")
	if err != nil {
		return
	}
	defer f.Close()
	var draft studioAutomationRecording
	decoder := json.NewDecoder(io.LimitReader(f, 256<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&draft) != nil || len(draft.ID) != 32 || len(draft.Path) > 512 || len(draft.Points) > 1024 || len(draft.Points) == 0 {
		return
	}
	previous := int64(-1)
	for _, p := range draft.Points {
		if p.Tick < 0 || p.Tick <= previous || p.Tick > draft.End || p.Tick%240 != 0 || math.IsNaN(p.Value) || math.IsInf(p.Value, 0) {
			return
		}
		previous = p.Tick
	}
	draft.Active = false
	s.automationRecording = &draft
}

func (s *studio) automationRecordingState(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.restoreAutomationRecording()
	if recording := s.automationRecording; recording != nil && recording.Active && !recording.Saving {
		state := s.transport.snapshot()
		if !state.Playing || recording.Epoch != 0 && state.MIDIClockEpoch != recording.Epoch || automationTick(state, recording.End) < recording.Points[len(recording.Points)-1].Tick {
			recording.Active = false
			if err := s.persistAutomationRecording(true); err != nil {
				studioJSON(w, 500, map[string]any{"error": err.Error()})
				return
			}
		}
	}
	studioJSON(w, 200, map[string]any{"recording": s.automationRecording})
}

func automationTick(state transportSnapshot, end int64) int64 {
	return min(end, max(int64(0), (state.Bar-1)*3840+(state.Step-1)*240))
}

func (s *studio) automationRecordingCommand(w http.ResponseWriter, r *http.Request) {
	request, ok := studioRequest(w, r)
	if !ok {
		return
	}
	s.mu.Lock()
	s.restoreAutomationRecording()
	fail := func(status int, err error) {
		s.mu.Unlock()
		studioJSON(w, status, map[string]any{"error": err.Error(), "retained": true})
	}
	var value struct {
		Number  float64 `json:"number"`
		Replace bool    `json:"replace"`
	}
	if len(request.Value) > 0 && json.Unmarshal(request.Value, &value) != nil {
		fail(400, fmt.Errorf("choose an automation value"))
		return
	}
	if request.Action == "start" {
		if s.automationRecording != nil {
			fail(409, fmt.Errorf("keep or discard the retained automation take first"))
			return
		}
		if s.captureID != "" {
			fail(409, fmt.Errorf("finish the audio take before recording mixer automation"))
			return
		}
		source, err := os.ReadFile(s.path)
		if err != nil || studioRevision(source) != request.Revision || request.SourceRevision == "" {
			fail(409, fmt.Errorf("refresh Studio before recording automation"))
			return
		}
		if err = checkStudioSourceRevision(s.path, request.SourceRevision); err != nil {
			fail(409, err)
			return
		}
		p, err := compileStudioSource(s.path, source)
		if err != nil {
			fail(422, err)
			return
		}
		if p.Edition < 2 {
			fail(422, fmt.Errorf("recorded automation requires a Cicada 2 project"))
			return
		}
		resolved, err := project.ResolveParameterPath(p, request.Path)
		if err != nil || !resolved.Descriptor.Live || !resolved.Descriptor.Automatable || resolved.Descriptor.Curve == "toggle" || resolved.Descriptor.Unit == "enum" {
			fail(422, fmt.Errorf("choose a live numeric automation parameter"))
			return
		}
		state := s.transport.snapshot()
		if !state.Playing || state.BrowserPlaying {
			fail(409, fmt.Errorf("play the song with the native engine before recording movements"))
			return
		}
		initial := 0.
		for _, a := range project.ParamAddresses(p) {
			if a.Address == request.Path {
				if n, ok := a.Value.(float64); ok {
					initial = n
				}
			}
		}
		end := studioArrangementEnd(p)
		tick := automationTick(state, end)
		for _, lane := range p.Automation {
			if lane.Path == request.Path {
				if !value.Replace {
					fail(409, fmt.Errorf("this parameter has a curve; enable Replace saved curve to record a replacement"))
					return
				}
				if v, ok := project.AutomationValue(p, lane, tick); ok {
					initial = v
				}
			}
		}
		var id [16]byte
		if _, err = rand.Read(id[:]); err != nil {
			fail(500, err)
			return
		}
		s.automationRecording = &studioAutomationRecording{ID: hex.EncodeToString(id[:]), Path: request.Path, Revision: request.Revision, SourceRevision: request.SourceRevision, Unit: resolved.Descriptor.Unit, Active: true, End: end, Epoch: state.MIDIClockEpoch, Points: []recordedAutomationPoint{{tick, initial}}, Project: p}
		if err = s.persistAutomationRecording(true); err != nil {
			s.automationRecording = nil
			fail(500, err)
			return
		}
		s.mu.Unlock()
		studioJSON(w, 200, map[string]any{"ok": true})
		return
	}
	recording := s.automationRecording
	if recording == nil || request.Name != recording.ID || recording.Saving {
		fail(409, fmt.Errorf("automation take changed; refresh the recording controls"))
		return
	}
	switch request.Action {
	case "value":
		state := s.transport.snapshot()
		if recording.Epoch == 0 {
			recording.Epoch = state.MIDIClockEpoch
		}
		if !recording.Active || !state.Playing || state.MIDIClockEpoch != recording.Epoch {
			recording.Active = false
			_ = s.persistAutomationRecording(true)
			fail(409, fmt.Errorf("playback changed; keep or discard this automation pass"))
			return
		}
		tick := automationTick(state, recording.End)
		if tick < recording.Points[len(recording.Points)-1].Tick {
			recording.Active = false
			_ = s.persistAutomationRecording(true)
			fail(409, fmt.Errorf("loop or seek ended this pass; keep or discard its movements"))
			return
		}
		if err := s.applyProjectControlPreview(recording.Project, audioClientMessage{Type: "param", Address: recording.Path, Value: json.RawMessage(strconv.FormatFloat(value.Number, 'g', -1, 64))}, &recording.Preview); err != nil {
			fail(422, err)
			return
		}
		point := recordedAutomationPoint{tick, value.Number}
		if tick == recording.Points[len(recording.Points)-1].Tick {
			recording.Points[len(recording.Points)-1] = point
		} else {
			if len(recording.Points) == 1024 {
				recording.Active = false
				_ = s.persistAutomationRecording(true)
				fail(422, fmt.Errorf("automation take is full; keep it before recording another pass"))
				return
			}
			recording.Points = append(recording.Points, point)
			// Remove exact constant/straight runs without changing their curve.
			if n := len(recording.Points); n >= 3 {
				a, b, c := recording.Points[n-3], recording.Points[n-2], recording.Points[n-1]
				slope := a.Value + (c.Value-a.Value)*float64(b.Tick-a.Tick)/float64(c.Tick-a.Tick)
				if math.Abs(b.Value-slope) < 1e-9 {
					recording.Points[n-2] = c
					recording.Points = recording.Points[:n-1]
				}
			}
		}
		if err := s.persistAutomationRecording(false); err != nil {
			fail(500, err)
			return
		}
		s.mu.Unlock()
		studioJSON(w, 200, map[string]any{"ok": true})
		return
	case "discard":
		if err := s.removeAutomationDraft(); err != nil && !os.IsNotExist(err) {
			fail(500, err)
			return
		}
		releaseAutomationPreview(recording.Preview)
		s.automationRecording = nil
		s.mu.Unlock()
		studioJSON(w, 200, map[string]any{"ok": true})
		return
	case "keep":
		recording.Active = false
		if err := s.persistAutomationRecording(true); err != nil {
			fail(500, err)
			return
		}
		if recording.Project == nil {
			source, err := os.ReadFile(s.path)
			if err != nil {
				fail(422, err)
				return
			}
			recording.Project, err = compileStudioSource(s.path, source)
			if err != nil {
				fail(422, err)
				return
			}
		}
		var intents []edits.Intent
		for _, lane := range recording.Project.Automation {
			if lane.Path == recording.Path {
				for _, point := range lane.Points {
					intents = append(intents, &edits.RemoveAutomationPoint{Entity: edits.EntityID("param:" + recording.Path), At: listeningPosition(point.Tick)})
				}
			}
		}
		for _, point := range recording.Points {
			intents = append(intents, &edits.SetAutomationPoint{Entity: edits.EntityID("param:" + recording.Path), At: listeningPosition(point.Tick), Value: strconv.FormatFloat(point.Value, 'g', -1, 64) + automationRecordUnit(recording.Unit), Shape: "linear"})
		}
		recording.Saving = true
		request.Revision, request.SourceRevision, request.Label = recording.Revision, recording.SourceRevision, "Record automation · "+recording.Path
		s.mu.Unlock()
		receipt := &studioRecordingResponse{ResponseWriter: w}
		s.applyPreparedIntents(receipt, request, edits.Envelope{Intents: intents}, edits.ParamWriterAuto, nil, nil, nil)
		s.mu.Lock()
		defer s.mu.Unlock()
		recording.Saving = false
		if receipt.status == 200 && s.automationRecording == recording {
			releaseAutomationPreview(recording.Preview)
			s.automationRecording = nil
			_ = s.removeAutomationDraft()
		}
		return
	default:
		fail(400, fmt.Errorf("choose start, value, keep or discard"))
		return
	}
}

func automationRecordUnit(unit string) string {
	switch unit {
	case "db", "dB":
		return "dB"
	case "hz", "Hz":
		return "Hz"
	case "ms":
		return "ms"
	}
	return ""
}
