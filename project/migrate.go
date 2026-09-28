package project

import (
	"encoding/json"
	"fmt"
)

// Migrate1To2 returns a detached project value with the v2 semantic format
// marker. It never mutates the input. CanonicalJSON still chooses /1 when the
// returned project has no /2-only scene settings.
func Migrate1To2(source *Project) (*Project, error) {
	if source == nil || source.Format != FormatID || source.Version != 1 {
		return nil, fmt.Errorf("Migrate1To2 requires cicada.project/1")
	}
	if err := ValidateProject(source); err != nil {
		return nil, err
	}
	data, err := json.Marshal(source)
	if err != nil {
		return nil, err
	}
	var migrated Project
	if err := json.Unmarshal(data, &migrated); err != nil {
		return nil, err
	}
	migrated.Format, migrated.Version = FormatID2, 2
	migrateMixerFields(&migrated)
	return &migrated, nil
}

func migrateMixerFields(project *Project) {
	if project.Buses == nil {
		project.Buses = []Bus{}
	}
	effectByKind := map[string]string{}
	legacyComp := false
	for _, effect := range project.Effects {
		kind := semanticEffectKind(effect)
		effectByKind[kind] = effect.ID
		legacyComp = legacyComp || effect.Kind == "" && kind == "comp"
	}
	for i := range project.Tracks {
		mixer := &project.Tracks[i].Mixer
		mixer.wireV2 = true
		if mixer.GainDB != -6 {
			gain := mixer.GainDB
			mixer.Level = &Value{Unit: "db", Number: &gain}
		}
		mixer.panSet = mixer.Pan != 0
		mixer.muteSet = mixer.Mute
		mixer.soloSet = mixer.Solo
		if mixer.Inserts == nil && mixer.Insert != "none" {
			mixer.Inserts = []string{mixer.Insert}
		}
		if mixer.Out == nil && mixer.Bus != "music" {
			mixer.Out = stringPointer(mixer.Bus)
		}
		if mixer.Sends == nil {
			mixer.Sends = []MixerSend{}
		}
		for _, send := range []struct {
			kind string
			gain float64
		}{{"delay", mixer.SendA}, {"reverb", mixer.SendB}} {
			if send.gain <= 0 {
				continue
			}
			id := effectByKind[send.kind]
			if id == "" {
				continue
			}
			amount := send.gain
			tap := "post"
			if mixer.SendPre {
				tap = "pre"
			}
			mixer.Sends = append(mixer.Sends, MixerSend{To: id, Level: Value{Unit: "ratio", Number: &amount}, Tap: tap})
		}
	}
	if legacyComp {
		found := false
		for _, bus := range project.Buses {
			found = found || bus.ID == "music"
		}
		if !found {
			project.Buses = append(project.Buses, Bus{ID: "music", Mixer: Mixer{
				GainDB: -3, Insert: "comp", Bus: "music", Inserts: []string{"comp"}, wireV2: true,
			}})
		}
	}
	for i := range project.Buses {
		bus := &project.Buses[i]
		bus.Mixer.wireV2 = true
		if bus.Mixer.Bus == "" {
			bus.Mixer.Bus = bus.ID
		}
		if bus.ID == "music" && bus.Mixer.GainDB == -6 {
			bus.Mixer.GainDB = -3
		} else if bus.ID == "sfx" && bus.Mixer.GainDB == -6 {
			bus.Mixer.GainDB = 0
		}
	}
	for i := range project.Effects {
		if project.Effects[i].Kind != "" {
			continue
		}
		kind := semanticEffectKind(project.Effects[i])
		// Keep the legacy kind implicit unless another /2 construct (such as
		// the explicit music compressor route) requires a /2 record.
		if legacyComp && kind == "comp" {
			project.Effects[i].Kind = kind
		}
	}
}
