package control

import (
	"fmt"
	"math"
)

// VocalPhrase keeps the level with the chosen range, rather than the raw take.
type VocalPhrase struct {
	Take        string   `json:"take"`
	Start       int64    `json:"startTick"`
	End         int64    `json:"endTick"`
	GainDB      float64  `json:"gainDB,omitempty"`
	OffsetMS    float64  `json:"offsetMS,omitempty"`
	FadeMS      *float64 `json:"fadeMS,omitempty"`
	CrossfadeMS *float64 `json:"crossfadeMS,omitempty"`
}

type VocalEdits struct{ OffsetMS, FadeMS, CrossfadeMS float64 }

func VocalPhraseEdits(phrases []VocalPhrase, take string, r VocalRange) VocalEdits {
	value := VocalEdits{FadeMS: 5}
	for _, p := range phrases {
		if p.Take == take && r.Start >= p.Start && r.End <= p.End && r.End > r.Start {
			value.OffsetMS = p.OffsetMS
			if p.FadeMS != nil {
				value.FadeMS = *p.FadeMS
			}
			if p.CrossfadeMS != nil {
				value.CrossfadeMS = *p.CrossfadeMS
			}
			return value
		}
	}
	return value
}

func ValidVocalGain(db float64) bool {
	return !math.IsNaN(db) && !math.IsInf(db, 0) && db >= -24 && db <= 18
}

// A selection wholly inside one chosen phrase inherits its level. New passes
// and selections spanning different levels start at unity.
func VocalPhraseGain(phrases []VocalPhrase, take string, r VocalRange) float64 {
	for _, p := range phrases {
		if p.Take == take && r.Start >= p.Start && r.End <= p.End && r.End > r.Start && ValidVocalGain(p.GainDB) {
			return p.GainDB
		}
	}
	return 0
}

func VocalGainText(db float64) string {
	if !ValidVocalGain(db) || math.Abs(db) < .05 {
		return "0.0 dB"
	}
	return fmt.Sprintf("%+.1f dB", db)
}
