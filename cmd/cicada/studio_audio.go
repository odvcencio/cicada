package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/coder/websocket"
	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/host/liveplay"
	"m31labs.dev/cicada/host/schedule"
	webhost "m31labs.dev/cicada/host/web"
	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/project"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"
)

type studioParamsResponse struct {
	Revision  string                 `json:"revision"`
	Registry  json.RawMessage        `json:"registry"`
	Addresses []project.ParamAddress `json:"addresses"`
}

var studioAudioSessionID atomic.Uint64

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
	Type       string          `json:"type"`
	Address    string          `json:"address"`
	Track      string          `json:"track"`
	Value      json.RawMessage `json:"value"`
	Note       *int            `json:"note,omitempty"`
	Velocity   *int            `json:"velocity,omitempty"`
	On         *bool           `json:"on,omitempty"`
	NoteID     any             `json:"noteId,omitempty"`
	Channel    *int            `json:"channel,omitempty"`
	PitchCents *float32        `json:"pitchCents,omitempty"`
	Pressure   *float32        `json:"pressure,omitempty"`
	Timbre     *float32        `json:"timbre,omitempty"`
	owner      string
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
	session := strconv.FormatUint(studioAudioSessionID.Add(1), 10) + ":"
	held := make(map[string]audioClientMessage)
	defer func() {
		for _, message := range held {
			off := false
			zero := 0
			message.On, message.Velocity = &off, &zero
			_ = s.applyAudioMessage(message)
		}
	}()
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
		if message.Type == "note" && message.Note != nil && message.On != nil {
			id, _, identityErr := audioNoteIdentity(message.NoteID)
			if identityErr != nil {
				s.queueAudioError(ctx, outgoing, audioErrorResponse(message, identityErr))
				continue
			}
			if id == "" {
				id = message.Track + ":" + strconv.Itoa(*message.Note)
			}
			message.owner = session + id
			if original, ok := held[message.owner]; ok {
				if *message.On {
					continue
				} // Idempotent repeated press.
				message.Track, message.Note = original.Track, original.Note
			} else if !*message.On {
				continue
			} // Unknown releases have no owner.
			if *message.On && len(held) >= 128 {
				s.queueAudioError(ctx, outgoing, audioErrorResponse(message, fmt.Errorf("held note capacity exceeded")))
				continue
			}
		}
		if err := s.applyAudioMessage(message); err != nil {
			s.queueAudioError(ctx, outgoing, audioErrorResponse(message, err))
		} else if message.Type == "note" && message.On != nil {
			if *message.On {
				held[message.owner] = message
			} else {
				delete(held, message.owner)
			}
		}
	}
}

func audioErrorResponse(message audioClientMessage, err error) audioErrorMessage {
	address := message.Address
	if address == "" && (message.Type == "mute" || message.Type == "solo") {
		address = message.Track + "." + message.Type
	}
	if address == "" && (message.Type == "note" || message.Type == "note-expression") {
		address = message.Track
	}
	code := "CICADA-PARAM"
	if message.Type == "note" || message.Type == "note-expression" {
		code = "CICADA-NOTE"
	}
	return audioErrorMessage{Type: "error", Code: code, Message: err.Error(), Address: address}
}

func (s *studio) queueAudioError(ctx context.Context, outgoing chan<- any, message audioErrorMessage) {
	select {
	case outgoing <- message:
	case <-ctx.Done():
	default:
	}
}

// audioNoteIdentity accepts legacy owner strings and numeric expressive identities.
func audioNoteIdentity(value any) (string, uint16, error) {
	if value == nil {
		return "", 0, nil
	}
	if owner, ok := value.(string); ok {
		if len(owner) > 512 {
			return "", 0, fmt.Errorf("note ID exceeds limit")
		}
		return owner, 0, nil
	}
	var number float64
	switch id := value.(type) {
	case float64:
		number = id
	case int:
		number = float64(id)
	case *int:
		if id == nil {
			return "", 0, nil
		}
		number = float64(*id)
	case uint16:
		number = float64(id)
	default:
		return "", 0, fmt.Errorf("invalid note identity")
	}
	if number < 1 || number > 65534 || number != float64(uint16(number)) {
		return "", 0, fmt.Errorf("note identity must be from 1 to 65534")
	}
	return fmt.Sprintf("mpe:%d", uint16(number)), uint16(number), nil
}

func validateAudioNote(message audioClientMessage) error {
	if message.Note == nil || message.Velocity == nil || message.On == nil {
		return fmt.Errorf("note message needs note, velocity, and on fields")
	}
	if *message.Note < 0 || *message.Note > 127 {
		return fmt.Errorf("note must be in MIDI range 0–127")
	}
	if *message.Velocity < 0 || *message.Velocity > 127 {
		return fmt.Errorf("velocity must be in MIDI range 0–127")
	}
	return nil
}

func (s *studio) applyAudioMessage(message audioClientMessage) error {
	if message.Type == "loudness-reset" {
		s.transport.resetLoudness()
		return nil
	}
	if message.Type == "note" || message.Type == "note-expression" {
		owner, noteID, err := audioNoteIdentity(message.NoteID)
		if err != nil {
			return err
		}
		if message.owner != "" {
			owner = message.owner
		}
		if message.Channel != nil && (*message.Channel < 0 || *message.Channel > 15) {
			return fmt.Errorf("MIDI channel must be from 0 to 15")
		}
		if message.Type == "note-expression" {
			if noteID == 0 || message.PitchCents == nil || message.Pressure == nil || message.Timbre == nil {
				return fmt.Errorf("expression needs noteId, pitchCents, pressure, and timbre fields")
			}
			s.transport.mu.Lock()
			stream := s.transport.stream
			s.transport.mu.Unlock()
			if stream == nil {
				return fmt.Errorf("audio transport is not running")
			}
			return stream.NoteExpression(message.Track, noteID, *message.PitchCents, *message.Pressure, *message.Timbre)
		}
		if err := validateAudioNote(message); err != nil {
			return err
		}
		s.transport.mu.Lock()
		stream := s.transport.stream
		s.transport.mu.Unlock()
		if stream == nil {
			return fmt.Errorf("audio transport is not running")
		}
		if owner != "" {
			return stream.NoteOwnerID(message.Track, *message.Note, *message.Velocity, *message.On, owner, noteID)
		}
		return stream.NoteWithID(message.Track, *message.Note, *message.Velocity, *message.On, noteID)
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
		if message.On == nil {
			return fmt.Errorf("%s message needs an on field", message.Type)
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
		return stream.SetMute(track, *message.On)
	}
	if message.Type == "solo" {
		return stream.SetSolo(track, *message.On)
	}
	return stream.SetParam(track, id, value)
}

func (s *studio) resetLiveLoudness(w http.ResponseWriter, r *http.Request) {
	s.transport.resetLoudness()
	studioJSON(w, http.StatusOK, map[string]any{"ok": true})
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

func (s *studio) kernelImage(w http.ResponseWriter, r *http.Request) {
	rate, err := strconv.Atoi(r.URL.Query().Get("rate"))
	if err != nil || rate != 44_100 && rate != 48_000 && rate != 96_000 {
		studioJSON(w, http.StatusBadRequest, map[string]string{"error": "rate must be 44100, 48000, or 96000"})
		return
	}
	s.mu.Lock()
	source, err := os.ReadFile(s.path)
	if err != nil {
		s.mu.Unlock()
		studioJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	revision := studioRevision(source)
	p, err := compileStudioSource(s.path, source)
	s.mu.Unlock()
	if err != nil {
		studioJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	if r.URL.Query().Get("capture") == "1" {
		p = captureBacking(p)
	}
	root, err := studioProjectRoot(s.path)
	if err != nil {
		studioJSON(w, 422, map[string]any{"error": err.Error()})
		return
	}
	cfg, err := schedule.Compile(p, root, rate, 128)
	if err != nil {
		studioJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	image, err := kernelimage.Encode(cfg)
	if err != nil {
		studioJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Cicada-Revision", revision)
	w.Header().Set("ETag", `"`+revision+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(image)
}

func (s *studio) kernelWASM(w http.ResponseWriter, _ *http.Request) {
	path, err := locateKernelWASM()
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/wasm")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

func locateKernelWASM() (string, error) {
	if explicit := os.Getenv("CICADA_KERNEL_WASM"); explicit != "" {
		if info, err := os.Stat(explicit); err == nil && info.Mode().IsRegular() {
			return explicit, nil
		}
		return "", fmt.Errorf("CICADA_KERNEL_WASM does not name a kernel module")
	}
	if executable, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(executable), "cicada-kernel.wasm")
		if info, statErr := os.Stat(candidate); statErr == nil && info.Mode().IsRegular() {
			return candidate, nil
		}
	}
	working, err := os.Getwd()
	if err == nil {
		for directory := working; ; directory = filepath.Dir(directory) {
			candidate := filepath.Join(directory, "build", "cicada-kernel.wasm")
			if info, statErr := os.Stat(candidate); statErr == nil && info.Mode().IsRegular() {
				return candidate, nil
			}
			parent := filepath.Dir(directory)
			if parent == directory {
				break
			}
		}
	}
	return "", fmt.Errorf("kernel WASM is missing; run make build-kernel-wasm first")
}

func (s *studio) processorAsset(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(webhost.Processor())
}

func (s *studio) clientAsset(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(webhost.Client())
}
