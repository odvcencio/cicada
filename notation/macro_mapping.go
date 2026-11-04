package notation

import "m31labs.dev/cicada/internal/paramdefs"

func validateMappedMacros(s *Score) []Diagnostic {
	if s.Live == nil {
		return nil
	}
	var ds []Diagnostic
	seen := map[string]bool{}
	for _, m := range s.Live.Macros {
		if m.Target == "" {
			continue
		}
		add := func(message string) {
			ds = append(ds, Diagnostic{Code: "CICADA-LIVE-MACRO", Severity: "error", Message: message, Position: m.Position})
		}
		desc, _, _, code, message := resolveNotationPath(s, m.Target)
		if code != "" {
			add(message)
			continue
		}
		if !desc.Live || desc.Curve == "toggle" || desc.Type == "integer" || desc.Type == "boolean" {
			add("mapped macro requires a continuous live parameter")
			continue
		}
		if seen[m.Target] {
			add("duplicate mapped macro target " + m.Target)
		}
		seen[m.Target] = true
		if m.Curve != "" && m.Curve != "linear" && m.Curve != "exp" {
			add("macro curve must be linear or exp")
		}
		if err := validateSceneValue(desc, m.Target, m.Min); err != nil {
			add("invalid macro minimum: " + err.Error())
			continue
		}
		if err := validateSceneValue(desc, m.Target, m.Max); err != nil {
			add("invalid macro maximum: " + err.Error())
			continue
		}
		min, _, minErr := paramdefs.ParseBase(m.Min)
		max, _, maxErr := paramdefs.ParseBase(m.Max)
		if minErr != nil || maxErr != nil || float32(min) >= float32(max) {
			add("macro range minimum must be below maximum")
			continue
		}
		if m.Curve == "exp" && min <= 0 {
			add("exponential macro ranges require a positive minimum")
		}
	}
	return ds
}
