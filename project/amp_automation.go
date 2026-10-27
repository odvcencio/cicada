package project

import (
	"fmt"
	"m31labs.dev/cicada/kernel/fx/ampcab"
)

// trackAmpParams resolves a track-owned control only when its target is unique.
func trackAmpParams(p *Project, track Track) (ampcab.Params, error) {
	var effect *Effect
	for _, name := range mixerInsertIDs(track.Mixer) {
		for i := range p.Effects {
			e := &p.Effects[i]
			if e.ID == name && semanticEffectKind(*e) == "ampcab" {
				if effect != nil {
					return ampcab.Params{}, fmt.Errorf("amp controls require exactly one amp insert")
				}
				effect = e
			}
		}
	}
	if effect == nil {
		return ampcab.Params{}, fmt.Errorf("amp controls require exactly one amp insert")
	}
	return AmpCabParamsFromValues(effect.Params)
}
