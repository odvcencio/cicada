package kernelimage

import (
	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/engine"
)

// usesEchoHighpass reports whether a score sets or moves the in-loop
// high-pass of its delay. The core kernel's parameter table ends before the
// rows that follow the keyboard block, where the echo high-pass rows sit, so a
// portable image could not address the control; carrying it needs a new image
// version and a core table that reaches those rows.
func usesEchoHighpass(cfg *engine.Config) bool {
	echo := func(id uint16) bool {
		return id == uint16(kernel.ParamFxDelayHighpass) || id == uint16(kernel.ParamFxTapeHighpass)
	}
	if cfg.DelayA != nil && cfg.DelayA.HighpassHz != 0 {
		return true
	}
	for _, scene := range cfg.Scenes {
		for _, setting := range scene.Settings {
			if echo(uint16(setting.ID)) {
				return true
			}
		}
	}
	for _, control := range cfg.Automation {
		if echo(control.Index) {
			return true
		}
	}
	for _, route := range cfg.Routes {
		if echo(route.Param) {
			return true
		}
	}
	for _, mapping := range cfg.MacroMappings {
		if echo(mapping.Param) {
			return true
		}
	}
	return false
}
