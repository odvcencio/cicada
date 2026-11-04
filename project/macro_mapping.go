package project

import (
	"fmt"
	"m31labs.dev/cicada/internal/paramdefs"
	"m31labs.dev/cicada/kernel"
)

func validateMappedMacrosProject(p *Project) error {
	seen := map[[2]uint16]bool{}
	for _, macro := range p.Live.Macros {
		m := macro.Mapping
		if m == nil {
			continue
		}
		if macro.Default != 0 {
			return fmt.Errorf("CICADA-LIVE-MACRO: mapped macros start at zero")
		}
		resolved, err := ResolveParameterPath(p, m.Target)
		if err != nil {
			return err
		}
		if !resolved.Descriptor.Live || resolved.Descriptor.Curve == "toggle" || kernel.IsInteger(resolved.ID) {
			return fmt.Errorf("CICADA-LIVE-MACRO: mapped macro requires a continuous live parameter")
		}
		key := [2]uint16{uint16(resolved.Track), uint16(resolved.ID)}
		if seen[key] {
			return fmt.Errorf("CICADA-LIVE-MACRO: duplicate mapped macro target %s", m.Target)
		}
		seen[key] = true
		if m.Curve != "" && m.Curve != "linear" && m.Curve != "exp" {
			return fmt.Errorf("CICADA-LIVE-MACRO: invalid mapping curve")
		}
		for _, v := range []Value{m.Min, m.Max} {
			literal, err := valueSource(v)
			if err != nil {
				return err
			}
			if _, err := paramdefs.ValidateLiteral(resolved.Descriptor, literal); err != nil {
				return err
			}
		}
		if m.Min.Number == nil || m.Max.Number == nil || float32(*m.Min.Number) >= float32(*m.Max.Number) || m.Curve == "exp" && *m.Min.Number <= 0 {
			return fmt.Errorf("CICADA-LIVE-MACRO: invalid mapping range")
		}
	}
	return nil
}
