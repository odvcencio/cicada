// Package gamescore compiles a Cicada project into the score pack a GoSX game
// ships (see sdk/gosxhost). It runs at build time; the browser runs only the
// kernel module and the pack.
package gamescore

import (
	"encoding/binary"
	"fmt"

	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/host/schedule"
	"m31labs.dev/cicada/kernel/limits"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/sdk/gosxhost"
)

// Build compiles p, whose assets resolve against root, into a pack for an
// AudioContext running at rate (44100, 48000 or 96000 Hz). The project needs a
// live block: its states, macros and stingers become the pack's control
// manifest.
func Build(p *project.Project, root string, rate int) (*gosxhost.Pack, error) {
	if tracks := len(p.Tracks); tracks > limits.PortableMaxTracks {
		return nil, fmt.Errorf("a browser kernel supports at most %d tracks, and this score has %d (cicada check --profile browser lists them)", limits.PortableMaxTracks, tracks)
	}
	surface, err := project.DirectorSurfaceOf(p, rate)
	if err != nil {
		return nil, err
	}
	cfg, err := schedule.CompileResident(p, root, rate, 128)
	if err != nil {
		return nil, err
	}
	image, err := kernelimage.Encode(cfg)
	if err != nil {
		return nil, err
	}
	return &gosxhost.Pack{
		SampleRate:           rate,
		TempoMilli:           binary.LittleEndian.Uint32(image[12:16]),
		RequiredCapabilities: ImageCapabilities(image),
		Kernel:               KernelFlavor(image),
		Image:                image,
		Surface:              surface,
	}, nil
}

// ImageCapabilities is the kernel capability mask an image needs: the 16 bits
// its header lists, plus the bits that image versions 15 to 18 imply.
func ImageCapabilities(image []byte) uint32 {
	mask := uint32(binary.LittleEndian.Uint16(image[30:32]))
	version := int(binary.LittleEndian.Uint16(image[4:6]))
	for i, bit := range []uint{16, 18, 19, 21} {
		if version >= 15+i {
			mask |= 1 << bit
		}
	}
	return mask
}

// KernelFlavor picks the smallest kernel module that runs the image, by the
// same rule cicada wam2 uses.
func KernelFlavor(image []byte) uint8 {
	required := binary.LittleEndian.Uint16(image[30:32])
	modular := binary.LittleEndian.Uint32(image[8:12])&(kernelimage.ModulatorFlag|kernelimage.MacroMappingFlag|kernelimage.InstrumentVibratoFlag|kernelimage.SamplerExpressionFlag|kernelimage.ChorusFlag|kernelimage.GraphLFOFlag) != 0
	switch {
	case modular && required&kernelimage.KeysCapability != 0:
		return gosxhost.KernelKeysModular
	case modular:
		return gosxhost.KernelModular
	case required&kernelimage.KeysCapability != 0:
		return gosxhost.KernelKeys
	case required&kernelimage.TrackEffectsCapability != 0:
		return gosxhost.KernelVocals
	}
	return gosxhost.KernelCore
}
