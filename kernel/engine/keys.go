package engine

import (
	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/voice/keyboard"
)

// keysControlFirst..keysControlLast are the contiguous registry IDs for the
// modeled-keyboard controls. keysControls is indexed by ID - keysControlFirst.
const (
	keysControlFirst = kernel.ParamKeysEpTremolo
	keysControlLast  = kernel.ParamKeysStringsOutput
	keysControlCount = int(keysControlLast-keysControlFirst) + 1
)

// keysControls maps each registry ID to its keyboard family and Spec.Controls
// slot. The same slots set initial values in the prepared project.
var keysControls = [keysControlCount]struct {
	family keyboard.Family
	slot   uint8
}{
	kernel.ParamKeysEpTremolo - keysControlFirst:       {keyboard.EP, 7},
	kernel.ParamKeysEpTremoloRate - keysControlFirst:   {keyboard.EP, 8},
	kernel.ParamKeysEpAutoPan - keysControlFirst:       {keyboard.EP, 9},
	kernel.ParamKeysEpGain - keysControlFirst:          {keyboard.EP, 10},
	kernel.ParamKeysEpDrive - keysControlFirst:         {keyboard.EP, 6},
	kernel.ParamKeysClavGain - keysControlFirst:        {keyboard.Clav, 6},
	kernel.ParamKeysClavDrive - keysControlFirst:       {keyboard.Clav, 5},
	kernel.ParamKeysOrganGain - keysControlFirst:       {keyboard.Organ, 21},
	kernel.ParamKeysOrganDrive - keysControlFirst:      {keyboard.Organ, 17},
	kernel.ParamKeysOrganRotaryMix - keysControlFirst:  {keyboard.Organ, 18},
	kernel.ParamKeysOrganRotaryFast - keysControlFirst: {keyboard.Organ, 20},
	kernel.ParamKeysOrganDrawbar1 - keysControlFirst:   {keyboard.Organ, 1},
	kernel.ParamKeysOrganDrawbar2 - keysControlFirst:   {keyboard.Organ, 2},
	kernel.ParamKeysOrganDrawbar3 - keysControlFirst:   {keyboard.Organ, 3},
	kernel.ParamKeysOrganDrawbar4 - keysControlFirst:   {keyboard.Organ, 4},
	kernel.ParamKeysOrganDrawbar5 - keysControlFirst:   {keyboard.Organ, 5},
	kernel.ParamKeysOrganDrawbar6 - keysControlFirst:   {keyboard.Organ, 6},
	kernel.ParamKeysOrganDrawbar7 - keysControlFirst:   {keyboard.Organ, 7},
	kernel.ParamKeysOrganDrawbar8 - keysControlFirst:   {keyboard.Organ, 8},
	kernel.ParamKeysOrganDrawbar9 - keysControlFirst:   {keyboard.Organ, 9},
	kernel.ParamKeysFmGain - keysControlFirst:          {keyboard.FM, 1},
	kernel.ParamKeysAnalogDrive - keysControlFirst:     {keyboard.Analog, 14},
	kernel.ParamKeysAnalogOutput - keysControlFirst:    {keyboard.Analog, 21},
	kernel.ParamKeysStringsOutput - keysControlFirst:   {keyboard.Strings, 10},
}

func isKeysControl(id kernel.ParamID) bool { return id >= keysControlFirst && id <= keysControlLast }

// IsKeysControl reports whether a registry ID is a modeled-keyboard control.
func IsKeysControl(id kernel.ParamID) bool { return isKeysControl(id) }

// KeysControlSlot returns the Spec.Controls slot a keyboard patch uses for a
// registry control, or false when the patch belongs to another family.
func KeysControlSlot(patch uint8, id kernel.ParamID) (int, bool) {
	if !keysEnabled || !isKeysControl(id) || keysControls[id-keysControlFirst].family != keyboard.FamilyOf(patch) {
		return 0, false
	}
	return int(keysControls[id-keysControlFirst].slot), true
}

// KeysControlFits reports whether a prepared track is a keyboard of the
// family that owns the control. Hosts use it to reject a scene setting or an
// automation lane before playback.
func KeysControlFits(track *TrackConfig, id kernel.ParamID) bool {
	return keysEnabled && isKeysControl(id) && track.Kind == VoiceKeys && track.Keys != nil &&
		keysControls[id-keysControlFirst].family == keyboard.FamilyOf(track.Keys.Patch)
}

// initKeysControls records the prepared controls so a scene change can restore
// them, and resolves the voice's live-control interface once.
func (v *voiceSlot) initKeysControls(spec *keyboard.Spec) {
	v.keysFamily = keyboard.FamilyOf(spec.Patch)
	v.keysControl, _ = v.keys.(keyboard.Controller)
	for n := range keysControls {
		if keysControls[n].family == v.keysFamily {
			v.keysValues[n] = spec.Controls[keysControls[n].slot]
		}
	}
}

// TrackKeysFamily reports the keyboard family of a track, or NoFamily.
func (e *Engine) TrackKeysFamily(track int) keyboard.Family {
	if !keysEnabled || track < 0 || track >= e.tracks || e.voices[track].kind != VoiceKeys {
		return keyboard.NoFamily
	}
	return e.voices[track].keysFamily
}

// setKeysControl applies one live keyboard control. Controls of another family
// fault, exactly like other parameters aimed at a voice that lacks them.
func (e *Engine) setKeysControl(v *voiceSlot, id kernel.ParamID, value float32, immediate bool) {
	entry := &keysControls[id-keysControlFirst]
	if v.kind != VoiceKeys || v.keysControl == nil || entry.family != v.keysFamily {
		e.fault(FaultParam)
		return
	}
	glide := e.paramAlpha[id]
	if immediate {
		glide = 1
	}
	if v.keysControl.SetControl(int(entry.slot), value, glide) != nil {
		e.fault(FaultParam)
		return
	}
	v.keysValues[id-keysControlFirst] = value
}

// restoreKeysControls returns every changed control to its saved value. The
// caller reports the fault.
func (*Engine) restoreKeysControls(v *voiceSlot, saved *[keysControlCount]float32) bool {
	for n := range keysControls {
		if keysControls[n].family != v.keysFamily || v.keysValues[n] == saved[n] {
			continue
		}
		if v.keysControl.SetControl(int(keysControls[n].slot), saved[n], 1) != nil {
			return false
		}
		v.keysValues[n] = saved[n]
	}
	return true
}

func (e *Engine) releaseKeysPattern(track int) {
	if keysEnabled {
		p := &e.patterns[track]
		for n := uint8(0); n < p.playingPitchCount; n++ {
			e.voices[track].keys.NoteOff(p.playingPitches[n])
		}
		p.playingPitchCount = 0
	}
}
