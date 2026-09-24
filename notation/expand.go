package notation

// expandPhrases lowers bounded phrase uses to ordinary step tokens. Original
// parts remain in the AST so editors can show the authored program.
func expandPhrases(s *Score) []Diagnostic {
	var ds []Diagnostic
	phrases := make(map[string]Phrase, len(s.Phrases))
	for _, phrase := range s.Phrases {
		if _, exists := phrases[phrase.Name]; exists {
			ds = append(ds, Diagnostic{"CICADA-DUPLICATE", "duplicate phrase " + phrase.Name, "error", phrase.Position})
		}
		phrases[phrase.Name] = phrase
		if len(phrase.Steps) < 1 || len(phrase.Steps) > 64 {
			ds = append(ds, Diagnostic{"CICADA-PHRASE", "phrase must have 1 to 64 steps", "error", phrase.Position})
		}
	}
	for pi := range s.Patterns {
		p := &s.Patterns[pi]
		if p.Kind != "acid" && p.Kind != "notes" {
			continue
		}
		for _, part := range p.Parts {
			if part.Step != nil {
				if len(p.Steps) < 64 {
					p.Steps = append(p.Steps, *part.Step)
				} else {
					ds = append(ds, Diagnostic{"CICADA-EXPANSION", "pattern expands beyond 64 steps", "error", part.Step.Position})
					break
				}
				continue
			}
			use := part.Use
			if use == nil {
				continue
			}
			phrase, ok := phrases[use.Name]
			if !ok {
				ds = append(ds, Diagnostic{"CICADA-REFERENCE", "unknown phrase " + use.Name, "error", use.Position})
				continue
			}
			if use.Repeat < 1 || use.Repeat > 64 || use.Transpose < -24 || use.Transpose > 24 {
				ds = append(ds, Diagnostic{"CICADA-USE", "phrase repeat must be 1 to 64 and transpose -24 to 24", "error", use.Position})
				continue
			}
			if len(p.Steps)+len(phrase.Steps)*use.Repeat > 64 {
				ds = append(ds, Diagnostic{"CICADA-EXPANSION", "pattern expands beyond 64 steps", "error", use.Position})
				continue
			}
			for range use.Repeat {
				for _, step := range phrase.Steps {
					step.Transpose = use.Transpose
					p.Steps = append(p.Steps, step)
				}
			}
		}
	}
	return ds
}
