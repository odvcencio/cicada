package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"time"

	"github.com/coder/websocket"
	"m31labs.dev/cicada/host/liveplay"
	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/project"
)

type studioParamsResponse struct {
	Revision  string                 `json:"revision"`
	Registry  json.RawMessage        `json:"registry"`
	Addresses []project.ParamAddress `json:"addresses"`
}

func (s *studio) params(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	source, err := os.ReadFile(s.path)
	if err == nil {
		if compiled, compileErr := compileStudioSource(s.path, source); compileErr == nil {
			s.lastGoodSource, s.lastGoodProject = source, compiled
		}
	}
	goodSource, compiled := append([]byte(nil), s.lastGoodSource...), s.lastGoodProject
	s.mu.Unlock()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	addresses := []project.ParamAddress{}
	if compiled != nil {
		addresses = project.ParamAddresses(compiled)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(studioParamsResponse{
		Revision: studioRevision(goodSource), Registry: json.RawMessage(project.ParamsJSON()), Addresses: addresses,
	})
}

func (s *studio) liveProject() (*project.Project, []byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	source, err := os.ReadFile(s.path)
	if err != nil {
		return nil, nil, err
	}
	if compiled, compileErr := compileStudioSource(s.path, source); compileErr == nil {
		s.lastGoodSource, s.lastGoodProject = source, compiled
	}
	return s.lastGoodProject, append([]byte(nil), s.lastGoodSource...), nil
}

type audioClientMessage struct {
	Type    string          `json:"type"`
	Address string          `json:"address"`
	Track   string          `json:"track"`
	Value   json.RawMessage `json:"value"`
	On      bool            `json:"on"`
}

type audioErrorMessage struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Address string `json:"address"`
}

func (s *studio) audioSocket(w http.ResponseWriter, r *http.Request) {
	if !studioSameOrigin(r) {
		http.Error(w, "cross-origin audio control is not allowed", http.StatusForbidden)
		return
	}
	connection, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer connection.CloseNow()
	connection.SetReadLimit(16 * 1024)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	outgoing := make(chan any, 8)
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		ticker := time.NewTicker(time.Second / 60)
		defer ticker.Stop()
		var sent uint64
		var sentLoudness uint64
		hasSentLoudness := false
		for {
			select {
			case <-ctx.Done():
				return
			case value := <-outgoing:
				writeCtx, done := context.WithTimeout(ctx, time.Second)
				writeErr := connection.Write(writeCtx, websocket.MessageText, mustJSON(value))
				done()
				if writeErr != nil {
					cancel()
					return
				}
			case <-ticker.C:
				latest := s.transport.latestMeter.Load()
				if latest == nil || latest.sequence == sent {
					continue
				}
				writeCtx, done := context.WithTimeout(ctx, time.Second)
				includeLoudness := latest.loudness.Sequence > 0 && (!hasSentLoudness || latest.loudness.Sequence != sentLoudness)
				var message map[string]any
				if includeLoudness {
					message = studioMeters(latest.frame, latest.loudness)
					sentLoudness, hasSentLoudness = latest.loudness.Sequence, true
				} else {
					message = studioMeters(latest.frame)
				}
				writeErr := connection.Write(writeCtx, websocket.MessageText, mustJSON(message))
				done()
				if writeErr != nil {
					cancel()
					return
				}
				sent = latest.sequence
			}
		}
	}()
	for {
		_, data, readErr := connection.Read(ctx)
		if readErr != nil {
			cancel()
			<-writerDone
			return
		}
		var message audioClientMessage
		if decodeErr := json.Unmarshal(data, &message); decodeErr != nil {
			s.queueAudioError(ctx, outgoing, audioErrorMessage{Type: "error", Code: "CICADA-PARAM", Message: "invalid audio message", Address: ""})
			continue
		}
		if err := s.applyAudioMessage(message); err != nil {
			address := message.Address
			if address == "" && (message.Type == "mute" || message.Type == "solo") {
				address = message.Track + "." + message.Type
			}
			s.queueAudioError(ctx, outgoing, audioErrorMessage{Type: "error", Code: "CICADA-PARAM", Message: err.Error(), Address: address})
		}
	}
}

func (s *studio) queueAudioError(ctx context.Context, outgoing chan<- any, message audioErrorMessage) {
	select {
	case outgoing <- message:
	case <-ctx.Done():
	default:
	}
}

func (s *studio) applyAudioMessage(message audioClientMessage) error {
	if message.Type == "loudness-reset" {
		s.transport.resetLoudness()
		return nil
	}
	p, _, err := s.liveProject()
	if err != nil {
		return err
	}
	var id kernel.ParamID
	var track uint8
	trackName := ""
	var value float32
	switch message.Type {
	case "param":
		if message.Address == "" || p == nil {
			return fmt.Errorf("unknown parameter address")
		}
		address, lookupErr := project.ParamAddressByName(p, message.Address)
		if lookupErr != nil {
			return fmt.Errorf("unknown parameter address")
		}
		descriptor, ok := project.LookupParamDescriptor(address.Param)
		if !ok || !descriptor.Live {
			return fmt.Errorf("parameter is not live")
		}
		found := false
		for index := range kernel.Params {
			if kernel.Params[index].Name == address.Param {
				id, found = kernel.ParamID(index), true
				break
			}
		}
		if !found {
			return fmt.Errorf("parameter is not live")
		}
		if descriptor.Scope == "global" {
			track = 0xff
		} else {
			trackName = address.Track
		}
		if string(message.Value) == "null" {
			if !descriptor.Off {
				return fmt.Errorf("parameter does not accept off")
			}
			value = float32(math.Inf(-1))
		} else {
			var number float64
			if len(message.Value) == 0 || json.Unmarshal(message.Value, &number) != nil || math.IsNaN(number) || math.IsInf(number, 0) {
				return fmt.Errorf("parameter value must be a finite number")
			}
			value = float32(number)
			if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) || value < kernel.Params[id].Min || value > kernel.Params[id].Max {
				return fmt.Errorf("parameter value is out of range")
			}
		}
		if kernel.Params[id].Curve == "toggle" && value != 0 && value != 1 {
			return fmt.Errorf("parameter value must be zero or one")
		}
	case "mute", "solo":
		if message.Track == "" {
			return fmt.Errorf("unknown track")
		}
		trackName = message.Track
		id = kernel.ParamMixMute
		if message.Type == "solo" {
			id = kernel.ParamMixSolo
		}
	default:
		return fmt.Errorf("type must be param, mute, solo, or loudness-reset")
	}
	s.transport.mu.Lock()
	stream := s.transport.stream
	s.transport.mu.Unlock()
	if stream == nil {
		return fmt.Errorf("audio transport is not running")
	}
	if trackName != "" {
		var ok bool
		track, ok = stream.TrackIndex(trackName)
		if !ok {
			return fmt.Errorf("track is not in the playing score")
		}
	}
	if message.Type == "mute" {
		return stream.SetMute(track, message.On)
	}
	if message.Type == "solo" {
		return stream.SetSolo(track, message.On)
	}
	return stream.SetParam(track, id, value)
}

func mustJSON(value any) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		return []byte(`{"type":"error","code":"CICADA-PARAM","message":"encoding failed","address":""}`)
	}
	return encoded
}

func dbfs(value float32) float64 {
	if value <= 0 || math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
		return -120
	}
	db := 20 * math.Log10(float64(value))
	return math.Max(-120, math.Min(24, db))
}

func studioMeters(frame liveplay.MeterFrame, loudnessSnapshots ...liveplay.LoudnessSnapshot) map[string]any {
	tracks := make(map[string]any, frame.TrackCount)
	for index := 0; index < int(frame.TrackCount); index++ {
		tracks[frame.TrackIDs[index]] = dbPair(frame.Tracks[index])
	}
	returns := make(map[string]any, 2)
	if frame.HasReturnA {
		returns["a"] = dbPair(frame.ReturnA)
	}
	if frame.HasReturnB {
		returns["b"] = dbPair(frame.ReturnB)
	}
	buses := map[string]any{"music": dbPair(frame.Music)}
	if frame.HasSFX {
		buses["sfx"] = dbPair(frame.SFX)
	}
	master := map[string]any{
		"pre_peak": dbfs(frame.MasterPre.Peak), "peak": dbfs(frame.MasterPeak), "rms": dbfs(frame.MasterRMS),
		"comp_gr": math.Max(0, float64(frame.CompGR)), "limiter_gr": math.Max(0, float64(frame.LimiterGR)),
		"over": dbfs(frame.MasterPre.Peak) > -0.3,
	}
	result := map[string]any{
		"type": "meters", "tick": frame.Tick, "tracks": tracks, "returns": returns, "buses": buses, "master": master,
	}
	if len(loudnessSnapshots) != 0 {
		loudness := loudnessSnapshots[0]
		result["loudness"] = map[string]any{
			"momentary":      nullableLoudness(loudness.MomentaryLUFS, loudness.HasMomentary),
			"short_term":     nullableLoudness(loudness.ShortTermLUFS, loudness.HasShortTerm),
			"integrated":     nullableLoudness(loudness.IntegratedLUFS, loudness.HasIntegrated),
			"range":          nullableLoudness(loudness.RangeLU, loudness.HasRange),
			"true_peak":      nullableLoudness(loudness.TruePeakDBTP, loudness.HasTruePeak),
			"sample_peak":    nullableLoudness(loudness.SamplePeakDBFS, loudness.HasSamplePeak),
			"dropped_blocks": loudness.DroppedBlocks,
		}
	}
	return result
}

func nullableLoudness(value float64, valid bool) any {
	if !valid || math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}
	return value
}

func dbPair(value liveplay.MeterValue) map[string]float64 {
	return map[string]float64{"peak": dbfs(value.Peak), "rms": dbfs(value.RMS)}
}

func (s *studio) audioScript(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(studioAudioScript)
}
