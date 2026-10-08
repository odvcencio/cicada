package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/taproot/walk"
	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/internal/paramdefs"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

type mixerLineRange = edits.LineRange

type mixerField struct {
	Path        string               `json:"path"`
	Address     string               `json:"address,omitempty"`
	Value       any                  `json:"value"`
	SourceValue string               `json:"sourceValue,omitempty"`
	Descriptor  paramdefs.Descriptor `json:"descriptor"`
	Choices     []string             `json:"choices,omitempty"`
	SourceRange mixerLineRange       `json:"sourceRange"`
	Supported   bool                 `json:"supported"`
	Reason      string               `json:"reason,omitempty"`
}

type mixerSendView struct {
	Path        string               `json:"path"`
	Address     string               `json:"address,omitempty"`
	To          string               `json:"to"`
	Kind        string               `json:"kind"`
	Level       string               `json:"level"`
	Value       any                  `json:"value"`
	Pre         bool                 `json:"pre"`
	Descriptor  paramdefs.Descriptor `json:"descriptor"`
	SourceRange mixerLineRange       `json:"sourceRange"`
	Supported   bool                 `json:"supported"`
	Reason      string               `json:"reason,omitempty"`
}

type mixerStripView struct {
	ID          string                `json:"id"`
	Name        string                `json:"name"`
	Kind        string                `json:"kind"`
	SourceKind  string                `json:"sourceKind,omitempty"`
	Meter       string                `json:"meter"`
	SourceRange mixerLineRange        `json:"sourceRange"`
	Fields      map[string]mixerField `json:"fields"`
	Sends       []mixerSendView       `json:"sends,omitempty"`
	Inserts     []string              `json:"inserts"`
	InsertPath  string                `json:"insertPath"`
	InsertAddr  string                `json:"insertAddress,omitempty"`
	InsertOK    bool                  `json:"insertSupported"`
	InsertWhy   string                `json:"insertReason,omitempty"`
	RouteReason string                `json:"routeReason,omitempty"`
}

type mixerReturnView struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Kind        string         `json:"kind"`
	Meter       string         `json:"meter"`
	SourceRange mixerLineRange `json:"sourceRange"`
	Fields      []mixerField   `json:"fields"`
}

type studioMixerView struct {
	Revision  string                 `json:"revision"`
	Edition   int                    `json:"edition"`
	Tempo     float64                `json:"tempo"`
	Tracks    []mixerStripView       `json:"tracks"`
	Buses     []mixerStripView       `json:"buses"`
	Returns   []mixerReturnView      `json:"returns"`
	Master    mixerStripView         `json:"master"`
	Effects   []mixerReturnView      `json:"effects"`
	Registry  json.RawMessage        `json:"registry"`
	Addresses []project.ParamAddress `json:"addresses"`
}

func (s *studio) mixerState(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := studioRecoveryConflict(s.path); err != nil {
		studioJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
		return
	}
	source, err := os.ReadFile(s.path)
	if err != nil {
		studioJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	view, err := buildStudioMixerView(s.path, source)
	if err != nil {
		studioJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error()})
		return
	}
	if compiled, compileErr := compileStudioSource(s.path, source); compileErr == nil {
		s.lastGoodSource, s.lastGoodProject = bytes.Clone(source), compiled
	}
	studioJSON(w, http.StatusOK, view)
}

func (s *studio) editMixer(w http.ResponseWriter, r *http.Request) {
	edit, ok := studioRequest(w, r)
	if !ok {
		return
	}
	if edit.Path == "" || len(edit.Value) == 0 {
		studioJSON(w, http.StatusBadRequest, map[string]any{"error": "mixer path and value are required"})
		return
	}

	editionNumber, _, err := scoreEdition(s.path)
	if err != nil {
		studioJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error()})
		return
	}
	sourceEdition, err := mixerSourceEdition(s.path)
	if err != nil {
		studioJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error()})
		return
	}
	if sourceEdition == 1 && !edit.ConfirmUpgrade {
		studioJSON(w, http.StatusConflict, map[string]any{
			"code":            "CICADA-VERSION",
			"error":           "This score uses edition 1. Upgrade to edition 2 to save mixer changes?",
			"upgradeRequired": true,
		})
		return
	}
	_ = editionNumber // scoreEdition also verifies the project manifest.

	// The route pins the mixer writer, so an unsupported owner or field keeps
	// the mixer writer's error text.
	intent := &edits.SetParam{Entity: edits.EntityID("param:" + edit.Path), Value: edit.Value, ConfirmUpgrade: edit.ConfirmUpgrade}
	s.applyIntents(w, edit, edits.Envelope{Intents: []edits.Intent{intent}}, edits.ParamWriterMixer, nil)
}

func mixerSourceEdition(path string) (int, error) {
	source, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	score, diagnostics, parseErr := parseScoreForPath(path, source)
	if parseErr != nil {
		return 0, parseErr
	}
	if score == nil || hasDiagnosticErrors(diagnostics) {
		return 0, fmt.Errorf("score must validate before mixer edits")
	}
	return score.Version, nil
}

func buildStudioMixerView(path string, source []byte) (studioMixerView, error) {
	semantic, err := compileStudioSource(path, source)
	if err != nil {
		return studioMixerView{}, err
	}
	score, diagnostics, err := parseScoreForPath(path, source)
	if err != nil {
		return studioMixerView{}, err
	}
	if hasDiagnosticErrors(diagnostics) {
		return studioMixerView{}, fmt.Errorf("score must validate before the Mix view can load")
	}
	score, diagnostics = notation.ResolvePresets(score)
	if hasDiagnosticErrors(diagnostics) {
		return studioMixerView{}, fmt.Errorf("invalid presets: %v", diagnostics)
	}
	root, walker, err := notation.ParseTree(source)
	if err != nil {
		return studioMixerView{}, err
	}
	ranges := mixerSourceRanges(source, root, walker)
	addresses := project.ParamAddresses(semantic)
	addressValues := make(map[string]any, len(addresses))
	for _, address := range addresses {
		addressValues[address.Address] = address.Value
	}

	view := studioMixerView{
		Revision: studioRevision(source), Edition: score.Version, Tempo: float64(semantic.TempoMilli) / 1000,
		Tracks: []mixerStripView{}, Buses: []mixerStripView{}, Returns: []mixerReturnView{}, Effects: []mixerReturnView{},
		Addresses: addresses, Registry: json.RawMessage(project.ParamsJSON()),
	}
	for _, track := range score.Tracks {
		strip := mixerStripView{ID: track.Name, Name: track.Name, Kind: "track", SourceKind: track.Kind, Meter: track.Name, SourceRange: ranges[track.Name], Fields: map[string]mixerField{}, Sends: []mixerSendView{}, Inserts: sourceInsertNames(track.Params), InsertPath: track.Name + ".insert", InsertOK: true}
		for _, field := range []string{"level", "pan", "mute", "solo", "insert", "out"} {
			descriptorID := trackMixerDescriptor(field)
			apiAddress := mixerAddressFor(addresses, track.Name, descriptorID, "track")
			descriptor, found := project.LookupParamDescriptor(descriptorID)
			if !found {
				continue
			}
			value := addressValues[apiAddress]
			if value == nil {
				value = defaultMixerAddressValue(descriptor)
			}
			value = mixerSourceAddressValue(field, trackMixerSourceText(track.Params, field), value)
			if field == "out" {
				value = trackMixerSourceValue(track.Params, "out", addressValues[apiAddress])
			}
			sourceValue := trackMixerSourceText(track.Params, field)
			fieldPath := track.Name + "." + field
			control := mixerField{Path: fieldPath, Address: apiAddress, Value: value, SourceValue: sourceValue, Descriptor: descriptor, SourceRange: ranges[fieldPath], Supported: true}
			control.Supported, control.Reason = mixerFieldCapability("track", track.Name, field, sourceValue, score)
			strip.Fields[field] = control
		}
		strip.InsertAddr = strip.Fields["insert"].Address
		strip.InsertOK, strip.InsertWhy = mixerInsertCapability("track", track.Name, strip.Inserts, score)
		for _, param := range track.Params {
			if param.Name != "send" {
				continue
			}
			fx := findNotationEffect(score, param.Target)
			kind := "unknown"
			address, descriptorID := "", ""
			if fx != nil {
				kind = fx.Kind
				if kind == "delay" {
					descriptorID = "mix.send_a"
				} else if kind == "reverb" {
					descriptorID = "mix.send_b"
				}
			}
			descriptor, found := project.LookupParamDescriptor(descriptorID)
			if !found {
				continue
			}
			address = mixerAddressFor(addresses, track.Name, descriptorID, "track")
			controlPath := track.Name + ".send." + param.Target
			liveValue := addressValues[address]
			if liveValue == nil {
				liveValue = mixerSendAddressValue(param.Value)
			}
			control := mixerSendView{Path: controlPath, Address: address, To: param.Target, Kind: kind, Level: param.Value, Value: liveValue, Pre: param.Pre, Descriptor: descriptor, SourceRange: ranges[controlPath]}
			control.Supported, control.Reason = mixerSendCapability(track, param.Target, kind, score)
			strip.Sends = append(strip.Sends, control)
		}
		view.Tracks = append(view.Tracks, strip)
	}

	busSource := map[string][]notation.Param{}
	for _, bus := range score.Buses {
		busSource[bus.Name] = bus.Params
	}
	for _, busID := range []string{"music", "sfx"} {
		params := busSource[busID]
		strip := mixerStripView{ID: busID, Name: busID, Kind: "bus", Meter: busID, SourceRange: ranges[busID], Fields: map[string]mixerField{}, Inserts: sourceInsertNames(params), InsertPath: busID + ".insert"}
		for _, field := range []string{"level", "pan", "mute", "solo", "insert", "out", "send.delay", "send.reverb"} {
			descriptorID := "mix.bus." + field
			apiAddress := mixerAddressFor(addresses, busID, descriptorID, "bus")
			descriptor, found := project.LookupParamDescriptor(descriptorID)
			if !found {
				continue
			}
			fieldPath := busID + "." + field
			sourceName := strings.Split(field, ".")[0]
			sourceValue := trackMixerSourceText(params, sourceName)
			value := addressValues[apiAddress]
			if value == nil {
				value = defaultMixerAddressValue(descriptor)
			}
			value = mixerSourceAddressValue(sourceName, sourceValue, value)
			control := mixerField{Path: fieldPath, Address: apiAddress, Value: value, SourceValue: sourceValue, Descriptor: descriptor, SourceRange: ranges[fieldPath]}
			if field == "level" {
				if busID == "music" {
					control.Choices = []string{"-3dB", "off"}
				} else {
					control.Choices = []string{"off"}
				}
			}
			control.Supported, control.Reason = mixerFieldCapability("bus", busID, field, sourceValue, score)
			strip.Fields[field] = control
		}
		strip.InsertAddr = strip.Fields["insert"].Address
		strip.InsertOK, strip.InsertWhy = mixerInsertCapability("bus", busID, strip.Inserts, score)
		view.Buses = append(view.Buses, strip)
	}

	masterParams := score.Master
	view.Master = mixerStripView{ID: "master", Name: "Master", Kind: "master", Meter: "master", SourceRange: ranges["master"], Fields: map[string]mixerField{}, Inserts: sourceInsertNames(masterParams), InsertPath: "master.insert"}
	for _, field := range []string{"level", "pan", "mute", "solo", "insert", "out", "send.delay", "send.reverb"} {
		descriptorID := "mix.master." + field
		apiAddress := mixerAddressFor(addresses, "master", descriptorID, "master")
		descriptor, found := project.LookupParamDescriptor(descriptorID)
		if !found {
			continue
		}
		fieldPath := "master." + field
		sourceName := strings.Split(field, ".")[0]
		sourceValue := trackMixerSourceText(masterParams, sourceName)
		value := addressValues[apiAddress]
		if value == nil {
			value = defaultMixerAddressValue(descriptor)
		}
		value = mixerSourceAddressValue(sourceName, sourceValue, value)
		control := mixerField{Path: fieldPath, Address: apiAddress, Value: value, SourceValue: sourceValue, Descriptor: descriptor, SourceRange: ranges[fieldPath]}
		control.Supported, control.Reason = mixerFieldCapability("master", "master", field, sourceValue, score)
		view.Master.Fields[field] = control
	}
	view.Master.InsertAddr = view.Master.Fields["insert"].Address
	view.Master.InsertOK, view.Master.InsertWhy = mixerInsertCapability("master", "master", view.Master.Inserts, score)

	for _, effect := range score.Effects {
		meter := ""
		if effect.Kind == "delay" {
			meter = "a"
		} else if effect.Kind == "reverb" {
			meter = "b"
		}
		strip := mixerReturnView{ID: effect.Name, Name: effect.Name, Kind: effect.Kind, Meter: meter, SourceRange: ranges[effect.Name], Fields: []mixerField{}}
		for _, address := range addresses {
			if !strings.HasPrefix(address.Address, effect.Name+".") {
				continue
			}
			descriptor, found := project.LookupParamDescriptor(address.Param)
			if !found {
				continue
			}
			field := strings.TrimPrefix(address.Address, effect.Name+".")
			fieldPath := effect.Name + "." + field
			strip.Fields = append(strip.Fields, mixerField{
				Path: fieldPath, Address: address.Address, Value: address.Value,
				SourceValue: trackMixerSourceText(effect.Params, descriptor.Source), Descriptor: descriptor,
				SourceRange: ranges[fieldPath], Supported: true,
			})
		}
		view.Effects = append(view.Effects, strip)
		if effect.Kind == "delay" || effect.Kind == "reverb" {
			view.Returns = append(view.Returns, strip)
		}
	}
	return view, nil
}

func trackMixerDescriptor(field string) string {
	switch field {
	case "level":
		return "mix.gain"
	case "pan":
		return "mix.pan"
	case "mute":
		return "mix.mute"
	case "solo":
		return "mix.solo"
	case "insert":
		return "mix.insert"
	case "out":
		return "mix.bus"
	}
	return ""
}

func mixerAddressFor(addresses []project.ParamAddress, owner, parameter, scope string) string {
	for _, address := range addresses {
		if address.Param != parameter {
			continue
		}
		if scope == "track" && address.Track == owner {
			return address.Address
		}
		if scope != "track" && address.Track == "" && strings.HasPrefix(address.Address, owner+".") {
			return address.Address
		}
	}
	return ""
}

func defaultMixerAddressValue(descriptor paramdefs.Descriptor) any {
	if descriptor.Curve == "enum" && descriptor.Default >= 0 && descriptor.Default < float64(len(descriptor.Values)) {
		return descriptor.Values[int(descriptor.Default)]
	}
	return descriptor.Default
}

func mixerSourceAddressValue(field, sourceValue string, fallback any) any {
	if field != "mute" && field != "solo" {
		return fallback
	}
	switch sourceValue {
	case "on", "true":
		return true
	case "off", "false":
		return false
	default:
		return fallback
	}
}

func mixerSendAddressValue(sourceValue string) float64 {
	if strings.HasSuffix(strings.ToLower(sourceValue), "db") {
		number, err := strconv.ParseFloat(sourceValue[:len(sourceValue)-2], 64)
		if err == nil {
			return math.Pow(10, number/20)
		}
	}
	number, _ := strconv.ParseFloat(sourceValue, 64)
	return number
}

func trackMixerSourceValue(params []notation.Param, name string, fallback any) any {
	value := trackMixerSourceText(params, name)
	if value != "" {
		return value
	}
	return fallback
}

func sourceInsertNames(params []notation.Param) []string {
	value := trackMixerSourceText(params, "insert")
	if value == "" || value == "none" {
		return []string{}
	}
	var names []string
	for _, part := range strings.Fields(value) {
		if part != "->" {
			names = append(names, part)
		}
	}
	return names
}

func mixerSourceRanges(source []byte, root *gts.Node, walker *walk.Walker) map[string]mixerLineRange {
	ranges := map[string]mixerLineRange{}
	for i := 0; i < root.NamedChildCount(); i++ {
		decl := root.NamedChild(i)
		kind := walker.Type(decl)
		owner := ""
		switch kind {
		case "track_decl", "bus_decl", "fx_decl":
			owner = walker.Text(walker.Field(decl, "name"))
		case "master_decl":
			owner = "master"
		default:
			continue
		}
		ranges[owner] = sourceRangeAt(source, int(decl.StartByte()), int(decl.EndByte()))
		for j := 0; j < decl.NamedChildCount(); j++ {
			child := decl.NamedChild(j)
			fieldNode := child
			if kind != "fx_decl" {
				if walker.Type(child) != "mix_setting" || child.NamedChildCount() == 0 {
					continue
				}
				fieldNode = child.NamedChild(0)
			}
			switch walker.Type(fieldNode) {
			case "param_decl":
				name := walker.Text(walker.Field(fieldNode, "name"))
				ranges[owner+"."+name] = sourceRangeAt(source, int(child.StartByte()), int(child.EndByte()))
			case "send_decl":
				target := walker.Text(walker.Field(fieldNode, "to"))
				ranges[owner+".send."+target] = sourceRangeAt(source, int(child.StartByte()), int(child.EndByte()))
			}
		}
	}
	return ranges
}

func mixerFieldCapability(kind, owner, field, sourceValue string, score *notation.Score) (bool, string) {
	unsupported := func(reason string) (bool, string) { return false, "CICADA-UNSUPPORTED: " + reason }
	switch kind {
	case "track":
		switch {
		case field == "level" || field == "pan" || field == "mute" || field == "solo":
			return true, ""
		case field == "insert":
			return mixerInsertCapability("track", owner, sourceInsertNames(trackParams(score, owner)), score)
		case field == "out":
			if sourceValue != "" && sourceValue != "music" && sourceValue != "sfx" {
				return unsupported("output bus " + sourceValue + " is not implemented")
			}
			return true, ""
		default:
			return unsupported("track setting " + field + " is not implemented")
		}
	case "bus":
		switch field {
		case "mute", "solo":
			return true, ""
		case "level":
			if owner == "music" && (sourceValue == "" || sourceValue == "-3dB" || sourceValue == "-3db" || sourceValue == "off") {
				return true, ""
			}
			if owner == "sfx" && (sourceValue == "" || sourceValue == "off") {
				return true, ""
			}
			return unsupported("bus " + owner + " level is not implemented for that value")
		case "insert":
			return mixerInsertCapability("bus", owner, sourceInsertNames(busParams(score, owner)), score)
		case "pan":
			return unsupported("bus " + owner + " pan is not implemented")
		case "out":
			return unsupported("bus " + owner + " output routing is not implemented")
		case "send.delay", "send.reverb":
			return unsupported("sends from bus " + owner + " are not implemented")
		default:
			return unsupported("bus setting " + field + " is not implemented")
		}
	case "master":
		switch field {
		case "level", "mute", "solo":
			return true, ""
		case "insert":
			if sourceValue == "" || sourceValue == "none" {
				return true, ""
			}
			return unsupported("master inserts are not implemented")
		case "pan":
			return unsupported("master pan is not implemented")
		case "out", "send.delay", "send.reverb":
			return unsupported("master sends and output routing are not implemented")
		}
	}
	return unsupported(kind + " " + field + " is not implemented")
}

func mixerInsertCapability(kind, owner string, names []string, score *notation.Score) (bool, string) {
	unsupported := func(reason string) (bool, string) { return false, "CICADA-UNSUPPORTED: " + reason }
	if len(names) > 1 {
		return unsupported(kind + " insert chain is not implemented")
	}
	if len(names) == 0 {
		return true, ""
	}
	effect := findNotationEffect(score, names[0])
	if effect == nil {
		return unsupported("insert references undeclared effect " + names[0])
	}
	if kind == "track" && effect.Kind != "drive" {
		return unsupported(effect.Kind + " as a track insert is not implemented")
	}
	if kind == "bus" && (owner != "music" || effect.Kind != "comp") {
		return unsupported("music bus insert must be a compressor")
	}
	if kind == "master" {
		return unsupported("master inserts are not implemented")
	}
	return true, ""
}

func mixerSendCapability(track notation.Track, target, kind string, score *notation.Score) (bool, string) {
	if kind != "delay" && kind != "reverb" {
		return false, "CICADA-UNSUPPORTED: send target must be a delay or reverb return"
	}
	for _, param := range track.Params {
		if param.Name != "send" || param.Target == target {
			continue
		}
		other := findNotationEffect(score, param.Target)
		if other != nil && other.Kind == kind {
			return false, "CICADA-UNSUPPORTED: one send per effect kind is implemented"
		}
	}
	return true, ""
}

func studioWriteAuxiliaryFile(file studioAuxiliaryFile) error {
	current, err := os.ReadFile(file.Path)
	if os.IsNotExist(err) && file.Before == nil {
		current, err = nil, nil
	}
	if err != nil {
		return err
	}
	if !bytes.Equal(current, file.Before) {
		return fmt.Errorf("%s changed during source edit", filepath.Base(file.Path))
	}
	if file.Before != nil {
		committed, _, err := studioWriteIfRevision(file.Path, file.After, file.Mode, studioRevision(file.Before), nil)
		if err != nil {
			return err
		}
		if !committed {
			return fmt.Errorf("auxiliary source changed during commit")
		}
		return nil
	}
	dir, err := studioRecoveryDir(file.Path)
	if err != nil {
		return err
	}
	stage, err := os.CreateTemp(dir, ".cicada-studio-manifest-*")
	if err != nil {
		return err
	}
	stagePath := stage.Name()
	defer os.Remove(stagePath)
	if err := stage.Chmod(file.Mode); err != nil {
		stage.Close()
		return err
	}
	if _, err := stage.Write(file.After); err != nil {
		stage.Close()
		return err
	}
	if err := stage.Sync(); err != nil {
		stage.Close()
		return err
	}
	if err := stage.Close(); err != nil {
		return err
	}
	latest, err := os.ReadFile(file.Path)
	if os.IsNotExist(err) && file.Before == nil {
		latest, err = nil, nil
	}
	if err != nil || !bytes.Equal(latest, file.Before) {
		return fmt.Errorf("%s changed during source edit", filepath.Base(file.Path))
	}
	return os.Rename(stagePath, file.Path)
}

func studioWriteAuxiliaryFiles(files []studioAuxiliaryFile) error {
	for i, file := range files {
		if err := studioWriteAuxiliaryFile(file); err != nil {
			for j := i - 1; j >= 0; j-- {
				previous := files[j]
				previous.Before, previous.After = previous.After, previous.Before
				if rollbackErr := studioWriteAuxiliaryFile(previous); rollbackErr != nil {
					err = fmt.Errorf("%w; auxiliary rollback: %v", err, rollbackErr)
				}
			}
			return err
		}
	}
	return nil
}

// The mixer writer lives in the edit package; these names keep the view code
// and the other routes readable.
var (
	trackMixerSourceText  = edits.TrackMixerSourceText
	trackParams           = edits.TrackParams
	busParams             = edits.BusParams
	findNotationEffect    = edits.FindNotationEffect
	sourceRangeAt         = edits.SourceRangeAt
	validMixerIdentifier  = edits.ValidMixerIdentifier
	canonicalMixerLiteral = edits.CanonicalMixerLiteral
)
